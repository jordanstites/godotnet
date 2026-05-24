---
title: godotnet — Bootstrap Prompt for Claude Code
type: reference
status: living
tags: [neon-era, tech, claude-code, bootstrap, godotnet]
created: 2026-05-17
---

# godotnet — Bootstrap Prompt for Claude Code

Paste everything below the `--- PASTE BELOW ---` line into a fresh Claude Code session in an empty Go project directory, with plan mode on. Claude Code should produce a build plan (package skeleton, file list, build order). Review the plan, then exit plan mode to let it execute.

**Tip:** start the session with the Go module already initialized:

```bash
mkdir godotnet && cd godotnet
go mod init github.com/jstites/godotnet   # adjust import path to your real one
git init
```

Then open Claude Code, enter plan mode, and paste.

---

--- PASTE BELOW ---

# Project: godotnet — a Go networking library for Godot multiplayer

I'm starting a new Go package. I want you to **plan** the structure first (we're in plan mode); I'll review the plan before you write code. Stub out a minimal but real skeleton — package layout, key interfaces, file-by-file scope notes, and a commit-by-commit build order. **Don't write implementations yet** — just the skeleton and the plan.

## What this library is

`godotnet` is a Go library that gives a game server a clean API for talking to Godot multiplayer clients. It owns transport, framing, session lifecycle, dispatch, and the tick loop. It stays out of game logic entirely.

The concrete first user is a small cyberpunk 2D MMO ("Neon Era") — Godot 4 client, Go server, friends-only scale (~30 players max), running over Tailscale. But the library is generic.

## Architecture overview

Server-side, single Go process. Four kinds of goroutine, internal to the library:

1. **UDP reader** (1) — tight `ReadFromUDP` loop, decodes protobuf, pushes to the world inbox.
2. **TCP per-connection** (N) — one per connected player, length-prefixed protobuf framing.
3. **Tick loop** (1) — fixed-rate ticker (default 20Hz). Drains inbound mailbox, runs handlers, calls user `OnTick`, sleeps.
4. **Outbound writers** — per-player goroutines fed from per-player outbound channels.

**Critical property**: there is one logical "game thread" — the tick loop goroutine. All handlers and the `OnTick` callback run on it, sequentially, in order. Game state mutation happens here. No locks needed for game state — the user never touches it from elsewhere.

I/O goroutines do NOT touch game state. They only push messages into the inbox channel and pull from outbound channels.

## Wire protocol (the contract with Godot clients)

**TCP framing**: 4-byte big-endian length prefix, then protobuf bytes.

```
┌─────────────────┬────────────────────────┐
│ length (u32 BE) │ protobuf-encoded bytes │
└─────────────────┴────────────────────────┘
```

**UDP framing**: one protobuf message per datagram. Max safe payload ~1200 bytes (no fragmentation in v1).

**Handshake (library-defined messages, in `internal/proto/control.proto`):**

```protobuf
syntax = "proto3";
package godotnet.control;

message Login {
  bytes credentials = 1;  // opaque; library passes to Authenticate callback
}

message LoginResponse {
  bool   ok            = 1;
  string error_message = 2;
  uint32 player_id     = 3;
  string session_token = 4;
  string udp_endpoint  = 5;
}

message UdpHandshake {
  uint32 player_id     = 1;
  string session_token = 2;
}

message UdpHandshakeAck {
  bool   ok        = 1;
  uint32 player_id = 2;
}

message Ping { uint64 nonce = 1; }
message Pong { uint64 nonce = 1; }
```

**Game messages**: the *user* defines their own `.proto` with `ClientMessage` and `ServerMessage` envelopes using `oneof`. The library is generic over `proto.Message`; users register handlers per concrete message type.

## Public API surface (target shape)

This is the API the user code will see. Stub these in the plan — don't implement yet.

