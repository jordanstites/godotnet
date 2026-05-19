# godotnet Architecture

This document explains how godotnet works internally — the goroutine
model, the event queue, the session lifecycle, and the wire format.
Once you have these in your head, the rest of the library is just
plumbing.

## 1. The mental model: one event loop, one owner of state

A networked game server has to handle many things concurrently — many
players sending messages, periodic simulation steps, broadcasts,
timeouts. The naive Go approach is "one goroutine per connection, use
mutexes to share state." That works for chat servers but falls apart at
game-scale: ordering bugs, lock contention, deadlocks.

godotnet inverts this: **all game state lives behind one goroutine.**
I/O goroutines exist only to copy bytes between sockets and a queue.
They never touch game state.

Concretely:

- Player positions, inventory, NPC AI state, anything game-related —
  only the tick goroutine reads or writes it.
- Every inbound event (from any TCP socket or the UDP socket) is funneled
  through a single bounded queue.
- Every tick (default 20Hz), the tick goroutine drains the queue and
  runs your handlers in FIFO order.
- After draining, it calls your `OnTick` callback.
- Then it sleeps until the next tick.

The result: you never write a lock. Your handlers are pure functions
of `(TickCtx, *Session, proto.Message)` and run sequentially. Two
players sending input at the same wall-clock millisecond? Both events
land in the queue; one handler runs to completion, then the other —
no concurrency to coordinate.

## 2. The goroutine model

A running server has these goroutines:

```
TCP socket (player 1) ──► TCP reader ──┐
TCP socket (player 2) ──► TCP reader ──┤
TCP socket (player N) ──► TCP reader ──┼──► event queue ──► tick goroutine
                                       │   (bounded MPSC      │
UDP socket            ──► UDP reader ──┘    FIFO)             ├─► run handlers
                                                              ├─► run OnTick
                                                              ├─► send pings
                                                              └─► clean up
                                                                  disconnects

TCP socket (player 1) ◄── TCP writer ◄── sess.sendTCP ◄────── tick goroutine
TCP socket (player 2) ◄── TCP writer ◄── sess.sendTCP ◄──────       │
                                                                    │
UDP socket            ◄────────────────────────────────────── tick goroutine
                       (direct WriteTo — no per-player queue)
```

| Goroutine | Count | Job |
|---|---|---|
| Tick goroutine | 1 | Drain events, run handlers + OnTick, send pings, clean up disconnects |
| TCP accept loop | 1 | Accept incoming TCP conns, spawn reader+writer per accepted conn |
| TCP reader | N (one per session) | Read framed bytes off one socket, push to event queue |
| TCP writer | N (one per session) | Drain `sess.sendTCP` channel, write framed bytes to one socket |
| UDP reader | 1 | Read datagrams from the shared UDP socket, push to event queue |

UDP writes come directly from the tick goroutine — no per-player UDP
writer needed, because UDP is connectionless and `net.PacketConn.WriteTo`
is safe to call concurrently.

## 3. The event queue (the heart)

Defined in [events.go](events.go). It's a bounded MPSC FIFO:

- **Bounded** — fixed capacity (default 4096, configurable via
  `Config.EventQueueDepth`). On overflow, push returns false and the
  producer disconnects the offending player.
- **MPSC** — Multi-Producer Single-Consumer. Many goroutines push;
  only the tick goroutine drains.
- **FIFO** — events are processed in the order they arrived.

Each event carries:

```go
type event struct {
    kind    eventKind   // eventMessage, eventConnect, or eventDisconnect
    sess    *Session    // who it's from / for
    payload []byte      // raw protobuf bytes (for eventMessage)
    isUDP   bool        // did it arrive over UDP?
    udpAddr net.Addr    // sender's addr if sess is nil
    reason  error       // why disconnecting (for eventDisconnect)
}
```

The tick goroutine drains in batches:

```go
for _, e := range s.events.drain() {
    s.handleEvent(tc, e)
}
```

