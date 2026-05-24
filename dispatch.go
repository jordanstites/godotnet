package godotnet

import (
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	controlpb "github.com/jordanstites/godotnet/controlpb"
)

// handlerRegistry maps a protobuf full-name to a ClientHandler. The
// registry is intended to be populated before Run and read on the tick
// goroutine; the lock guards the rare case of late registration.
type handlerRegistry struct {
	mu       sync.RWMutex
	handlers map[protoreflect.FullName]ClientHandler
}

// register stores h under prototype's protobuf full-name. Overwrites
// any prior registration for that type. Passing a nil prototype or
// handler is a no-op.
func (r *handlerRegistry) register(prototype proto.Message, h ClientHandler) {
	if prototype == nil || h == nil {
		return
	}
	name := prototype.ProtoReflect().Descriptor().FullName()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.handlers == nil {
		r.handlers = make(map[protoreflect.FullName]ClientHandler)
	}
	r.handlers[name] = h
}

// lookup returns the handler registered for msg's concrete type.
func (r *handlerRegistry) lookup(msg proto.Message) (ClientHandler, bool) {
	if msg == nil {
		return nil, false
	}
	name := msg.ProtoReflect().Descriptor().FullName()
	r.mu.RLock()
	h, ok := r.handlers[name]
	r.mu.RUnlock()
	return h, ok
}

// dispatchUserMessage unmarshals payload as the user's
// ClientMessagePrototype, extracts the populated oneof body, and routes
// it to the matching handler. Logs and drops on any failure.
func (s *Server) dispatchUserMessage(tc *tickCtx, sess *Session, payload []byte) {
	if s.cfg.ClientMessagePrototype == nil {
		s.log.Warn("ClientMessagePrototype not set; dropping inbound message",
			"player", sess.ID,
			"bytes", len(payload),
		)
		return
	}

	msg := proto.Clone(s.cfg.ClientMessagePrototype)
	proto.Reset(msg)
	if err := proto.Unmarshal(payload, msg); err != nil {
		s.log.Debug("malformed client message",
			"err", err,
			"player", sess.ID,
		)
		return
	}

	inner := extractOneofBody(msg)
	if inner == nil {
		s.log.Debug("client message has no oneof body",
			"player", sess.ID,
		)
		return
	}

	h, ok := s.handlers.lookup(inner)
	if !ok {
		s.log.Debug("no handler for message type",
			"type", inner.ProtoReflect().Descriptor().FullName(),
			"player", sess.ID,
		)
		return
	}

	s.invokeClientHandler(h, tc, sess, inner)
}

// extractOneofBody returns the populated message inside the first oneof
// of msg, or nil if no oneof is populated or the populated field is not
// a message-typed field.
func extractOneofBody(msg proto.Message) proto.Message {
	refl := msg.ProtoReflect()
	oneofs := refl.Descriptor().Oneofs()
	for i := 0; i < oneofs.Len(); i++ {
		of := oneofs.Get(i)
		f := refl.WhichOneof(of)
		if f == nil {
			continue
		}
		if f.Kind() != protoreflect.MessageKind {
			continue
		}
		return refl.Get(f).Message().Interface()
	}
	return nil
}

// invokeClientHandler calls h with panic-recovery so a faulty handler
// cannot kill the tick loop. On panic, the offending session is
// scheduled for disconnect.
func (s *Server) invokeClientHandler(h ClientHandler, tc TickCtx, sess *Session, msg proto.Message) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("client handler panic recovered",
				"panic", r,
				"player", sess.ID,
				"type", msg.ProtoReflect().Descriptor().FullName(),
			)
			s.scheduleDisconnect(sess, ErrHandlerPanic)
		}
	}()
	h(tc, sess, msg)
}

// rpcRegistry maps a protobuf full-name to an RPCHandler. Same shape
// as handlerRegistry but with the RPC handler signature.
type rpcRegistry struct {
	mu       sync.RWMutex
	handlers map[protoreflect.FullName]RPCHandler
}

func (r *rpcRegistry) register(prototype proto.Message, h RPCHandler) {
	if prototype == nil || h == nil {
		return
	}
	name := prototype.ProtoReflect().Descriptor().FullName()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.handlers == nil {
		r.handlers = make(map[protoreflect.FullName]RPCHandler)
	}
	r.handlers[name] = h
}

func (r *rpcRegistry) lookup(msg proto.Message) (RPCHandler, bool) {
	if msg == nil {
		return nil, false
	}
	name := msg.ProtoReflect().Descriptor().FullName()
	r.mu.RLock()
	h, ok := r.handlers[name]
	r.mu.RUnlock()
	return h, ok
}

