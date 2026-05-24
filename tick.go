package godotnet

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"

	controlpb "github.com/jordanstites/godotnet/internal/proto"
)

// runTickLoop drives the simulation tick. It owns the per-tick sequence:
// drain event queue → dispatch handlers → invoke OnTick → sleep until next
// tick. Returns ctx.Err() when ctx is cancelled.
func (s *Server) runTickLoop(ctx context.Context) error {
	ticker := time.NewTicker(s.cfg.TickRate)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case t := <-ticker.C:
			s.tickCount++
			tc := &tickCtx{server: s, tick: s.tickCount, now: t}

			for _, env := range s.events.drain() {
				s.handleEvent(tc, env)
			}

			s.runPingTick(tc)

			s.mu.Lock()
			cb := s.onTick
			s.mu.Unlock()
			if cb != nil {
				s.invokeOnTick(cb, tc)
			}
		}
	}
}

// handleEvent processes one inbound event on the tick goroutine.
func (s *Server) handleEvent(tc *tickCtx, env event) {
	switch env.kind {
	case eventMessage:
		s.dispatchMessage(tc, env)
	case eventConnect:
		// Reserved for future use — currently OnConnect fires from the
		// UDP-handshake path directly.
		if cb := s.cfg.OnConnect; cb != nil && env.sess != nil {
			s.invokeOnConnect(cb, env.sess)
		}
	case eventDisconnect:
		s.cleanupSession(env.sess, env.reason)
	}
}

// dispatchMessage parses env.payload as a ClientFrame and routes its
// populated oneof body. The tick goroutine is the sole caller, so
// session-map reads here are race-free.
func (s *Server) dispatchMessage(tc *tickCtx, env event) {
	// Resolve session for UDP events via udpAddr.
	if env.isUDP {
		sess := s.udpSessions[env.udpAddr.String()]
		if sess != nil {
			sess.lastSeenUnixNano.Store(tc.now.UnixNano())
		}
		env.sess = sess
	}

	sess := env.sess
	// If a TCP event is for a cleaned-up session, drop silently.
	if !env.isUDP && sess != nil && sess.ID != 0 {
		if _, alive := s.sessions[sess.ID]; !alive {
			return
		}
	}

	var frame controlpb.ClientFrame
	if err := proto.Unmarshal(env.payload, &frame); err != nil {
		s.log.Debug("malformed ClientFrame",
			"err", err,
			"udp", env.isUDP,
			"player", sessIDOrZero(sess),
		)
		if sess != nil && !env.isUDP {
			s.scheduleDisconnect(sess, ErrMalformedFrame)
		}
		return
	}

	switch body := frame.GetBody().(type) {
	case *controlpb.ClientFrame_Login:
		if env.isUDP || sess == nil || sess.authState != sessionPreLogin {
			return
		}
		s.handleLogin(tc, sess, body.Login)

	case *controlpb.ClientFrame_UdpHandshake:
		if !env.isUDP {
			return
		}
		s.handleUDPHandshake(tc, env.udpAddr, body.UdpHandshake)

	case *controlpb.ClientFrame_Pong:
		if sess == nil {
			return
		}
		s.handlePong(tc, sess, body.Pong)

	case *controlpb.ClientFrame_GamePayload:
		if sess == nil || sess.authState != sessionReady {
			return
		}
		s.dispatchUserMessage(tc, sess, body.GamePayload)

	case *controlpb.ClientFrame_RpcRequest:
		if env.isUDP || sess == nil || sess.authState != sessionReady {
			return
		}
		s.dispatchRPC(tc, sess, body.RpcRequest)

	case nil:
		s.log.Debug("ClientFrame with empty body",
			"udp", env.isUDP,
			"player", sessIDOrZero(sess),
		)

	default:
		s.log.Debug("unknown ClientFrame body type",
			"udp", env.isUDP,
			"player", sessIDOrZero(sess),
		)
	}
}

func sessIDOrZero(s *Session) PlayerID {
	if s == nil {
		return 0
	}
	return s.ID
}

// invokeOnTick calls cb on the tick goroutine with panic-recovery so a
// faulty handler cannot kill the loop.
func (s *Server) invokeOnTick(cb func(TickCtx), tc TickCtx) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("OnTick panic recovered", "panic", r, "tick", tc.Tick())
		}
	}()
	cb(tc)
}

func (s *Server) invokeOnConnect(cb func(*Session), sess *Session) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("OnConnect panic recovered", "panic", r, "player", sess.ID)
		}
	}()
	cb(sess)
}

func (s *Server) invokeOnAuth(cb func(*Session, proto.Message), sess *Session, credentials proto.Message) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("OnAuth panic recovered", "panic", r, "player", sess.ID)
		}
	}()
	cb(sess, credentials)
}

func (s *Server) invokeOnDisconnect(cb func(*Session, error), sess *Session, reason error) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("OnDisconnect panic recovered", "panic", r, "player", sess.ID)
		}
	}()
	cb(sess, reason)
}
