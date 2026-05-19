// Package transport hides the choice between real net.* sockets and
// in-memory test fakes from the rest of godotnet.
//
// The package deliberately reuses stdlib interfaces:
//
//   - TCP listeners are net.Listener
//   - TCP connections are net.Conn
//   - UDP sockets are net.PacketConn
//
// Production code calls the Listen* constructors in this package; tests
// substitute in-memory implementations from memory.go.
package transport
