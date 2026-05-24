// Package sub provides server-pushed event subscriptions: a player
// subscribes to a named topic, the server publishes events on that
// topic, and the library fans them out only to subscribed sessions.
//
// Usage:
//
//	subMgr := sub.New()
//
//	// In a handler, subscribe a player to a topic.
//	srv.HandleClient(&pb.SubscribeTopic{}, func(tc godotnet.TickCtx, sess *godotnet.Session, m proto.Message) {
//	    subMgr.Subscribe(sess.ID, sub.Topic(m.(*pb.SubscribeTopic).GetTopic()))
//	})
//
//	// From OnTick or any handler, publish to all subscribers.
//	srv.OnTick(func(tc godotnet.TickCtx) {
//	    subMgr.Publish(tc, "stock:AAPL", &pb.StockTick{Symbol: "AAPL", Price: 187.32})
//	})
//
//	// Critical: clean up subscriptions on disconnect so the maps
//	// don't leak. The library has no implicit hook for this.
//	srv.Config.OnDisconnect = func(s *godotnet.Session, _ error) {
//	    subMgr.UnsubscribeAll(s.ID)
//	}
//
// Threading: all methods must be called from the tick goroutine
// (handlers and OnTick already run there). No locks; mutating
// concurrently is a bug.
package sub

import (
	"google.golang.org/protobuf/proto"

	"github.com/jordanstites/godotnet"
)

// Topic is a free-form string identifying a logical channel. Common
// patterns: "stock:AAPL", "guild:7", "level:downtown".
type Topic string

// Transport selects which socket Publish uses for fan-out.
type Transport int

const (
	// TransportUDP — fire-and-forget per subscriber. Right for
	// position broadcasts, price ticks, effects: cheap, lossy is fine.
	TransportUDP Transport = iota

	// TransportTCP — ordered and reliable per subscriber. Right for
	// events that must arrive: door unlocked, NPC died, item spawned.
	TransportTCP
)

// Manager tracks topic→subscribers and the reverse mapping. Construct
// with New; one instance per Server is typical.
type Manager struct {
	// Both maps are touched only from the tick goroutine, so no mutex
	// is needed. The reverse perPlayer map exists so UnsubscribeAll
	// is O(topics-this-player-is-on), not O(all-topics).
	subscribers map[Topic]map[godotnet.PlayerID]struct{}
	perPlayer   map[godotnet.PlayerID]map[Topic]struct{}
}

// New returns an empty Manager.
func New() *Manager {
	return &Manager{
		subscribers: map[Topic]map[godotnet.PlayerID]struct{}{},
		perPlayer:   map[godotnet.PlayerID]map[Topic]struct{}{},
	}
}

// Subscribe registers playerID as a subscriber of topic. Idempotent —
// subscribing twice has no effect.
func (m *Manager) Subscribe(playerID godotnet.PlayerID, topic Topic) {
	subs, ok := m.subscribers[topic]
	if !ok {
		subs = map[godotnet.PlayerID]struct{}{}
		m.subscribers[topic] = subs
	}
	subs[playerID] = struct{}{}

	topics, ok := m.perPlayer[playerID]
	if !ok {
		topics = map[Topic]struct{}{}
		m.perPlayer[playerID] = topics
	}
	topics[topic] = struct{}{}
}

// Unsubscribe removes playerID from topic. No-op if not subscribed.
// Removes the topic entry entirely once the last subscriber leaves.
func (m *Manager) Unsubscribe(playerID godotnet.PlayerID, topic Topic) {
	if subs, ok := m.subscribers[topic]; ok {
		delete(subs, playerID)
		if len(subs) == 0 {
			delete(m.subscribers, topic)
		}
	}
	if topics, ok := m.perPlayer[playerID]; ok {
		delete(topics, topic)
		if len(topics) == 0 {
			delete(m.perPlayer, playerID)
		}
	}
}

// UnsubscribeAll removes playerID from every topic they're on. Call
// this from your OnDisconnect callback to keep the maps from leaking.
func (m *Manager) UnsubscribeAll(playerID godotnet.PlayerID) {
	topics, ok := m.perPlayer[playerID]
	if !ok {
		return
	}
	for topic := range topics {
		if subs, ok := m.subscribers[topic]; ok {
			delete(subs, playerID)
			if len(subs) == 0 {
				delete(m.subscribers, topic)
			}
		}
	}
	delete(m.perPlayer, playerID)
}

// Publish sends msg to every subscriber of topic over UDP (the
// default for fan-out traffic). No-op if the topic has no subscribers.
func (m *Manager) Publish(tc godotnet.TickCtx, topic Topic, msg proto.Message) {
	m.PublishOver(tc, topic, msg, TransportUDP)
}

// PublishOver sends msg to every subscriber of topic over the chosen
// transport. No-op if the topic has no subscribers. If a subscriber's
// session has been torn down without us being notified yet, the
// underlying SendUDP / SendTCP silently no-ops.
func (m *Manager) PublishOver(tc godotnet.TickCtx, topic Topic, msg proto.Message, transport Transport) {
	subs, ok := m.subscribers[topic]
	if !ok || len(subs) == 0 {
		return
	}
	switch transport {
	case TransportTCP:
		for pid := range subs {
			tc.SendTCP(pid, msg)
		}
	default: // TransportUDP
		for pid := range subs {
			tc.SendUDP(pid, msg)
		}
	}
}

// Subscribers returns a freshly allocated slice of subscriber player
// IDs for topic. Order is unspecified. Useful for tests and admin
// commands; not the fast path (use Publish for normal fan-out).
func (m *Manager) Subscribers(topic Topic) []godotnet.PlayerID {
	subs, ok := m.subscribers[topic]
	if !ok {
		return nil
	}
	out := make([]godotnet.PlayerID, 0, len(subs))
	for pid := range subs {
		out = append(out, pid)
	}
	return out
}

// Topics returns a freshly allocated slice of topic names that
// playerID is currently subscribed to. Order is unspecified.
func (m *Manager) Topics(playerID godotnet.PlayerID) []Topic {
	topics, ok := m.perPlayer[playerID]
	if !ok {
		return nil
	}
	out := make([]Topic, 0, len(topics))
	for t := range topics {
		out = append(out, t)
	}
	return out
}
