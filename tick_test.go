package godotnet

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTickLoop_FiresAtConfiguredRate(t *testing.T) {
	var ticks int64
	s := NewServer(Config{TickRate: 10 * time.Millisecond})
	s.OnTick(func(_ TickCtx) { atomic.AddInt64(&ticks, 1) })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	time.Sleep(120 * time.Millisecond)
	cancel()
	<-done

	got := atomic.LoadInt64(&ticks)
	// 10ms ticker over ~120ms → expect ~12; allow a generous window
	// because timer fidelity varies (especially on Windows).
	if got < 4 || got > 40 {
		t.Errorf("got %d ticks, expected roughly 12 (range 4-40)", got)
	}
}

func TestTickLoop_TickNumbersMonotonic(t *testing.T) {
	var mu sync.Mutex
	var seen []uint64

	s := NewServer(Config{TickRate: 10 * time.Millisecond})
	s.OnTick(func(tc TickCtx) {
		mu.Lock()
		seen = append(seen, tc.Tick())
		mu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	time.Sleep(80 * time.Millisecond)
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(seen) < 2 {
		t.Fatalf("not enough ticks: %v", seen)
	}
	if seen[0] != 1 {
		t.Errorf("first tick was %d, want 1", seen[0])
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] != seen[i-1]+1 {
			t.Errorf("ticks not monotonic at index %d: %v", i, seen)
			break
		}
	}
}

func TestTickLoop_RecoversFromOnTickPanic(t *testing.T) {
	s := NewServer(Config{TickRate: 10 * time.Millisecond})
	var calls int64
	s.OnTick(func(_ TickCtx) {
		atomic.AddInt64(&calls, 1)
		panic("boom")
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	time.Sleep(60 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}

	if atomic.LoadInt64(&calls) < 2 {
		t.Error("tick loop appears to have died after first panic")
	}
}

func TestTickLoop_InvokesOnConnect(t *testing.T) {
	var called int64
	s := NewServer(Config{
		TickRate: 10 * time.Millisecond,
		OnConnect: func(_ *Session) {
			atomic.AddInt64(&called, 1)
		},
	})

	// Push before Run; the first tick will drain it.
	s.events.push(event{kind: eventConnect, sess: &Session{ID: 42}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	time.Sleep(40 * time.Millisecond)
	cancel()
	<-done

	if got := atomic.LoadInt64(&called); got != 1 {
		t.Errorf("OnConnect called %d times, want 1", got)
	}
}

func TestTickLoop_InvokesOnDisconnect(t *testing.T) {
	var gotReason error
	var mu sync.Mutex
	s := NewServer(Config{
		TickRate: 10 * time.Millisecond,
		OnDisconnect: func(_ *Session, reason error) {
			mu.Lock()
			gotReason = reason
			mu.Unlock()
		},
	})

	want := errors.New("test-reason")
	s.events.push(event{kind: eventDisconnect, sess: &Session{ID: 7}, reason: want})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	time.Sleep(40 * time.Millisecond)
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if gotReason != want {
		t.Errorf("got reason %v, want %v", gotReason, want)
	}
}

func TestQueue_PushDrainFIFO(t *testing.T) {
	m := newQueue(8)
	for i := 1; i <= 3; i++ {
		if !m.push(event{kind: eventConnect, sess: &Session{ID: PlayerID(i)}}) {
			t.Fatalf("push %d failed", i)
		}
	}

	got := m.drain()
	if len(got) != 3 {
		t.Fatalf("drained %d, want 3", len(got))
	}
	for i, env := range got {
		if env.sess.ID != PlayerID(i+1) {
			t.Errorf("event %d: sess.ID = %d, want %d", i, env.sess.ID, i+1)
		}
	}
}

func TestQueue_PushReturnsFalseWhenFull(t *testing.T) {
	m := newQueue(2)
	if !m.push(event{}) {
		t.Fatal("push 1")
	}
	if !m.push(event{}) {
		t.Fatal("push 2")
	}
	if m.push(event{}) {
		t.Fatal("push 3 should have failed")
	}
}

func TestQueue_EmptyDrainReturnsNil(t *testing.T) {
	m := newQueue(8)
	if got := m.drain(); got != nil {
		t.Errorf("empty drain returned %v", got)
	}
}

func TestQueue_ZeroDepthDefaults(t *testing.T) {
	m := newQueue(0)
	if cap(m.ch) != DefaultEventQueueDepth {
		t.Errorf("got cap %d, want %d", cap(m.ch), DefaultEventQueueDepth)
	}
}
