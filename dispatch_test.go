package godotnet

import (
	"testing"

	"google.golang.org/protobuf/proto"

	controlpb "github.com/jordanstites/godotnet/controlpb"
)

func TestHandlerRegistry_RegisterLookup(t *testing.T) {
	var r handlerRegistry
	var got proto.Message
	r.register(&controlpb.Ping{}, func(_ TickCtx, _ *Session, m proto.Message) {
		got = m
	})

	h, ok := r.lookup(&controlpb.Ping{Nonce: 42})
	if !ok {
		t.Fatal("registered type not found")
	}
	want := &controlpb.Ping{Nonce: 99}
	h(nil, nil, want)
	if got == nil {
		t.Fatal("handler was not called")
	}
	if got != want {
		t.Errorf("handler received %v, want %v", got, want)
	}
}

func TestHandlerRegistry_UnknownTypeReturnsFalse(t *testing.T) {
	var r handlerRegistry
	if _, ok := r.lookup(&controlpb.Ping{}); ok {
		t.Error("unknown-type lookup returned ok=true")
	}
}

func TestHandlerRegistry_NilArgsAreNoop(t *testing.T) {
	var r handlerRegistry
	r.register(nil, nil)
	r.register(&controlpb.Ping{}, nil)
	r.register(nil, func(_ TickCtx, _ *Session, _ proto.Message) {})

	if _, ok := r.lookup(&controlpb.Ping{}); ok {
		t.Error("nil-handler registration was recorded")
	}
}

func TestHandlerRegistry_OverwriteByType(t *testing.T) {
	var r handlerRegistry
	r.register(&controlpb.Ping{}, func(_ TickCtx, _ *Session, _ proto.Message) {
		t.Error("first handler called after overwrite")
	})
	var called bool
	r.register(&controlpb.Ping{}, func(_ TickCtx, _ *Session, _ proto.Message) {
		called = true
	})
	h, _ := r.lookup(&controlpb.Ping{})
	h(nil, nil, &controlpb.Ping{})
	if !called {
		t.Error("second handler was not called")
	}
}

func TestServer_HandleClientDelegatesToRegistry(t *testing.T) {
	s := NewServer(Config{})
	var called bool
	s.HandleClient(&controlpb.Ping{}, func(_ TickCtx, _ *Session, _ proto.Message) {
		called = true
	})
	h, ok := s.handlers.lookup(&controlpb.Ping{})
	if !ok {
		t.Fatal("handler not registered via HandleClient")
	}
	h(nil, nil, &controlpb.Ping{})
	if !called {
		t.Error("handler not called")
	}
}
