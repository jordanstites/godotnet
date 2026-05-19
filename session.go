package godotnet

import (
	"net"
	"sync/atomic"
	"time"
)

// PlayerID identifies a connected player. It is assigned by the
// Authenticate callback and is unique for the lifetime of one Server.
type PlayerID uint32

// sessionAuthState tracks where a session sits in the handshake.
type sessionAuthState uint8

const (
	// sessionPreLogin means the TCP+TLS handshake has completed but no
	// Login frame has been processed yet. Pre-login sessions are not
	// in Server.sessions and have ID == 0.
	sessionPreLogin sessionAuthState = iota

	// sessionAwaitingUDP means Authenticate has succeeded and a token
	// has been issued; the server is waiting for the matching
	// UdpHandshake datagram. The session is in Server.sessions.
	sessionAwaitingUDP

	// sessionReady means both legs are paired and OnConnect has fired.
	// Game messages flow freely.
	sessionReady
)

// Session represents a connected player. Exported fields are stable
// across the session's lifetime. UserData is the game-side scratchpad —
// the library never reads or writes it.
//
// All Session reads and writes must occur on the tick goroutine. The
// library never mutates Session state from I/O goroutines (atomic
// fields below are the documented exceptions).
type Session struct {
	ID          PlayerID
	Username    string
	ConnectedAt time.Time
	UserData    any

	// authState is read and written only on the tick goroutine.
	authState sessionAuthState

	// sessionToken is the secret issued by login that the client must
	// echo in UdpHandshake.
	sessionToken string

	// tcp is the underlying TCP connection (TLS-wrapped if configured).
	tcp net.Conn

	// udpAddr is set once the UDP handshake completes. The UDP writer
	// path on the tick goroutine reads it.
	udpAddr net.Addr

	// sendTCP is the per-session outbound TCP queue. Bounded; overflow
	// disconnects the session.
	sendTCP chan []byte

	// lastSeenUnixNano is updated atomically by the UDP reader so the
	// tick goroutine can detect zombies without locks.
	lastSeenUnixNano atomic.Int64

	// lastPingNonce is the last Ping nonce sent on UDP; only the tick
	// goroutine writes it.
	lastPingNonce uint64

	// disconnectOnce coalesces multiple disconnect signals (TCP error,
	// TickCtx.Disconnect, etc.) into one eventDisconnect event.
	disconnectOnce atomicOnce
}

// atomicOnce is a sync.Once equivalent that reports whether the action
// has fired. The library uses it to coalesce disconnect signals from
// different goroutines.
type atomicOnce struct {
	fired atomic.Bool
}

// Do runs fn the first time it is called and reports whether fn ran.
func (o *atomicOnce) Do(fn func()) bool {
	if o.fired.CompareAndSwap(false, true) {
		fn()
		return true
	}
	return false
}
