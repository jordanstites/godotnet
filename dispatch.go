package godotnet

import (
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
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
