package godotnet

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/jordanstites/godotnet/internal/transport"
)

func TestTLSAccept_FrameRoundTripOverRealLoopback(t *testing.T) {
	cert := newSelfSignedCert(t)
	serverTLS := &tls.Config{Certificates: []tls.Certificate{cert}}

	l, err := transport.ListenTCP("127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	s := NewServer(Config{MaxFrameLen: 4096})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	accDone := make(chan error, 1)
	go func() { accDone <- s.runTCPAccept(ctx, l) }()

	clientTLS := &tls.Config{InsecureSkipVerify: true}
	dialer := &tls.Dialer{Config: clientTLS, NetDialer: nil}
	conn, err := dialer.DialContext(context.Background(), "tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	payload := []byte("hello over tls")
	if err := WriteFrame(conn, payload); err != nil {
		t.Fatal(err)
	}

	env, _ := waitForEvent(t, s, eventMessage, 2*time.Second)
	if !bytes.Equal(env.payload, payload) {
		t.Errorf("payload: got %q, want %q", env.payload, payload)
	}

	cancel()
	if err := <-accDone; !errors.Is(err, context.Canceled) {
		t.Errorf("accept returned %v, want context.Canceled", err)
	}
}

// newSelfSignedCert produces an ECDSA P-256 self-signed cert good for
// localhost. Suitable only for tests.
func newSelfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "godotnet-test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
