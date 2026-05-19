// Package godotnet is a networking library for Godot 4 multiplayer games.
// It owns transport (TLS-TCP and UDP), framing, session lifecycle, message
// dispatch, and the tick loop. It deliberately stays out of game logic.
package godotnet

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/jordanstites/godotnet/internal/transport"
)

// DefaultTickRate is used when Config.TickRate is zero (or negative).
// 50ms = 20Hz.
const DefaultTickRate = 50 * time.Millisecond

// DefaultMaxFrameLen is the default cap on inbound TCP frame size.
// 1 MiB is generous for game messages and prevents trivial OOM attacks.
const DefaultMaxFrameLen = 1 << 20

// DefaultSendQueueDepth is the bounded channel depth for per-player
// outbound TCP and UDP queues. Overflow disconnects the player.
const DefaultSendQueueDepth = 256

// DefaultEventQueueDepth is the bounded event-queue depth. Overflow
// disconnects the player whose message triggered the overflow.
const DefaultEventQueueDepth = 4096

// DefaultMaxUDPPayload is the per-datagram payload cap (excluding any
// outer framing). 1200 bytes is the conservative IPv4-MTU-minus-headers
// figure used to avoid fragmentation across most paths.
const DefaultMaxUDPPayload = 1200

// Config configures a Server. Mutating Config after passing it to
// NewServer is not supported.
type Config struct {
	// TCPAddr is the listen address for TCP (e.g. ":7777").
	TCPAddr string

	// UDPAddr is the listen address for UDP (e.g. ":7778").
	UDPAddr string

	// UDPAdvertiseAddr is the host:port string sent to clients in
	// LoginResponse.udp_endpoint. If empty, UDPAddr is used. Set this
	// when UDPAddr binds to 0.0.0.0 but clients need a routable address
	// (typical when the server sits behind NAT or on a public IP).
	UDPAdvertiseAddr string

	// TickRate is the period between OnTick invocations. Zero means
	// DefaultTickRate.
	TickRate time.Duration

	// TLSConfig optionally wraps the TCP listener with TLS. If you put
	// the server behind a TLS-terminating reverse proxy (the default
	// expectation), leave this nil.
	TLSConfig *tls.Config

	// Authenticate verifies the credentials in the client's Login
	// message and returns the PlayerID for the session. Returning an
	// error rejects the login. Called on the tick goroutine.
	Authenticate func(ctx context.Context, credentials proto.Message) (PlayerID, error)

	// LoginPrototype is the message Login.credentials is unmarshaled
	// into before being passed to Authenticate. The library proto.Clones
	// this value per login attempt.
	LoginPrototype proto.Message

	// ClientMessagePrototype is your top-level inbound game-plane
	// message. It must contain a single oneof field holding the union
	// of all expected client-side message types; handlers registered
	// via HandleClient match against the populated oneof body, not
	// against this top-level type itself.
	//
	// If nil, all post-login inbound frames are dropped with a log line.
	ClientMessagePrototype proto.Message

	// OnConnect fires on the tick goroutine after a session is fully
	// established (TCP login + UDP pairing both complete). Optional.
	OnConnect func(s *Session)

	// OnDisconnect fires on the tick goroutine when a session is being
	// torn down. The reason is the underlying error or sentinel.
	// Optional.
	OnDisconnect func(s *Session, reason error)

	// Logger receives structured log output. Nil means logs are dropped.
	Logger Logger

	// MaxFrameLen overrides DefaultMaxFrameLen if non-zero.
	MaxFrameLen uint32

	// SendQueueDepth overrides DefaultSendQueueDepth if non-zero.
	SendQueueDepth int

	// EventQueueDepth overrides DefaultEventQueueDepth if non-zero.
	EventQueueDepth int

	// MaxUDPPayload overrides DefaultMaxUDPPayload if non-zero.
	MaxUDPPayload int

	// PingInterval overrides DefaultPingInterval if non-zero. Set to a
	// large value (e.g. 1 hour) to effectively disable keepalive pings.
	PingInterval time.Duration

	// PingTimeout overrides DefaultPingTimeout if non-zero. Sessions
	// silent on UDP for longer than this are disconnected.
	PingTimeout time.Duration
}