// dispatchRPC handles an inbound RpcRequest. On every code path it
// sends exactly one RpcResponse with the matching correlation ID — the
// client's pending-call map relies on that to clean up. Failure modes:
// no RPCRequestPrototype, malformed payload, empty oneof, unknown
// type, handler error, handler panic, response marshal error.
func (s *Server) dispatchRPC(tc *tickCtx, sess *Session, env *controlpb.RpcRequest) {
	cid := env.GetCorrelationId()

	if s.cfg.RPCRequestPrototype == nil {
		s.log.Warn("RPCRequestPrototype not set; rejecting RPC",
			"player", sess.ID,
			"cid", cid,
		)
		s.sendRPCError(sess, cid, "rpc not configured on server")
		return
	}

	msg := proto.Clone(s.cfg.RPCRequestPrototype)
	proto.Reset(msg)
	if err := proto.Unmarshal(env.GetPayload(), msg); err != nil {
		s.log.Debug("malformed rpc request payload",
			"err", err,
			"player", sess.ID,
			"cid", cid,
		)
		s.sendRPCError(sess, cid, "malformed rpc request")
		return
	}

	inner := extractOneofBody(msg)
	if inner == nil {
		s.log.Debug("rpc request has no oneof body",
			"player", sess.ID,
			"cid", cid,
		)
		s.sendRPCError(sess, cid, "rpc request has no body")
		return
	}

	h, ok := s.rpcHandlers.lookup(inner)
	if !ok {
		s.log.Debug("no rpc handler for request type",
			"type", inner.ProtoReflect().Descriptor().FullName(),
			"player", sess.ID,
			"cid", cid,
		)
		s.sendRPCError(sess, cid, "no handler for request type")
		return
	}

	resp, err := s.invokeRPCHandler(h, tc, sess, inner, cid)
	if err != nil {
		s.sendRPCError(sess, cid, err.Error())
		return
	}

	var respBytes []byte
	if resp != nil {
		b, mErr := proto.Marshal(resp)
		if mErr != nil {
			s.log.Error("rpc response marshal",
				"err", mErr,
				"player", sess.ID,
				"type", resp.ProtoReflect().Descriptor().FullName(),
				"cid", cid,
			)
			s.sendRPCError(sess, cid, "response marshal failed")
			return
		}
		respBytes = b
	}

	s.sendRPCOK(sess, cid, respBytes)
}

// invokeRPCHandler calls h with panic-recovery. A panic is converted
// to an error so the response path is the same — the player stays
// connected (an RPC panic is a bug in one handler, not necessarily
// grounds for kicking the player).
func (s *Server) invokeRPCHandler(h RPCHandler, tc TickCtx, sess *Session, req proto.Message, cid uint64) (resp proto.Message, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("rpc handler panic recovered",
				"panic", r,
				"player", sess.ID,
				"type", req.ProtoReflect().Descriptor().FullName(),
				"cid", cid,
			)
			resp = nil
			err = ErrRPCHandlerPanic
		}
	}()
	return h(tc, sess, req)
}

// sendRPCOK wraps respBytes (may be nil) in an RpcResponse{ok=true}
// and pushes it onto sess's TCP send queue.
func (s *Server) sendRPCOK(sess *Session, cid uint64, respBytes []byte) {
	data, err := marshalRPCResponseFrame(&controlpb.RpcResponse{
		CorrelationId: cid,
		Ok:            true,
		Payload:       respBytes,
	})
	if err != nil {
		s.log.Error("rpc response frame marshal", "err", err, "player", sess.ID, "cid", cid)
		return
	}
	s.pushTCP(sess, data)
}

// sendRPCError sends an RpcResponse{ok=false, error_message=msg}.
func (s *Server) sendRPCError(sess *Session, cid uint64, msg string) {
	data, err := marshalRPCResponseFrame(&controlpb.RpcResponse{
		CorrelationId: cid,
		Ok:            false,
		ErrorMessage:  msg,
	})
	if err != nil {
		s.log.Error("rpc error frame marshal", "err", err, "player", sess.ID, "cid", cid)
		return
	}
	s.pushTCP(sess, data)
}

// marshalRPCResponseFrame wraps an RpcResponse in a ServerFrame and
// marshals it to bytes ready for the TCP wire.
func marshalRPCResponseFrame(resp *controlpb.RpcResponse) ([]byte, error) {
	frame := &controlpb.ServerFrame{
		Body: &controlpb.ServerFrame_RpcResponse{RpcResponse: resp},
	}
	return proto.Marshal(frame)
}
