package godotnet

import "net"

// The tick goroutine is structurally an event loop with a bounded
// event queue: I/O goroutines build events and push them in; the tick
// goroutine drains the queue once per tick and acts on each event in
// FIFO order. Bounded MPSC FIFO — many producers (TCP readers, UDP
// reader, scheduleDisconnect callers) push; one consumer (the tick
// goroutine) drains.

// eventKind tags an event's purpose. The tick goroutine switches on
// this to decide what to do with the event.
type eventKind uint8

const (
	eventMessage eventKind = iota
	eventConnect
	eventDisconnect
)

// event is the unit drained by the tick goroutine from the queue.
// I/O goroutines push events; the tick goroutine consumes them.
//
// For eventMessage events, payload is the raw protobuf bytes pulled
// off the wire. The tick goroutine parses based on per-session state —
// pre-login frames are Login, post-login frames are the user's
// top-level ClientMessage.
type event struct {
	kind eventKind
	sess *Session

	// payload is raw protobuf bytes, for eventMessage events.
	payload []byte

	// isUDP is true when the eventMessage came in over UDP rather than
	// the TCP control channel.
	isUDP bool

	// udpAddr is the sender's address for UDP datagrams arriving from
	// an unrecognized remote (sess is nil in that case).
	udpAddr net.Addr

	// reason is the disconnect cause for eventDisconnect events.
	reason error
}

// queue is a bounded channel of events. Push is non-blocking so I/O
// goroutines never stall waiting on a slow tick loop; on overflow the
// caller is responsible for treating the producing session as
// misbehaving and disconnecting it.
type queue struct {
	ch chan event
}

func newQueue(depth int) *queue {
	if depth <= 0 {
		depth = DefaultEventQueueDepth
	}
	return &queue{ch: make(chan event, depth)}
}

// push attempts to enqueue e. Returns false when the queue is full;
// the caller should disconnect the producing player.
func (q *queue) push(e event) bool {
	select {
	case q.ch <- e:
		return true
	default:
		return false
	}
}

// drain returns every event currently buffered, up to a snapshot of
// the channel length at call time. Events pushed during the drain
// itself remain for the next tick — guaranteeing the drain terminates
// even under a burst of inbound traffic.
func (q *queue) drain() []event {
	n := len(q.ch)
	if n == 0 {
		return nil
	}
	out := make([]event, 0, n)
	for j := 0; j < n; j++ {
		select {
		case e := <-q.ch:
			out = append(out, e)
		default:
			return out
		}
	}
	return out
}