```go
package godotnet

import (
    "context"
    "time"
    "google.golang.org/protobuf/proto"
)

type PlayerID uint32

type Config struct {
    TCPAddr  string         // ":7777"
    UDPAddr  string         // ":7778"
    TickRate time.Duration  // 50 * time.Millisecond for 20Hz

    // Required: how to verify credentials on TCP login.
    // The proto.Message is whatever the game's login request type is.
    Authenticate func(ctx context.Context, credentials proto.Message) (PlayerID, error)

    // The prototype message the library should unmarshal credentials INTO before
    // calling Authenticate. Game registers this.
    LoginPrototype proto.Message

    // Optional hooks
    OnConnect    func(s *Session)
    OnDisconnect func(s *Session, reason error)
    Logger       Logger
}

type Session struct {
    ID          PlayerID
    Username    string
    ConnectedAt time.Time
    UserData    any  // game-side scratchpad
    // unexported transport bits
}

type Server struct { /* unexported */ }

func NewServer(cfg Config) *Server

// Register a handler for a specific message type. The handler runs on the tick
// goroutine, after the mailbox has buffered the message but before OnTick fires
// for that tick.
type ClientHandler func(ctx TickCtx, sess *Session, msg proto.Message)
func (s *Server) HandleClient(prototype proto.Message, handler ClientHandler)

// Set the per-tick simulation callback.
func (s *Server) OnTick(cb func(ctx TickCtx))

// Blocks until ctx cancels or fatal error.
func (s *Server) Run(ctx context.Context) error

// TickCtx is what handlers and OnTick callbacks use to interact with the library.
// All methods are safe to call only from the tick goroutine.
type TickCtx interface {
    SendUDP(playerID PlayerID, msg proto.Message)
    SendTCP(playerID PlayerID, msg proto.Message)
    BroadcastUDP(msg proto.Message)
    BroadcastTCP(msg proto.Message)
    Sessions() []*Session
    Session(playerID PlayerID) *Session
    Disconnect(playerID PlayerID, reason string)
    Tick() uint64
    Now() time.Time
}

type Logger interface {
    Debug(msg string, kv ...any)
    Info(msg string, kv ...any)
    Warn(msg string, kv ...any)
    Error(msg string, kv ...any)
}
```

## Package layout

```
godotnet/
├── server.go          # Server, Config, NewServer, Run, lifecycle
├── session.go         # Session, PlayerID
├── tcp.go             # TCP listener, per-conn reader/writer
├── udp.go             # UDP read loop, per-player send queue
├── frame.go           # length-prefixed framing for TCP
├── dispatch.go        # handler registration, message-type → handler
├── tick.go            # tick loop, mailbox draining
├── handshake.go       # the TCP-then-UDP handshake protocol
├── tickctx.go         # TickCtx implementation
├── errors.go
├── log.go             # Logger interface, default impl (slog wrapper)
│
├── server_test.go     # tests using fake transport
├── handshake_test.go
├── frame_test.go
│
├── chat/              # opt-in: channels, DMs, broadcast, history
├── rpc/               # opt-in: typed request/response
├── sub/               # opt-in: event subscriptions
├── interest/          # opt-in: spatial filter
├── snapshot/          # opt-in: ring-buffered history
│
├── internal/
│   ├── proto/         # control.proto + generated code (handshake, ping)
│   └── transport/     # transport interface for testability
│
├── go.mod
├── go.sum
├── README.md
└── LICENSE
```

The five sub-packages (`chat`, `rpc`, `sub`, `interest`, `snapshot`) are **stubs only** in v0.1 — just the package directory with a placeholder `.go` file containing the planned exported types as commented-out signatures. They get built later as the consuming game needs them.

## Scope: what's IN

- TCP + UDP transport
- Length-prefixed protobuf framing on TCP
- Datagram protobuf on UDP
- Session pairing (TCP+UDP via token handshake)
- Auth handshake mechanism (game provides the policy)
- Message dispatch (typed handlers)
- Tick loop with mailbox draining
- Outbound primitives (`SendUDP`, `SendTCP`, broadcasts)
- Disconnect detection + cleanup
- Pluggable logging
- Pluggable transport for tests
- Future opt-in modules: chat, RPC, subscriptions, interest management, snapshot history, reliable-UDP

## Scope: what's OUT

- Game state, entities, replication semantics
- Physics simulation
- Pathfinding
- AI / behavior trees
- Inventory, quests, combat, NPCs, items, leveling
- Auth policy (game provides the credential check)
- Persistence (SQLite, Postgres, anything)
- Encryption (Tailscale handles it)
- Godot-side code (separate concern; only the wire protocol is the contract)
- **cgo** — pure Go only. Anything that would require cgo is out.

## Constraints

