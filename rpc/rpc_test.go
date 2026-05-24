package rpc_test

import (
	"testing"

	"github.com/jordanstites/godotnet"
	controlpb "github.com/jordanstites/godotnet/controlpb"
	"github.com/jordanstites/godotnet/rpc"
)

// Smoke test: Register accepts a typed handler and tolerates nil
// inputs without panicking. End-to-end dispatch semantics (oneof
// extraction, error envelope, panic recovery, correlation_id echo)
// are covered in the main-package rpc_test.go.
func TestRegister_AcceptsTypedHandlerAndNilInputs(t *testing.T) {
	srv := godotnet.NewServer(godotnet.Config{
		RPCRequestPrototype: &controlpb.ClientFrame{},
	})
	rpc.Register[*controlpb.Pong, *controlpb.Ping](srv, &controlpb.Pong{},
		func(_ godotnet.TickCtx, _ *godotnet.Session, req *controlpb.Pong) (*controlpb.Ping, error) {
			return &controlpb.Ping{Nonce: req.GetNonce()}, nil
		})

	// Nil server / handler must be no-ops, not panics — game code
	// might wire these up conditionally during boot.
	rpc.Register[*controlpb.Pong, *controlpb.Ping](nil, &controlpb.Pong{}, nil)
	rpc.Register[*controlpb.Pong, *controlpb.Ping](srv, &controlpb.Pong{}, nil)
}
