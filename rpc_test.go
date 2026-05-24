package godotnet

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	controlpb "github.com/jordanstites/godotnet/internal/proto"
)

// rpcTestServer builds a Server ready to dispatch RPCs and a Session
// already in sessionReady state with a buffered TCP send queue. The
// RPC request prototype is reused from the control plane (ClientFrame)
// because it has a oneof we can populate with the available control
// messages.
func rpcTestServer(t *testing.T) (*Server, *Session, *tickCtx) {
	t.Helper()
	s := NewServer(Config{
		RPCRequestPrototype: &controlpb.ClientFrame{},
	})
	sess := &Session{
		ID:        1,
		sendTCP:   make(chan []byte, 16),
		authState: sessionReady,
	}
	s.sessions[1] = sess
	tc := &tickCtx{server: s, tick: 1, now: time.Now()}
	return s, sess, tc
}

// readRPCResponse drains one frame off sess.sendTCP and returns its
// RpcResponse body, failing if anything else shows up.
func readRPCResponse(t *testing.T, sess *Session) *controlpb.RpcResponse {
	t.Helper()
	select {
	case data := <-sess.sendTCP:
		var frame controlpb.ServerFrame
		if err := proto.Unmarshal(data, &frame); err != nil {
			t.Fatalf("unmarshal ServerFrame: %v", err)
		}
		resp := frame.GetRpcResponse()
		if resp == nil {
			t.Fatalf("ServerFrame body is not RpcResponse: %T", frame.GetBody())
		}
		return resp
	case <-time.After(time.Second):
		t.Fatal("no RPC response frame queued")
		return nil
	}
}

// makeRPCPayload marshals inner into the user's RPCRequestPrototype
// (ClientFrame in these tests) with the matching oneof populated. The
// returned bytes go into RpcRequest.payload.
func makeRPCPayload(t *testing.T, inner proto.Message) []byte {
	t.Helper()
	var frame controlpb.ClientFrame
	switch m := inner.(type) {
	case *controlpb.Pong:
		frame.Body = &controlpb.ClientFrame_Pong{Pong: m}
	case *controlpb.Login:
		frame.Body = &controlpb.ClientFrame_Login{Login: m}
	case *controlpb.UdpHandshake:
		frame.Body = &controlpb.ClientFrame_UdpHandshake{UdpHandshake: m}
	default:
		t.Fatalf("unsupported test request type: %T", inner)
	}
	b, err := proto.Marshal(&frame)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return b
}

func TestDispatchRPC_SuccessReturnsHandlerResponse(t *testing.T) {
	s, sess, tc := rpcTestServer(t)

	var gotReq *controlpb.Pong
	s.HandleRPC(&controlpb.Pong{}, func(_ TickCtx, sessIn *Session, msg proto.Message) (proto.Message, error) {
		if sessIn != sess {
			t.Errorf("session: got %v, want %v", sessIn, sess)
		}
		gotReq = msg.(*controlpb.Pong)
		return &controlpb.Ping{Nonce: gotReq.Nonce * 2}, nil
	})

	payload := makeRPCPayload(t, &controlpb.Pong{Nonce: 21})
	s.dispatchRPC(tc, sess, &controlpb.RpcRequest{CorrelationId: 99, Payload: payload})

	if gotReq == nil || gotReq.Nonce != 21 {
		t.Fatalf("handler not invoked with expected payload; got %v", gotReq)
	}

	resp := readRPCResponse(t, sess)
	if resp.CorrelationId != 99 {
		t.Errorf("CorrelationId: got %d, want 99", resp.CorrelationId)
	}
	if !resp.Ok {
		t.Errorf("Ok: got false (error_message=%q), want true", resp.ErrorMessage)
	}
	var got controlpb.Ping
	if err := proto.Unmarshal(resp.Payload, &got); err != nil {
		t.Fatalf("unmarshal response payload: %v", err)
	}
	if got.Nonce != 42 {
		t.Errorf("response nonce: got %d, want 42", got.Nonce)
	}
}