`drain()` snapshots the current queue length and returns up to that
many events — bursts of new traffic during drain land in the next tick
rather than starving the loop.

## 4. Session lifecycle

Every connected player walks through this state machine:

```
[TCP accept]
      │
      ▼
 ┌─────────────────┐
 │ sessionPreLogin │  authState; sess.ID == 0; not yet in s.sessions
 └─────────────────┘
      │ receives Login frame
      │ Authenticate() callback runs (on tick goroutine)
      ▼
 ┌──────────────────────┐
 │ sessionAwaitingUDP   │  sess.ID assigned; sessionToken issued;
 └──────────────────────┘  added to s.sessions; LoginResponse sent
      │ receives UdpHandshake datagram
      │ token verified
      ▼
 ┌─────────────────┐
 │ sessionReady    │  udpAddr paired; added to s.udpSessions;
 └─────────────────┘  OnConnect callback fires; game messages flow
      │ TCP read error / TickCtx.Disconnect / ping timeout / queue overflow
      ▼
   [cleanup]   cleanupSession runs:
               - removes from s.sessions / s.udpSessions
               - closes TCP conn (idempotent)
               - closes sess.sendTCP channel (TCP writer drains and exits)
               - fires OnDisconnect callback
```

The `disconnectOnce` field on `Session` coalesces multiple disconnect
triggers (e.g. a TCP error and a `TickCtx.Disconnect` racing) into a
single `eventDisconnect` event so cleanup runs once.

## 5. Wire format

Defined in [internal/proto/control.proto](internal/proto/control.proto).

```
TCP frame:  [4-byte BE length][protobuf bytes]
UDP packet: [protobuf bytes]
```

The protobuf bytes are always a `ClientFrame` (client → server) or
`ServerFrame` (server → client):

```protobuf
message ClientFrame {
  oneof body {
    Login         login         = 1;
    UdpHandshake  udp_handshake = 2;
    Pong          pong          = 3;
    bytes         game_payload  = 16;  // marshaled user ClientMessage
  }
}
```

`ServerFrame` mirrors this for the server → client direction
(`LoginResponse`, `UdpHandshakeAck`, `Ping`, `game_payload`).

When the tick goroutine drains an event, it:

1. Unmarshals `event.payload` as a `ClientFrame`.
2. Switches on the populated oneof field.
3. For control plane (Login / UdpHandshake / Pong), calls the
   appropriate internal handler.
4. For `game_payload`, unmarshals the inner bytes into a clone of
   `Config.ClientMessagePrototype`, extracts the populated oneof body,
   looks up your `ClientHandler` by protobuf full-name, and invokes it.

This means **you never see `ClientFrame` directly.** You define your
own top-level `ClientMessage` message with a oneof covering every
inbound type, and register handlers per inner type. The library
handles the outer wrapping/unwrapping.

## 6. Where each file fits

| File | What's inside |
|---|---|
| [server.go](server.go) | `Server`, `Config`, `NewServer`, `Run` (lifecycle), `pushTCP`, `scheduleDisconnect`, `cleanupSession` |
| [session.go](session.go) | `Session`, `PlayerID`, `sessionAuthState`, `atomicOnce` |
| [events.go](events.go) | `event` struct, `queue` (bounded MPSC FIFO), `eventKind` constants |
| [tick.go](tick.go) | `runTickLoop`, `handleEvent`, `dispatchMessage` |
| [tcp.go](tcp.go) | `runTCPAccept`, per-conn `serveTCPConn`, `runTCPReader`, `runTCPWriter` |
| [udp.go](udp.go) | `runUDPReader`, `udpSendRaw` |
| [frame.go](frame.go) | `ReadFrame`, `WriteFrame` — length-prefixed TCP framing |
| [handshake.go](handshake.go) | `handleLogin`, `handleUDPHandshake`, token generation |
| [dispatch.go](dispatch.go) | `handlerRegistry`, `dispatchUserMessage`, oneof extraction |
| [ping.go](ping.go) | `runPingTick`, `handlePong`, `sendPing` |
| [tickctx.go](tickctx.go) | `TickCtx` interface + `tickCtx` implementation |
| [errors.go](errors.go) | Sentinel errors (`ErrAuthRejected`, `ErrPingTimeout`, etc.) |
| [log.go](log.go) | `Logger` interface + default slog-backed impl |
| [internal/proto/control.proto](internal/proto/control.proto) | Wire-format protobuf schema |
| [internal/transport/](internal/transport/) | `ListenTCP`, `ListenUDP`, plus `MemoryListener` for tests |

