package godotnet

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"time"

	"google.golang.org/protobuf/proto"

	controlpb "github.com/jordanstites/godotnet/internal/proto"
)

// handleLogin processes a parsed Login message. On success it
// transitions the session to sessionAwaitingUDP and replies with
// LoginResponse over TCP.
func (s *Server) handleLogin(_ *tickCtx, sess *Session, loginMsg *controlpb.Login) {
	if s.cfg.Authenticate == nil || s.cfg.LoginPrototype == nil {
		s.log.Error("Login received but Authenticate/LoginPrototype not configured")
		s.sendLoginResponse(sess, false, "server misconfigured", 0, "")
		s.scheduleDisconnect(sess, ErrServerMisconfigured)
		return
	}

	credentials := proto.Clone(s.cfg.LoginPrototype)
	proto.Reset(credentials)
	if err := proto.Unmarshal(loginMsg.GetCredentials(), credentials); err != nil {
		s.log.Debug("malformed credentials", "err", err)
		s.sendLoginResponse(sess, false, "malformed credentials", 0, "")
		s.scheduleDisconnect(sess, fmt.Errorf("%w: credentials", ErrMalformedFrame))
		return
	}

	playerID, err := s.callAuthenticate(credentials)
	if err != nil {
		s.log.Info("auth rejected", "err", err)
		s.sendLoginResponse(sess, false, err.Error(), 0, "")
		s.scheduleDisconnect(sess, fmt.Errorf("%w: %v", ErrAuthRejected, err))
		return
	}

	if _, taken := s.sessions[playerID]; taken {
		s.log.Info("duplicate player id; rejecting login", "player", playerID)
		s.sendLoginResponse(sess, false, "already connected", 0, "")
		s.scheduleDisconnect(sess, fmt.Errorf("%w: duplicate", ErrAuthRejected))
		return
	}

	token := generateSessionToken()
	sess.ID = playerID
	sess.sessionToken = token
	sess.authState = sessionAwaitingUDP
	s.sessions[playerID] = sess

	if cb := s.cfg.OnAuth; cb != nil {
		s.invokeOnAuth(cb, sess, credentials)
	}

	udpEndpoint := s.cfg.UDPAdvertiseAddr
	if udpEndpoint == "" {
		udpEndpoint = s.cfg.UDPAddr
	}
	s.sendLoginResponse(sess, true, "", uint32(playerID), token, udpEndpoint)
	s.log.Info("login accepted", "player", playerID, "remote", remoteAddrString(sess.tcp))
}

// remoteAddrString returns conn.RemoteAddr().String() or "" if conn is
// nil. Useful for log lines on sessions that lack an underlying conn
// (e.g. in tests).
func remoteAddrString(conn net.Conn) string {
	if conn == nil {
		return ""
	}
	if a := conn.RemoteAddr(); a != nil {
		return a.String()
	}
	return ""
}

// callAuthenticate invokes the user's Authenticate callback with panic
// recovery. The context passed in is fresh per call so user code can
// observe cancellation if the server shuts down.
func (s *Server) callAuthenticate(credentials proto.Message) (id PlayerID, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: panic %v", ErrAuthRejected, r)
		}
	}()
	return s.cfg.Authenticate(context.Background(), credentials)
}

// sendLoginResponse wraps a LoginResponse in a ServerFrame and queues
// it on the player's TCP outbound channel.
func (s *Server) sendLoginResponse(sess *Session, ok bool, errMsg string, playerID uint32, token string, udpEndpoint ...string) {
	endpoint := ""
	if len(udpEndpoint) > 0 {
		endpoint = udpEndpoint[0]
	}
	frame := &controlpb.ServerFrame{
		Body: &controlpb.ServerFrame_LoginResponse{
			LoginResponse: &controlpb.LoginResponse{
				Ok:           ok,
				ErrorMessage: errMsg,
				PlayerId:     playerID,
				SessionToken: token,
				UdpEndpoint:  endpoint,
			},
		},
	}
	data, err := proto.Marshal(frame)
	if err != nil {
		s.log.Error("marshal LoginResponse frame", "err", err)
		return
	}
	s.pushTCP(sess, data)
}

// handleUDPHandshake processes a parsed UdpHandshake datagram. On
// success it pairs the UDP address with the existing pending session.
func (s *Server) handleUDPHandshake(_ *tickCtx, addr net.Addr, hs *controlpb.UdpHandshake) {
	sess, ok := s.sessions[PlayerID(hs.GetPlayerId())]
	if !ok || sess.sessionToken != hs.GetSessionToken() {
		s.log.Info("UDP handshake rejected",
			"player", hs.GetPlayerId(),
			"from", addr,
			"reason", "unknown player or bad token",
		)
		s.sendUDPHandshakeAck(addr, false, hs.GetPlayerId())
		return
	}
	if sess.authState != sessionAwaitingUDP {
		s.log.Info("UDP handshake for non-pending session",
			"player", sess.ID,
			"state", sess.authState,
		)
		return
	}

	sess.udpAddr = addr
	sess.authState = sessionReady
	sess.lastSeenUnixNano.Store(time.Now().UnixNano())
	s.udpSessions[addr.String()] = sess

	s.sendUDPHandshakeAck(addr, true, uint32(sess.ID))

	if cb := s.cfg.OnConnect; cb != nil {
		s.invokeOnConnect(cb, sess)
	}
	s.log.Info("session paired",
		"player", sess.ID,
		"udp", addr,
		"tcp", remoteAddrString(sess.tcp),
	)
}

// sendUDPHandshakeAck wraps a UdpHandshakeAck in a ServerFrame and
// writes it directly to addr (one-shot send, no per-session queue).
func (s *Server) sendUDPHandshakeAck(addr net.Addr, ok bool, playerID uint32) {
	frame := &controlpb.ServerFrame{
		Body: &controlpb.ServerFrame_UdpHandshakeAck{
			UdpHandshakeAck: &controlpb.UdpHandshakeAck{
				Ok:       ok,
				PlayerId: playerID,
			},
		},
	}
	data, err := proto.Marshal(frame)
	if err != nil {
		s.log.Error("marshal UdpHandshakeAck frame", "err", err)
		return
	}
	s.udpSendRaw(addr, data)
}

// generateSessionToken returns 16 random bytes encoded as URL-safe
// base64. 128 bits is plenty for a per-session pairing secret.
func generateSessionToken() string {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		// crypto/rand failing means the OS RNG is broken — fall back
		// to a deterministic-but-still-unique value rather than panic.
		// Such failures are practically impossible in real deployments.
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
