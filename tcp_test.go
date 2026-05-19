package godotnet

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jordanstites/godotnet/internal/transport"
)

// waitForEvent polls s.events until an event of kind appears or the
// timeout elapses. Drained events of other kinds are appended to the
// returned slice for the caller to inspect.
func waitForEvent(t *testing.T, s *Server, kind eventKind, timeout time.Duration) (event, []event) {
	t.Helper()
	var seen []event
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, e := range s.events.drain() {
			if e.kind == kind {
				return e, seen
			}
			seen = append(seen, e)
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("event of kind %d did not arrive within %v (saw %d others)", kind, timeout, len(seen))
	return event{}, seen
}

func TestTCPAccept_PushesFrameToMailbox(t *testing.T) {
	s := NewServer(Config{MaxFrameLen: 1024})

	l := transport.NewMemoryListener("test-tcp")
	defer l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	accDone := make(chan error, 1)
	go func() { accDone <- s.runTCPAccept(ctx, l) }()

	client, err := l.Dial()
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte("hello world")
	if err := WriteFrame(client, payload); err != nil {
		t.Fatal(err)
	}

	env, _ := waitForEvent(t, s, eventMessage, time.Second)
	if !bytes.Equal(env.payload, payload) {
		t.Errorf("payload: got %q, want %q", env.payload, payload)
	}
	if env.isUDP {
		t.Error("event.isUDP=true on a TCP frame")
	}
	if env.sess == nil {
		t.Error("event.sess is nil on a TCP frame")
	}

	_ = client.Close()
	cancel()
	if err := <-accDone; !errors.Is(err, context.Canceled) {
		t.Errorf("accept returned %v, want context.Canceled", err)
	}
}

func TestTCPAccept_PushesDisconnectOnClientClose(t *testing.T) {
	s := NewServer(Config{MaxFrameLen: 1024})

	l := transport.NewMemoryListener("test-tcp")
	defer l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	accDone := make(chan error, 1)
	go func() { accDone <- s.runTCPAccept(ctx, l) }()

	client, err := l.Dial()
	if err != nil {
		t.Fatal(err)
	}
	_ = client.Close()

	env, _ := waitForEvent(t, s, eventDisconnect, time.Second)
	if env.reason == nil {
		t.Error("disconnect event missing reason")
	}

	cancel()
	<-accDone
}

func TestTCPAccept_MultipleFramesArriveInOrder(t *testing.T) {
	s := NewServer(Config{MaxFrameLen: 1024})

	l := transport.NewMemoryListener("test-tcp")
	defer l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	accDone := make(chan error, 1)
	go func() { accDone <- s.runTCPAccept(ctx, l) }()

	client, err := l.Dial()
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	payloads := [][]byte{[]byte("one"), []byte("two"), []byte("three"), []byte("four")}
	for _, p := range payloads {
		if err := WriteFrame(client, p); err != nil {
			t.Fatal(err)
		}
	}

	var got [][]byte
	deadline := time.Now().Add(2 * time.Second)
	for len(got) < len(payloads) && time.Now().Before(deadline) {
		for _, env := range s.events.drain() {
			if env.kind == eventMessage {
				got = append(got, env.payload)
			}
		}
		time.Sleep(2 * time.Millisecond)
	}

	if len(got) != len(payloads) {
		t.Fatalf("got %d frames, want %d", len(got), len(payloads))
	}
	for i, p := range payloads {
		if !bytes.Equal(got[i], p) {
			t.Errorf("frame %d: got %q, want %q", i, got[i], p)
		}
	}

	cancel()
	<-accDone
}

func TestTCPAccept_OversizedFrameDisconnects(t *testing.T) {
	s := NewServer(Config{MaxFrameLen: 8})

	l := transport.NewMemoryListener("test-tcp")
	defer l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	accDone := make(chan error, 1)
	go func() { accDone <- s.runTCPAccept(ctx, l) }()

	client, err := l.Dial()
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// The server will close the conn as soon as it reads the oversized
	// length header. With net.Pipe semantics, our payload write may
	// block / fail — that's expected, so do it asynchronously.
	go func() { _ = WriteFrame(client, make([]byte, 64)) }()

	env, _ := waitForEvent(t, s, eventDisconnect, time.Second)
	if !errors.Is(env.reason, ErrFrameTooLarge) {
		t.Errorf("got %v, want ErrFrameTooLarge", env.reason)
	}

	cancel()
	<-accDone
}

func TestTCPAccept_ShutdownOnContextCancel(t *testing.T) {
	s := NewServer(Config{MaxFrameLen: 1024})

	l := transport.NewMemoryListener("test-tcp")
	defer l.Close()

	ctx, cancel := context.WithCancel(context.Background())

	accDone := make(chan error, 1)
	go func() { accDone <- s.runTCPAccept(ctx, l) }()

	client, err := l.Dial()
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// Give the conn a moment to be accepted before cancelling.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-accDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("got %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("accept loop did not return after cancel")
	}
}
