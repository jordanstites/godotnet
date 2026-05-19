package transport

import (
	"crypto/tls"
	"net"
)

// ListenTCP returns a TCP listener bound at addr. If tlsCfg is non-nil,
// the listener is wrapped with tls.NewListener; each Accept blocks until
// the TLS handshake completes (or fails).
//
// addr follows net.Listen syntax (e.g. ":7777", "0.0.0.0:7777").
func ListenTCP(addr string, tlsCfg *tls.Config) (net.Listener, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	if tlsCfg != nil {
		return tls.NewListener(l, tlsCfg), nil
	}
	return l, nil
}

// ListenUDP returns a UDP packet socket bound at addr.
func ListenUDP(addr string) (net.PacketConn, error) {
	return net.ListenPacket("udp", addr)
}
