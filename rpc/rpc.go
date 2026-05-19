// Package rpc provides typed request/response over the godotnet TCP
// channel, with correlation IDs, timeouts, and cancellation.
//
// TODO(v0.2): implement. The package is a placeholder so consumers can
// import the path today.
package rpc

// Planned exported types and functions for v0.2:
//
//   type Handler[Req, Resp proto.Message] func(ctx godotnet.TickCtx, sess *godotnet.Session, req Req) (Resp, error)
//
//   type Registry struct { /* unexported */ }
//   func New(server *godotnet.Server) *Registry
//   func Register[Req, Resp proto.Message](r *Registry, prototype Req, h Handler[Req, Resp])
//
//   func Call[Resp proto.Message](ctx context.Context, c *Client, req proto.Message) (Resp, error)
