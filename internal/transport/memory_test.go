package transport

import (
	"bytes"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestMemoryListener_DialAcceptRoundTrip(t *testing.T) {
	l := NewMemoryListener("test")
	defer l.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	var server net.Conn
	go func() {
		defer wg.Done()
		c, err := l.Accept()
		if err != nil {
			t.Errorf("Accept: %v", err)
			return
		}
		server = c
	}()

	client, err := l.Dial()
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	wg.Wait()
	if server == nil {
		t.Fatal("server side never returned")
	}
	defer client.Close()
	defer server.Close()

	go func() {
		_, _ = client.Write([]byte("hello"))
	}()

	buf := make([]byte, 5)
	if _, err := io.ReadFull(server, buf); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if !bytes.Equal(buf, []byte("hello")) {
		t.Errorf("got %q, want %q", buf, "hello")
	}
}

func TestMemoryListener_OrderedDelivery(t *testing.T) {
	l := NewMemoryListener("test")
	defer l.Close()

	type result struct {
		got []byte
		err error
	}
	res := make(chan result, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			res <- result{err: err}
			return
		}
		got := make([]byte, 10)
		_, err = io.ReadFull(c, got)
		res <- result{got: got, err: err}
	}()

	client, err := l.Dial()
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	for i := byte(0); i < 10; i++ {
		if _, err := client.Write([]byte{i}); err != nil {
			t.Fatal(err)
		}
	}

	r := <-res
	if r.err != nil {
		t.Fatal(r.err)
	}
	for i, b := range r.got {
		if b != byte(i) {
			t.Errorf("byte %d: got %d, want %d", i, b, i)
		}
	}
}

func TestMemoryListener_EOFOnPeerClose(t *testing.T) {
	l := NewMemoryListener("test")
	defer l.Close()

	done := make(chan error, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			done <- err
			return
		}
		buf := make([]byte, 16)
		_, err = c.Read(buf)
		done <- err
	}()

	client, err := l.Dial()
	if err != nil {
		t.Fatal(err)
	}
	_ = client.Close()

	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Errorf("got %v, want EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server read did not return after client close")
	}
}

func TestMemoryListener_AcceptUnblocksOnClose(t *testing.T) {
	l := NewMemoryListener("test")
	done := make(chan error, 1)
	go func() {
		_, err := l.Accept()
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	_ = l.Close()
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Errorf("got %v, want ErrClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Accept did not unblock after Close")
	}
}

func TestMemoryListener_DialAfterCloseFails(t *testing.T) {
	l := NewMemoryListener("test")
	_ = l.Close()
	_, err := l.Dial()
	if !errors.Is(err, net.ErrClosed) {
		t.Errorf("got %v, want ErrClosed", err)
	}
}

func TestMemoryListener_DoubleCloseSafe(t *testing.T) {
	l := NewMemoryListener("test")
	_ = l.Close()
	_ = l.Close()
}

func TestMemoryListener_ReadDeadline(t *testing.T) {
	l := NewMemoryListener("test")
	defer l.Close()

	srv := make(chan net.Conn, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			t.Errorf("Accept: %v", err)
			return
		}
		srv <- c
	}()
	client, err := l.Dial()
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server := <-srv
	defer server.Close()

	if err := server.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	_, err = server.Read(buf)
	if err == nil {
		t.Fatal("expected deadline error")
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Errorf("got %v, want timeout error", err)
	}
}
