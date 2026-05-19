package godotnet

import (
	"crypto/rand"
	"encoding/binary"
	"time"

	"google.golang.org/protobuf/proto"

	controlpb "github.com/jordanstites/godotnet/controlpb"
)

// DefaultPingInterval is the period between server-initiated UDP Ping
// datagrams to each ready session.
const DefaultPingInterval = 5 * time.Second

// DefaultPingTimeout is the time without any UDP traffic from a session
// after which the server disconnects it as a zombie.
const DefaultPingTimeout = 30 * time.Second

// handlePong updates the session's last-seen timestamp. The nonce is
// not currently validated against lastPingNonce — any Pong is taken as
// a liveness signal, which is good enough for v0.1 NAT keepalive.
func (s *Server) handlePong(tc *tickCtx, sess *Session, _ *controlpb.Pong) {
	sess.lastSeenUnixNano.Store(tc.now.UnixNano())
}

// runPingTick is called once per tick. It batches Ping sends every
// PingInterval and disconnects sessions that have been quiet for
// longer than PingTimeout.
func (s *Server) runPingTick(tc *tickCtx) {
	interval := s.cfg.PingInterval
	if interval <= 0 {
		interval = DefaultPingInterval
	}
	timeout := s.cfg.PingTimeout
	if timeout <= 0 {
		timeout = DefaultPingTimeout
	}

	sendPings := tc.now.Sub(s.lastPingBatchAt) >= interval
	if sendPings {
		s.lastPingBatchAt = tc.now
	}

	timeoutNanos := tc.now.Add(-timeout).UnixNano()

	for _, sess := range s.sessions {
		if sess.authState != sessionReady {
			continue
		}
		if sess.udpAddr == nil {
			continue
		}
		if sess.lastSeenUnixNano.Load() < timeoutNanos {
			s.log.Info("session timed out", "player", sess.ID)
			s.scheduleDisconnect(sess, ErrPingTimeout)
			continue
		}
		if sendPings {
			s.sendPing(sess)
		}
	}
}

// sendPing builds and writes a Ping datagram to sess's UDP address.
func (s *Server) sendPing(sess *Session) {
	var nonceBytes [8]byte
	if _, err := rand.Read(nonceBytes[:]); err != nil {
		// crypto/rand failure: fall back to a tick-derived nonce.
		// The library only uses the nonce as a debugging aid.
		binary.BigEndian.PutUint64(nonceBytes[:], uint64(time.Now().UnixNano()))
	}
	nonce := binary.BigEndian.Uint64(nonceBytes[:])
	sess.lastPingNonce = nonce

	frame := &controlpb.ServerFrame{
		Body: &controlpb.ServerFrame_Ping{
			Ping: &controlpb.Ping{Nonce: nonce},
		},
	}
	data, err := proto.Marshal(frame)
	if err != nil {
		s.log.Error("marshal Ping frame", "err", err)
		return
	}
	s.udpSendRaw(sess.udpAddr, data)
}