// ClientHandler is invoked on the tick goroutine when an inbound message
// of the registered protobuf type arrives, before OnTick fires for that
// tick.
type ClientHandler func(ctx TickCtx, sess *Session, msg proto.Message)

// Server is the main library entry point. Construct with NewServer,
// register handlers with HandleClient and OnTick, then call Run.
type Server struct {
	cfg Config
	log Logger

	mu     sync.Mutex
	onTick func(ctx TickCtx)

	// handlers is the typed-handler registry. Safe for concurrent
	// register/lookup, though in practice it is populated before Run.
	handlers handlerRegistry

	// events is the inbound event queue, allocated by NewServer and
	// read by the tick goroutine. I/O goroutines push events into it.
	events *queue

	// tickCount, lastPingBatchAt, and the maps below are touched only
	// by the tick goroutine.
	tickCount       uint64
	lastPingBatchAt time.Time
	sessions        map[PlayerID]*Session
	udpSessions     map[string]*Session

	// udpConn is the shared UDP socket. Set in Run when UDPAddr is
	// non-empty. Concurrent WriteTo calls are safe per Go docs.
	udpConn net.PacketConn

	running atomic.Bool
}

// NewServer returns a Server configured with cfg.
func NewServer(cfg Config) *Server {
	if cfg.TickRate <= 0 {
		cfg.TickRate = DefaultTickRate
	}
	if cfg.MaxFrameLen == 0 {
		cfg.MaxFrameLen = DefaultMaxFrameLen
	}
	if cfg.SendQueueDepth <= 0 {
		cfg.SendQueueDepth = DefaultSendQueueDepth
	}
	if cfg.EventQueueDepth <= 0 {
		cfg.EventQueueDepth = DefaultEventQueueDepth
	}
	if cfg.MaxUDPPayload <= 0 {
		cfg.MaxUDPPayload = DefaultMaxUDPPayload
	}
	return &Server{
		cfg:         cfg,
		log:         loggerOrNop(cfg.Logger),
		events:      newQueue(cfg.EventQueueDepth),
		sessions:    make(map[PlayerID]*Session),
		udpSessions: make(map[string]*Session),
	}
}

// OnTick sets the per-tick simulation callback. Must be called before
// Run. Passing nil clears any previously-set callback.
func (s *Server) OnTick(cb func(ctx TickCtx)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onTick = cb
}

// HandleClient registers a handler for inbound messages whose concrete
// type matches prototype's. Must be called before Run.
//
// The matching is by protobuf full-name; prototype itself is never
// stored, only its descriptor is consulted. Re-registering the same
// type overwrites the previous handler.
func (s *Server) HandleClient(prototype proto.Message, handler ClientHandler) {
	s.handlers.register(prototype, handler)
}

