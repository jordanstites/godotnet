package godotnet

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// runUDPReader reads datagrams off conn and pushes each as an
// eventMessage event into the event queue. The tick goroutine resolves
// the sender by address — this goroutine never touches the session
// maps directly.
func (s *Server) runUDPReader(ctx context.Context, conn net.PacketConn) error {
	// Close conn on ctx cancel so the blocking ReadFrom returns.
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	// Read one extra byte so we can detect oversized datagrams.
	buf := make([]byte, s.cfg.MaxUDPPayload+1)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			if isCleanCloseErr(err) || errors.Is(err, net.ErrClosed) {
				return ctx.Err()
			}
			return fmt.Errorf("godotnet: udp read: %w", err)
		}
		if n > s.cfg.MaxUDPPayload {
			s.log.Debug("oversized udp datagram dropped",
				"size", n,
				"max", s.cfg.MaxUDPPayload,
				"from", addr,
			)
			continue
		}

		// Copy out of the shared read buffer.
		payload := make([]byte, n)
		copy(payload, buf[:n])

		// Push without sess — the tick goroutine looks up by udpAddr.
		if !s.events.push(event{
			kind:    eventMessage,
			payload: payload,
			isUDP:   true,
			udpAddr: addr,
		}) {
			s.log.Warn("event queue full; dropping udp datagram", "from", addr)
		}
	}
}

// udpSendRaw writes a single datagram to addr using the shared udpConn.
// Safe to call from the tick goroutine. UDP write failure is logged but
// non-fatal — UDP is best-effort by design.
func (s *Server) udpSendRaw(addr net.Addr, payload []byte) {
	if s.udpConn == nil || addr == nil {
		return
	}
	if len(payload) > s.cfg.MaxUDPPayload {
		s.log.Warn("udp message too large; dropping",
			"size", len(payload),
			"max", s.cfg.MaxUDPPayload,
		)
		return
	}
	if _, err := s.udpConn.WriteTo(payload, addr); err != nil {
		if !isCleanCloseErr(err) {
			s.log.Debug("udp write error", "err", err, "to", addr)
		}
	}
}