## 7. Public API at a glance

### Constructing the server

```go
srv := godotnet.NewServer(godotnet.Config{ ... })
```

### Config — the knobs you'll actually touch

| Field | What it does |
|---|---|
| `TCPAddr` | TCP listen address (e.g. `":7777"`). Empty disables TCP. |
| `UDPAddr` | UDP listen address (e.g. `":7778"`). Empty disables UDP. |
| `UDPAdvertiseAddr` | Host:port sent to clients in `LoginResponse.udp_endpoint`. Set when UDPAddr binds to `0.0.0.0` but clients need a routable address. |
| `TickRate` | Period between OnTick calls. Default `50ms` (20Hz). |
| `LoginPrototype` | Prototype message `Login.credentials` is unmarshaled into. **Required.** |
| `Authenticate` | Function that verifies credentials and returns a `PlayerID`. **Required.** |
| `ClientMessagePrototype` | Your top-level `ClientMessage` type. Library unmarshals every game frame into a clone of this and dispatches by the populated oneof body. **Required for game traffic.** |
| `OnConnect` | Optional; fires when a session is fully paired (TCP + UDP both done). |
| `OnDisconnect` | Optional; fires when a session ends, with the reason. |
| `Logger` | Optional; structured logger. Nil = drop logs. |

There are also depth/timeout tuning knobs (`EventQueueDepth`,
`SendQueueDepth`, `MaxFrameLen`, `MaxUDPPayload`, `PingInterval`,
`PingTimeout`) with sensible defaults.

### Registering handlers (before Run)

```go
// Handle a specific protobuf message type. Matched by full-name.
srv.HandleClient(&mygame.Move{}, func(tc TickCtx, sess *Session, msg proto.Message) {
    move := msg.(*mygame.Move)
    // ...
})

// Per-tick simulation callback.
srv.OnTick(func(tc TickCtx) {
    // ...
})
```

### Running

```go
err := srv.Run(ctx)  // blocks until ctx cancels or fatal error
```

### What you can do inside a handler (TickCtx methods)

All called on the tick goroutine. Safe everywhere, no locks needed.

```go
tc.SendTCP(playerID, msg)        // queue a TCP message to one player
tc.SendUDP(playerID, msg)        // queue a UDP datagram to one player
tc.BroadcastTCP(msg)             // send to every connected player over TCP
tc.BroadcastUDP(msg)             // send to every UDP-paired player over UDP
tc.Sessions()                    // snapshot slice of connected sessions
tc.Session(playerID)             // one session by ID, or nil
tc.Disconnect(playerID, reason)  // schedule a disconnect for end-of-tick
tc.Tick()                        // current tick number (uint64)
tc.Now()                         // time the current tick started
```

## 8. Common patterns

### "Player moved → broadcast new position to everyone"

```go
srv.HandleClient(&mygame.Move{}, func(tc godotnet.TickCtx, sess *godotnet.Session, m proto.Message) {
    move := m.(*mygame.Move)
    sess.UserData.(*Player).Position = move.Position

    tc.BroadcastUDP(&mygame.ServerMessage{
        Body: &mygame.ServerMessage_Moved{
            Moved: &mygame.PlayerMoved{Id: uint32(sess.ID), Position: move.Position},
        },
    })
})
```