- **Go version**: 1.22+ (use the new `slog`, range-over-int, etc.)
- **Protobuf**: `google.golang.org/protobuf` only. Generate with `protoc-gen-go`.
- **No cgo**. No C dependencies. `go build` produces a static binary on any GOOS/GOARCH combo.
- **Dependencies kept minimal**. Standard library + `google.golang.org/protobuf` + `log/slog`. Anything else needs justification.
- **Generics where they help**. Go 1.18+ generics are fine; don't use them for things that work cleanly with interfaces.
- **Context-aware**. Every long-running operation takes a `context.Context`.
- **Testable without sockets**. The transport layer is behind an interface so tests use in-memory pipes.

## Build order (the commits)

Plan these as concrete, individually-shippable commits. Each commit must compile and pass tests.

1. **`init: go mod init + license + README skeleton`** — empty `server.go`, package compiles.
2. **`feat: Logger interface + default slog impl`** — minimum required for everything else to log.
3. **`feat: framing — length-prefixed protobuf reader/writer`** — `frame.go` + tests. Pure I/O, no network.
4. **`feat: control.proto + generated code`** — `internal/proto/control.proto`, compiled to `internal/proto/*.pb.go`. Include a `Makefile` target or `go generate` directive.
5. **`feat: transport interface + in-memory fake`** — `internal/transport/transport.go`. Enables testing.
6. **`feat: Server skeleton — NewServer, Config, no-op Run`** — `server.go` + `tickctx.go`. Compiles, Run blocks until context cancel.
7. **`feat: tick loop + mailbox`** — `tick.go`. Run drives a fixed-rate ticker, drains an empty mailbox, calls OnTick. Tests: tick rate observed, OnTick called.
8. **`feat: TCP listener + per-conn goroutines`** — `tcp.go`. Accepts connections, reads framed messages, pushes to mailbox. Doesn't know about handshakes yet.
9. **`feat: dispatch — HandleClient registration + typed handlers`** — `dispatch.go`. Mailbox messages routed to handlers by message type.
10. **`feat: login handshake over TCP`** — `handshake.go`. Login/LoginResponse flow. Authenticate callback fires. Test with fake transport.
11. **`feat: UDP listener + send queue`** — `udp.go`. Receives datagrams, doesn't know about sessions yet (drops unrecognized).
12. **`feat: UDP handshake + session pairing`** — Token-based pairing. TCP+UDP tied together. Test.
13. **`feat: outbound primitives — SendUDP/TCP, broadcasts`** — TickCtx implementation. Tests with fake transport.
14. **`feat: disconnect detection + cleanup`** — TCP read returns error → cleanup + OnDisconnect hook fires. UDP timeout = TODO comment for now (needs ping/pong).
15. **`feat: ping/pong + UDP keepalive`** — Periodic Ping on UDP, NAT keepalive, zombie detection.
16. **`docs: README with quick-start example`** — minimal Hello World server in the README.
17. **`feat: module stubs — chat, rpc, sub, interest, snapshot`** — each as an empty package with the planned exported types as commented signatures + a `// TODO(v0.2)` marker.

That's v0.1. ~17 commits. The library at this point is *usable* — a game can build the equivalent of Neon Era M0 (two-dots demo) on it.

## Plan-mode deliverables

Before writing any code, produce:

1. **A confirmation that the package layout above is what we're building.** If you see issues, raise them before proceeding.
2. **The list of files you'll create, in order, with a 1-sentence purpose for each.**
3. **The commit-by-commit build order** (you can copy the 17 above or propose changes — explain any changes).
4. **A test strategy** — which tests get written at each step, what they prove.
5. **A list of things you'll need clarified before writing the implementation** (e.g., exact import path for `go.mod`, my GitHub username if it goes in there, license choice).
6. **Specific risks or unknowns you flag** (e.g., "the protobuf code generation may need protoc installed — should we vendor it or document it as a prereq?").

Once you've laid out the plan, **wait for me to approve before exiting plan mode and writing code**.

## What I'll provide when you ask

- Exact import path (likely `github.com/<my-username>/godotnet`)
- License preference (likely MIT)
- Whether I want `protoc` invoked via `Makefile`, `go generate`, or assumed pre-built
- Any preferences on test framework (stdlib `testing` is default; happy to use `testify` if you think it's worth it)

Ready when you are. Plan first; don't write code until I approve the plan.

--- PASTE ABOVE ---

## See also

- [[godotnet Library Design]] — the full design doc this prompt is distilled from
- [[Networking & Tech]] — the broader server architecture
- [[M0 - Two Dots]] — the first concrete thing the library will be used for

← [[Home]]
