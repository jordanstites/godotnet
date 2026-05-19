package transport

import (
	"net"
	"sync"
)

// MemoryListener is an in-memory net.Listener for tests. Call Dial to
// produce a paired client net.Conn; the server side is returned from the
// next Accept call. Accepted connections are net.Pipe pairs and behave
// like real sockets with respect to deadlines, EOF on close, and ordered
// byte delivery.
type MemoryListener struct {
	addr     memoryAddr
	incoming chan net.Conn
	closed   chan struct{}
	once     sync.Once
}

// NewMemoryListener returns a listener whose Addr().String() is name.
func NewMemoryListener(name string) *MemoryListener {
	return &MemoryListener{
		addr:     memoryAddr(name),
		incoming: make(chan net.Conn, 16),
		closed:   make(chan struct{}),
	}
}

// Accept blocks until Dial is called or the listener is closed.
func (l *MemoryListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.incoming:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

// Close stops accepting and unblocks any pending Accept calls with
// net.ErrClosed. Calling Close more than once is safe.
func (l *MemoryListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

// Addr returns the listener's logical address.
func (l *MemoryListener) Addr() net.Addr { return l.addr }

// Dial creates a net.Pipe pair, hands the server side to the next
// Accept caller, and returns the client side. Returns net.ErrClosed if
// the listener has been closed.
func (l *MemoryListener) Dial() (net.Conn, error) {
	select {
	case <-l.closed:
		return nil, net.ErrClosed
	default:
	}
	client, server := net.Pipe()
	select {
	case l.incoming <- server:
		return client, nil
	case <-l.closed:
		_ = client.Close()
		_ = server.Close()
		return nil, net.ErrClosed
	}
}

type memoryAddr string

func (m memoryAddr) Network() string { return "memory" }
func (m memoryAddr) String() string  { return string(m) }