// Run blocks until ctx is cancelled. Returns ctx.Err() on clean shutdown
// or the first non-cancel error encountered by an internal goroutine.
// Run may only be called once per Server.
//
// Goroutines launched by Run: the tick loop (1), the TCP accept loop
// (1, when Config.TCPAddr is set), and per-connection reader/writer
// pairs (N). Future commits add the UDP reader and per-player UDP
// writers. The first goroutine to error cancels the shared run-context,
// causing the others to unwind.
func (s *Server) Run(ctx context.Context) error {
	if !s.running.CompareAndSwap(false, true) {
		return ErrServerAlreadyRunning
	}
	defer s.running.Store(false)

	s.tickCount = 0

	s.log.Info("godotnet server starting",
		"tcp", s.cfg.TCPAddr,
		"udp", s.cfg.UDPAddr,
		"tick", s.cfg.TickRate,
		"tls", s.cfg.TLSConfig != nil,
	)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		errMu    sync.Mutex
		firstErr error
	)
	recordErr := func(err error) {
		if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
		cancel()
	}

	var tcpListener net.Listener
	if s.cfg.TCPAddr != "" {
		l, err := transport.ListenTCP(s.cfg.TCPAddr, s.cfg.TLSConfig)
		if err != nil {
			return fmt.Errorf("godotnet: tcp listen %q: %w", s.cfg.TCPAddr, err)
		}
		tcpListener = l
	}

	if s.cfg.UDPAddr != "" {
		c, err := transport.ListenUDP(s.cfg.UDPAddr)
		if err != nil {
			if tcpListener != nil {
				_ = tcpListener.Close()
			}
			return fmt.Errorf("godotnet: udp listen %q: %w", s.cfg.UDPAddr, err)
		}
		s.udpConn = c
	}
	// Defer-clear the udpConn pointer so a second Run doesn't reuse a
	// closed socket.
	defer func() { s.udpConn = nil }()

	var wg sync.WaitGroup

	if tcpListener != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recordErr(s.runTCPAccept(runCtx, tcpListener))
		}()
	}

	if s.udpConn != nil {
		udpConn := s.udpConn
		wg.Add(1)
		go func() {
			defer wg.Done()
			recordErr(s.runUDPReader(runCtx, udpConn))
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		recordErr(s.runTickLoop(runCtx))
	}()

	wg.Wait()

	errMu.Lock()
	err := firstErr
	errMu.Unlock()

	if err == nil {
		err = ctx.Err()
	}
	s.log.Info("godotnet server stopping", "reason", err)
	return err
}

// pushTCP enqueues payload on sess's outbound TCP queue. On overflow,
// the session is scheduled for disconnect.
//
// pushTCP must be called from the tick goroutine.
func (s *Server) pushTCP(sess *Session, payload []byte) {
	if sess == nil || sess.sendTCP == nil {
		return
	}
	// Best-effort non-blocking send. The writer goroutine drains it.
	select {
	case sess.sendTCP <- payload:
	default:
		s.log.Warn("sendTCP full; disconnecting",
			"player", sess.ID,
		)
		s.scheduleDisconnect(sess, ErrOutboundQueueFull)
	}
}

// scheduleDisconnect pushes an eventDisconnect event for sess. The
// disconnectOnce on sess ensures only the first scheduling wins.
//
// Safe to call from any goroutine.
func (s *Server) scheduleDisconnect(sess *Session, reason error) {
	if sess == nil {
		return
	}
	sess.disconnectOnce.Do(func() {
		if !s.events.push(event{
			kind:   eventDisconnect,
			sess:   sess,
			reason: reason,
		}) {
			s.log.Warn("event queue full; force-closing tcp", "player", sess.ID)
			if sess.tcp != nil {
				_ = sess.tcp.Close()
			}
		}
	})
}

// cleanupSession runs on the tick goroutine when an eventDisconnect
// event is processed. It removes the session from lookup tables,
// closes the underlying conn, drains the outbound channel, and fires
// the OnDisconnect callback.
func (s *Server) cleanupSession(sess *Session, reason error) {
	if sess == nil {
		return
	}
	if sess.ID != 0 {
		delete(s.sessions, sess.ID)
	}
	if sess.udpAddr != nil {
		delete(s.udpSessions, sess.udpAddr.String())
	}
	if sess.tcp != nil {
		_ = sess.tcp.Close()
	}
	if sess.sendTCP != nil {
		// Closing here unblocks the writer goroutine, which drains and
		// exits. We do not nil out sendTCP — pushTCP guards against
		// the closed-channel case via the disconnectOnce / map lookup
		// in TickCtx, both of which run before pushTCP can be called.
		close(sess.sendTCP)
		sess.sendTCP = nil
	}
	if cb := s.cfg.OnDisconnect; cb != nil {
		s.invokeOnDisconnect(cb, sess, reason)
	}
}
