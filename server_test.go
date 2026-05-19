package godotnet

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestServer_RunReturnsOnContextCancel(t *testing.T) {
	s := NewServer(Config{})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestServer_DoubleRunRejected(t *testing.T) {
	s := NewServer(Config{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	time.Sleep(20 * time.Millisecond)

	if err := s.Run(ctx); !errors.Is(err, ErrServerAlreadyRunning) {
		t.Errorf("second Run: got %v, want ErrServerAlreadyRunning", err)
	}

	cancel()
	<-done
}

func TestServer_NoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	s := NewServer(Config{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	// Give the scheduler a moment to retire goroutines.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before+1 {
		t.Errorf("goroutine leak: before=%d, after=%d", before, got)
	}
}

func TestNewServer_DefaultsTickRate(t *testing.T) {
	s := NewServer(Config{})
	if s.cfg.TickRate != DefaultTickRate {
		t.Errorf("TickRate: got %v, want %v", s.cfg.TickRate, DefaultTickRate)
	}
}

func TestNewServer_PreservesExplicitTickRate(t *testing.T) {
	custom := 100 * time.Millisecond
	s := NewServer(Config{TickRate: custom})
	if s.cfg.TickRate != custom {
		t.Errorf("TickRate: got %v, want %v", s.cfg.TickRate, custom)
	}
}

func TestNewServer_DefaultsAllFields(t *testing.T) {
	s := NewServer(Config{})
	if s.cfg.MaxFrameLen != DefaultMaxFrameLen {
		t.Errorf("MaxFrameLen: got %d, want %d", s.cfg.MaxFrameLen, DefaultMaxFrameLen)
	}
	if s.cfg.SendQueueDepth != DefaultSendQueueDepth {
		t.Errorf("SendQueueDepth: got %d, want %d", s.cfg.SendQueueDepth, DefaultSendQueueDepth)
	}
	if s.cfg.EventQueueDepth != DefaultEventQueueDepth {
		t.Errorf("EventQueueDepth: got %d, want %d", s.cfg.EventQueueDepth, DefaultEventQueueDepth)
	}
	if s.cfg.MaxUDPPayload != DefaultMaxUDPPayload {
		t.Errorf("MaxUDPPayload: got %d, want %d", s.cfg.MaxUDPPayload, DefaultMaxUDPPayload)
	}
}

func TestServer_OnTickAcceptsNilAndNonNil(t *testing.T) {
	s := NewServer(Config{})
	s.OnTick(func(_ TickCtx) {})
	s.OnTick(nil)
}
