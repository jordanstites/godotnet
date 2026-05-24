// Package rpc provides typed client→server request/response on top of
// the godotnet TCP plane.
//
// The wire-level work (RpcRequest/RpcResponse framing, correlation-ID
// echoing, error envelope) lives in the main godotnet package; this
// package only adds a generic, type-safe registration helper on top of
// godotnet.Server.HandleRPC so handlers don't have to do their own
// proto.Message → concrete-type cast and resp/err assertion.
//
// Usage:
//
//	srv := godotnet.NewServer(godotnet.Config{
//	    // ...other fields...
//	    RPCRequestPrototype: &mygame.RpcRequest{},
//	})
//
//	rpc.Register[*mygame.BuyItem, *mygame.BuyItemResponse](srv,
//	    &mygame.BuyItem{},
//	    func(tc godotnet.TickCtx, sess *godotnet.Session, req *mygame.BuyItem) (*mygame.BuyItemResponse, error) {
//	        // ...handler runs on the tick goroutine...
//	    })
//
// The user's top-level RpcRequest message must hold a single oneof
// over every request type. The library extracts the populated body and
// matches it by protobuf full-name. Responses are bare user messages
// (no envelope) — the client knows the response type from the request
// type at schema level.
package rpc

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/jordanstites/godotnet"
)

// Handler is the typed signature game code implements per request type.
type Handler[Req, Resp proto.Message] func(tc godotnet.TickCtx, sess *godotnet.Session, req Req) (Resp, error)

// Register installs h on srv as the handler for inbound RPC requests
// whose populated oneof body type matches prototype. Wraps the typed
// handler in an untyped godotnet.RPCHandler that does the concrete-type
// assertion once per call.
//
// Must be called before srv.Run.
func Register[Req, Resp proto.Message](srv *godotnet.Server, prototype Req, h Handler[Req, Resp]) {
	if srv == nil || h == nil {
		return
	}
	srv.HandleRPC(prototype, func(tc godotnet.TickCtx, sess *godotnet.Session, msg proto.Message) (proto.Message, error) {
		req, ok := msg.(Req)
		if !ok {
			// Would only happen on a programmer error — registry keyed
			// by full-name, so the concrete type should always match.
			return nil, fmt.Errorf("rpc: handler type mismatch: got %T", msg)
		}
		resp, err := h(tc, sess, req)
		if err != nil {
			return nil, err
		}
		return resp, nil
	})
}
