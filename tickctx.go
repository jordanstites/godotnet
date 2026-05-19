package godotnet

import (
	"errors"
	"time"

	"google.golang.org/protobuf/proto"

	controlpb "github.com/jordanstites/godotnet/controlpb"
)

// TickCtx is the context passed to ClientHandlers and the OnTick callback.
// All methods are safe to call only from the tick goroutine.
type TickCtx interface {
	// SendUDP queues a UDP datagram to the player. No-op if the player
	// is unknown or not yet UDP-paired.
	SendUDP(playerID PlayerID, msg proto.Message)

	// SendTCP queues a TCP message to the player. No-op if the player
	// is unknown.
	SendTCP(playerID PlayerID, msg proto.Message)

	// BroadcastUDP fans the message to every UDP-paired session.
	BroadcastUDP(msg proto.Message)

	// BroadcastTCP fans the message to every authenticated session.
	BroadcastTCP(msg proto.Message)

	// Sessions returns a freshly-allocated snapshot of authenticated
	// sessions.
	Sessions() []*Session

	// Session returns the session with the given ID, or nil.
	Session(playerID PlayerID) *Session

	// Disconnect schedules the player for cleanup; OnDisconnect fires
	// on the next tick boundary.
	Disconnect(playerID PlayerID, reason string)

	// Tick returns the monotonically-increasing tick number.
	Tick() uint64

	// Now returns the time the current tick started.
	Now() time.Time
}

// tickCtx is the concrete implementation. Instances are freshly created
// per tick; closing over s lets the methods reach into server state.
type tickCtx struct {
	server *Server
	tick   uint64
	now    time.Time
}

func (c *tickCtx) SendUDP(playerID PlayerID, msg proto.Message) {
	sess := c.server.sessions[playerID]
	if sess == nil || sess.authState != sessionReady || sess.udpAddr == nil {
		return
	}
	data, err := marshalGameServerFrame(msg)
	if err != nil {
		c.server.log.Error("SendUDP marshal", "err", err, "player", playerID)
		return
	}
	c.server.udpSendRaw(sess.udpAddr, data)
}

func (c *tickCtx) SendTCP(playerID PlayerID, msg proto.Message) {
	sess := c.server.sessions[playerID]
	if sess == nil {
		return
	}
	data, err := marshalGameServerFrame(msg)
	if err != nil {
		c.server.log.Error("SendTCP marshal", "err", err, "player", playerID)
		return
	}
	c.server.pushTCP(sess, data)
}

func (c *tickCtx) BroadcastUDP(msg proto.Message) {
	data, err := marshalGameServerFrame(msg)
	if err != nil {
		c.server.log.Error("BroadcastUDP marshal", "err", err)
		return
	}
	for _, sess := range c.server.sessions {
		if sess.authState == sessionReady && sess.udpAddr != nil {
			c.server.udpSendRaw(sess.udpAddr, data)
		}
	}
}

func (c *tickCtx) BroadcastTCP(msg proto.Message) {
	data, err := marshalGameServerFrame(msg)
	if err != nil {
		c.server.log.Error("BroadcastTCP marshal", "err", err)
		return
	}
	for _, sess := range c.server.sessions {
		c.server.pushTCP(sess, data)
	}
}

// marshalGameServerFrame marshals msg and wraps it in a ServerFrame
// with the game_payload oneof set, returning the marshaled frame ready
// to write to the wire.
func marshalGameServerFrame(msg proto.Message) ([]byte, error) {
	inner, err := proto.Marshal(msg)
	if err != nil {
		return nil, err
	}
	frame := &controlpb.ServerFrame{
		Body: &controlpb.ServerFrame_GamePayload{GamePayload: inner},
	}
	return proto.Marshal(frame)
}

func (c *tickCtx) Sessions() []*Session {
	out := make([]*Session, 0, len(c.server.sessions))
	for _, sess := range c.server.sessions {
		out = append(out, sess)
	}
	return out
}

func (c *tickCtx) Session(playerID PlayerID) *Session {
	return c.server.sessions[playerID]
}

func (c *tickCtx) Disconnect(playerID PlayerID, reason string) {
	sess := c.server.sessions[playerID]
	if sess == nil {
		return
	}
	c.server.scheduleDisconnect(sess, errors.New(reason))
}

func (c *tickCtx) Tick() uint64    { return c.tick }
func (c *tickCtx) Now() time.Time  { return c.now }

// compile-time assertion.
var _ TickCtx = (*tickCtx)(nil)