### "Player said something in chat → broadcast over TCP"

```go
srv.HandleClient(&mygame.Chat{}, func(tc godotnet.TickCtx, sess *godotnet.Session, m proto.Message) {
    chat := m.(*mygame.Chat)
    tc.BroadcastTCP(&mygame.ServerMessage{
        Body: &mygame.ServerMessage_Chat{
            Chat: &mygame.ServerChat{From: sess.Username, Body: chat.Body},
        },
    })
})
```

### "Per-tick simulation step"

```go
srv.OnTick(func(tc godotnet.TickCtx) {
    // Run physics, advance NPCs, etc.
    advanceWorld(tc.Now())

    // Once every 5 ticks, broadcast a snapshot.
    if tc.Tick()%5 == 0 {
        tc.BroadcastUDP(&mygame.ServerMessage{
            Body: &mygame.ServerMessage_Snapshot{Snapshot: buildSnapshot()},
        })
    }
})
```

### "Kick a player"

```go
tc.Disconnect(playerID, "cheating detected")
// On the next tick, OnDisconnect fires with errors.New("cheating detected").
```

### "Per-player game state"

Stash whatever you want in `Session.UserData any`. The library never
touches it.

```go
srv.OnConnect = func(s *godotnet.Session) {
    s.UserData = &Player{
        ID:        s.ID,
        Inventory: []Item{},
        Position:  spawnPoint,
    }
}

srv.HandleClient(&mygame.UseItem{}, func(tc godotnet.TickCtx, s *godotnet.Session, m proto.Message) {
    player := s.UserData.(*Player)
    // ... mutate player.Inventory ...
})
```

## 9. Things to know

### Backpressure

- Per-player TCP send queue (`sess.sendTCP`, default 256 items) — on
  overflow, the player is disconnected. This protects the tick
  goroutine from being held up by one slow client.
- The shared event queue (default 4096) — on overflow, the producing
  player is disconnected.
- UDP writes never block; on socket-buffer-full the kernel drops the
  packet (which is fine for UDP semantics).

### Shutdown

`Run(ctx)` returns when `ctx` is cancelled. Internally it cancels a
derived context that the tick loop, TCP accept loop, UDP reader, and
per-connection goroutines all watch. The accept loop closes the
listener so blocked `Accept()` returns; conn goroutines close their
conns so blocked reads/writes return. Everything drains; `Run` returns
`ctx.Err()`.

### Panic recovery

If a `ClientHandler` panics, the library recovers, logs, and
disconnects that player — the tick loop survives. If `OnTick` panics,
it's logged and the loop continues.

### Where game logic does NOT belong

- I/O goroutines (TCP/UDP readers and writers). They never call user
  code.
- Goroutines spawned outside the library. If you start your own
  goroutines, they cannot touch game state without locks.

If you need to do slow work (DB query, HTTP call), launch a goroutine
from inside a handler, do the work there, then deliver the result by
pushing back through a channel that the next tick reads via `OnTick`.
Or use a pattern where you store "pending requests" in `UserData` and
poll them in `OnTick`.

## 10. Reading list

The actor-model + event-queue pattern under godotnet is well-trodden
ground. To go deeper:

- **Gaffer on Games** — [gafferongames.com](https://gafferongames.com). Read *Snapshot Interpolation*, *State Synchronization*, *Networked Physics*. Foundations of modern multiplayer.
- **Valve's Source Multiplayer Networking** — [developer.valvesoftware.com/wiki/Source_Multiplayer_Networking](https://developer.valvesoftware.com/wiki/Source_Multiplayer_Networking). Lag compensation and interpolation in production.
- **Tim Ford's "Overwatch Gameplay Architecture and Netcode"** — GDC 2017 on YouTube. Real production-scale architecture.
- **Designing for Scalability with Erlang/OTP** — Cesarini & Vinoski. The conceptual home of the actor + event-queue pattern.
