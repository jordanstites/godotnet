package godotnet

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// runTCPAccept owns the TCP listener. It accepts connections, spawns a
// per-connection serve goroutine for each, and waits for them all to
// exit before returning. Returns ctx.Err() on graceful shutdown.
func (s *Server) runTCPAccept(ctx context.Context, l net.Listener) error {
	// Close the listener when ctx is cancelled so a blocked Accept
	// returns immediately.
	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()

	var wg sync.WaitGroup
	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				wg.Wait()
				return ctx.Err()
			}
			// Transient accept error — log and continue. Real
			// rate-limiting / DoS protection is a v0.2 concern.
			s.log.Warn("tcp accept error", "err", err)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.serveTCPConn(ctx, conn)
		}()
	}
}

// serveTCPConn runs one accepted TCP connection. It allocates the
// per-session outbound queue, spawns the writer goroutine, and runs the
// reader in the current goroutine. Returns when the connection is
// fully torn down.
func (s *Server) serveTCPConn(ctx context.Context, conn net.Conn) {
	sess := &Session{
		tcp:         conn,
		sendTCP:     make(chan []byte, s.cfg.SendQueueDepth),
		ConnectedAt: time.Now(),
	}

	s.log.Debug("tcp conn accepted", "remote", conn.RemoteAddr())

	// Close the conn when ctx is cancelled so any blocked Read or Write
	// returns. The reader and writer both treat net.ErrClosed as a
	// clean exit.
	stopWatcher := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stopWatcher:
		}
	}()
	defer close(stopWatcher)

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		s.runTCPWriter(ctx, sess)
	}()

	s.runTCPReader(sess)

	// Reader has exited. Close the connection (idempotent). The
	// outbound channel is closed by cleanupSession on the tick
	// goroutine — the writer exits via either that close or ctx cancel.
	_ = conn.Close()
	<-writerDone
}

// runTCPReader reads framed bytes from the connection and pushes each
// frame into the event queue as an eventMessage. On read error or EOF,
// it pushes an eventDisconnect event and returns.
func (s *Server) runTCPReader(sess *Session) {
	for {
		payload, err := ReadFrame(sess.tcp, s.cfg.MaxFrameLen)
		if err != nil {
			if !isCleanCloseErr(err) {
				s.log.Debug("tcp read error",
					"err", err,
					"remote", sess.tcp.RemoteAddr(),
				)
			}
			sess.disconnectOnce.Do(func() {
				s.events.push(event{
					kind:   eventDisconnect,
					sess:   sess,
					reason: err,
				})
			})
			return
		}
		if !s.events.push(event{
			kind:    eventMessage,
			sess:    sess,
			payload: payload,
		}) {
			s.log.Warn("event queue full; disconnecting",
				"remote", sess.tcp.RemoteAddr(),
			)
			sess.disconnectOnce.Do(func() {
				s.events.push(event{
					kind:   eventDisconnect,
					sess:   sess,
					reason: ErrOutboundQueueFull,
				})
			})
			return
		}
	}
}

// runTCPWriter drains sess.sendTCP and writes each payload as a length-
// prefixed frame. Exits when sendTCP is closed, a write error occurs,
// or ctx is cancelled. The outbound channel is closed by
// cleanupSession on the tick goroutine — this writer never closes it.
func (s *Server) runTCPWriter(ctx context.Context, sess *Session) {
	for {
		select {
		case <-ctx.Done():
			return
		case payload, ok := <-sess.sendTCP:
			if !ok {
				return
			}
			if err := WriteFrame(sess.tcp, payload); err != nil {
				if !isCleanCloseErr(err) {
					s.log.Debug("tcp write error",
						"err", err,
						"remote", sess.tcp.RemoteAddr(),
					)
				}
				return
			}
		}
	}
}

// isCleanCloseErr reports whether err represents a graceful peer close
// or a local Close() — neither warrants a log line at warn level.
func isCleanCloseErr(err error) bool {
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed)
}