func TestDispatchRPC_HandlerErrorReturnsErrorResponse(t *testing.T) {
	s, sess, tc := rpcTestServer(t)

	s.HandleRPC(&controlpb.Pong{}, func(_ TickCtx, _ *Session, _ proto.Message) (proto.Message, error) {
		return nil, errors.New("not enough gold")
	})

	payload := makeRPCPayload(t, &controlpb.Pong{Nonce: 1})
	s.dispatchRPC(tc, sess, &controlpb.RpcRequest{CorrelationId: 7, Payload: payload})

	resp := readRPCResponse(t, sess)
	if resp.CorrelationId != 7 {
		t.Errorf("CorrelationId: got %d, want 7", resp.CorrelationId)
	}
	if resp.Ok {
		t.Error("Ok: got true, want false")
	}
	if resp.ErrorMessage != "not enough gold" {
		t.Errorf("ErrorMessage: got %q", resp.ErrorMessage)
	}
	if len(resp.Payload) != 0 {
		t.Errorf("Payload should be empty on error; got %d bytes", len(resp.Payload))
	}
}

func TestDispatchRPC_HandlerPanicRecoversAndReturnsErrorResponse(t *testing.T) {
	s, sess, tc := rpcTestServer(t)

	s.HandleRPC(&controlpb.Pong{}, func(_ TickCtx, _ *Session, _ proto.Message) (proto.Message, error) {
		panic("kaboom")
	})

	payload := makeRPCPayload(t, &controlpb.Pong{Nonce: 1})
	s.dispatchRPC(tc, sess, &controlpb.RpcRequest{CorrelationId: 11, Payload: payload})

	resp := readRPCResponse(t, sess)
	if resp.Ok {
		t.Error("Ok: got true, want false")
	}
	if resp.ErrorMessage != ErrRPCHandlerPanic.Error() {
		t.Errorf("ErrorMessage: got %q, want %q", resp.ErrorMessage, ErrRPCHandlerPanic.Error())
	}

	// Session must NOT be scheduled for disconnect — RPC panics don't kick.
	if env := s.events.drain(); len(env) != 0 {
		t.Errorf("expected no events; got %v", env)
	}
}

func TestDispatchRPC_NoHandlerReturnsErrorResponse(t *testing.T) {
	s, sess, tc := rpcTestServer(t)

	payload := makeRPCPayload(t, &controlpb.Pong{Nonce: 1})
	s.dispatchRPC(tc, sess, &controlpb.RpcRequest{CorrelationId: 3, Payload: payload})

	resp := readRPCResponse(t, sess)
	if resp.Ok {
		t.Error("Ok: got true, want false")
	}
	if resp.CorrelationId != 3 {
		t.Errorf("CorrelationId: got %d, want 3", resp.CorrelationId)
	}
	if resp.ErrorMessage == "" {
		t.Error("ErrorMessage empty")
	}
}

func TestDispatchRPC_NoPrototypeReturnsErrorResponse(t *testing.T) {
	// Server without RPCRequestPrototype set.
	s := NewServer(Config{})
	sess := &Session{
		ID:        1,
		sendTCP:   make(chan []byte, 16),
		authState: sessionReady,
	}
	s.sessions[1] = sess
	tc := &tickCtx{server: s, tick: 1, now: time.Now()}

	s.dispatchRPC(tc, sess, &controlpb.RpcRequest{CorrelationId: 5, Payload: []byte("anything")})

	resp := readRPCResponse(t, sess)
	if resp.Ok {
		t.Error("Ok: got true, want false")
	}
	if resp.CorrelationId != 5 {
		t.Errorf("CorrelationId: got %d, want 5", resp.CorrelationId)
	}
}

func TestDispatchRPC_MalformedPayloadReturnsErrorResponse(t *testing.T) {
	s, sess, tc := rpcTestServer(t)

	// Random bytes that don't parse as a ClientFrame.
	s.dispatchRPC(tc, sess, &controlpb.RpcRequest{
		CorrelationId: 8,
		Payload:       []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f},
	})

	resp := readRPCResponse(t, sess)
	if resp.Ok {
		t.Error("Ok: got true, want false")
	}
	if resp.CorrelationId != 8 {
		t.Errorf("CorrelationId: got %d, want 8", resp.CorrelationId)
	}
}

func TestDispatchRPC_EmptyOneofReturnsErrorResponse(t *testing.T) {
	s, sess, tc := rpcTestServer(t)

	// Marshal an empty ClientFrame (no oneof populated).
	payload, err := proto.Marshal(&controlpb.ClientFrame{})
	if err != nil {
		t.Fatal(err)
	}
	s.dispatchRPC(tc, sess, &controlpb.RpcRequest{CorrelationId: 12, Payload: payload})

	resp := readRPCResponse(t, sess)
	if resp.Ok {
		t.Error("Ok: got true, want false")
	}
	if resp.CorrelationId != 12 {
		t.Errorf("CorrelationId: got %d, want 12", resp.CorrelationId)
	}
}
