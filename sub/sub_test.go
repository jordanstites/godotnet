package sub_test

import (
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/jordanstites/godotnet"
	controlpb "github.com/jordanstites/godotnet/internal/proto"
	"github.com/jordanstites/godotnet/sub"
)

// fakeTickCtx records SendUDP / SendTCP calls so tests can assert who
// received what over which transport. All other TickCtx methods are
// unused by sub.Publish and panic if invoked, which would catch the
// implementation accidentally reaching beyond the documented surface.
type fakeTickCtx struct {
	udpSends []sentMsg
	tcpSends []sentMsg
}

type sentMsg struct {
	to  godotnet.PlayerID
	msg proto.Message
}

func (c *fakeTickCtx) SendUDP(p godotnet.PlayerID, m proto.Message) {
	c.udpSends = append(c.udpSends, sentMsg{p, m})
}
func (c *fakeTickCtx) SendTCP(p godotnet.PlayerID, m proto.Message) {
	c.tcpSends = append(c.tcpSends, sentMsg{p, m})
}
func (c *fakeTickCtx) BroadcastUDP(_ proto.Message)            { panic("unused") }
func (c *fakeTickCtx) BroadcastTCP(_ proto.Message)            { panic("unused") }
func (c *fakeTickCtx) Sessions() []*godotnet.Session           { panic("unused") }
func (c *fakeTickCtx) Session(_ godotnet.PlayerID) *godotnet.Session { panic("unused") }
func (c *fakeTickCtx) Disconnect(_ godotnet.PlayerID, _ string)     { panic("unused") }
func (c *fakeTickCtx) Tick() uint64                            { return 1 }
func (c *fakeTickCtx) Now() time.Time                          { return time.Now() }

// recipients returns the player IDs (sorted) that received messages.
func recipients(sends []sentMsg) []godotnet.PlayerID {
	out := make([]godotnet.PlayerID, 0, len(sends))
	for _, s := range sends {
		out = append(out, s.to)
	}
	slices.Sort(out)
	return out
}

func TestSubscribeThenPublish_OnlyMatchingPlayersReceive(t *testing.T) {
	m := sub.New()
	m.Subscribe(1, "stock:AAPL")
	m.Subscribe(2, "stock:AAPL")
	m.Subscribe(3, "stock:MSFT")

	tc := &fakeTickCtx{}
	m.Publish(tc, "stock:AAPL", &controlpb.Ping{Nonce: 99})

	got := recipients(tc.udpSends)
	want := []godotnet.PlayerID{1, 2}
	if !slices.Equal(got, want) {
		t.Errorf("recipients: got %v, want %v", got, want)
	}
	if len(tc.tcpSends) != 0 {
		t.Errorf("default Publish should use UDP, got %d tcp sends", len(tc.tcpSends))
	}
}

func TestUnsubscribe_RemovesSingleSubscription(t *testing.T) {
	m := sub.New()
	m.Subscribe(1, "stock:AAPL")
	m.Subscribe(1, "stock:MSFT")
	m.Subscribe(2, "stock:AAPL")

	m.Unsubscribe(1, "stock:AAPL")

	tc := &fakeTickCtx{}
	m.Publish(tc, "stock:AAPL", &controlpb.Ping{})
	if got := recipients(tc.udpSends); !slices.Equal(got, []godotnet.PlayerID{2}) {
		t.Errorf("after unsubscribe of 1: got %v, want [2]", got)
	}

	tc2 := &fakeTickCtx{}
	m.Publish(tc2, "stock:MSFT", &controlpb.Ping{})
	if got := recipients(tc2.udpSends); !slices.Equal(got, []godotnet.PlayerID{1}) {
		t.Errorf("MSFT still has player 1: got %v, want [1]", got)
	}
}

func TestUnsubscribeAll_RemovesPlayerFromEveryTopic(t *testing.T) {
	m := sub.New()
	m.Subscribe(1, "stock:AAPL")
	m.Subscribe(1, "stock:MSFT")
	m.Subscribe(1, "stock:GOOG")
	m.Subscribe(2, "stock:AAPL")

	m.UnsubscribeAll(1)

	if topics := m.Topics(1); len(topics) != 0 {
		t.Errorf("player 1 still has topics: %v", topics)
	}
	// Player 2 should still be on AAPL.
	if got := m.Subscribers("stock:AAPL"); !slices.Equal(got, []godotnet.PlayerID{2}) {
		t.Errorf("AAPL subscribers: got %v, want [2]", got)
	}
	// MSFT/GOOG should be gone entirely since player 1 was the only subscriber.
	if got := m.Subscribers("stock:MSFT"); len(got) != 0 {
		t.Errorf("MSFT should be empty: got %v", got)
	}
	if got := m.Subscribers("stock:GOOG"); len(got) != 0 {
		t.Errorf("GOOG should be empty: got %v", got)
	}
}

func TestPublishToEmptyTopic_NoOp(t *testing.T) {
	m := sub.New()
	tc := &fakeTickCtx{}
	m.Publish(tc, "stock:NOSUCH", &controlpb.Ping{})
	if len(tc.udpSends)+len(tc.tcpSends) != 0 {
		t.Errorf("publish to empty topic should not send: udp=%d tcp=%d",
			len(tc.udpSends), len(tc.tcpSends))
	}
}

func TestDuplicateSubscribe_Idempotent(t *testing.T) {
	m := sub.New()
	m.Subscribe(1, "stock:AAPL")
	m.Subscribe(1, "stock:AAPL")
	m.Subscribe(1, "stock:AAPL")

	tc := &fakeTickCtx{}
	m.Publish(tc, "stock:AAPL", &controlpb.Ping{})
	if len(tc.udpSends) != 1 {
		t.Errorf("duplicate subscribe should not multiply sends: got %d, want 1", len(tc.udpSends))
	}
}

func TestPublishOver_TCPTransport(t *testing.T) {
	m := sub.New()
	m.Subscribe(1, "doors:lobby")
	m.Subscribe(2, "doors:lobby")

	tc := &fakeTickCtx{}
	m.PublishOver(tc, "doors:lobby", &controlpb.Ping{}, sub.TransportTCP)

	if got := recipients(tc.tcpSends); !slices.Equal(got, []godotnet.PlayerID{1, 2}) {
		t.Errorf("tcp recipients: got %v, want [1 2]", got)
	}
	if len(tc.udpSends) != 0 {
		t.Errorf("TransportTCP should not send via UDP, got %d", len(tc.udpSends))
	}
}

func TestUnsubscribe_TopicDeletedWhenLastSubscriberLeaves(t *testing.T) {
	m := sub.New()
	m.Subscribe(1, "ephemeral")
	m.Unsubscribe(1, "ephemeral")

	// Re-subscribing should work cleanly.
	m.Subscribe(2, "ephemeral")
	if got := m.Subscribers("ephemeral"); !slices.Equal(got, []godotnet.PlayerID{2}) {
		t.Errorf("after recreate: got %v, want [2]", got)
	}
}

func TestUnsubscribeAll_UnknownPlayerIsNoOp(t *testing.T) {
	m := sub.New()
	// Should not panic or otherwise misbehave.
	m.UnsubscribeAll(42)
}
