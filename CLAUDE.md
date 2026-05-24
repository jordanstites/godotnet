# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`godotnet` is a Go networking library for Godot 4 multiplayer games. It owns transport (TCP+UDP), length-prefixed framing, session lifecycle, dispatch, and the tick loop. It deliberately stays out of game logic. The paired Godot 4 client plugin lives in a separate repo at `../godotnet-client/`.

Status is v0.1 — API may still change.

## Commands

Use the Makefile (do not invoke `go test` directly without thinking — the race build is the must-pass CI gate):

```sh
make test       # go test ./...
make test-race  # go test -race -count=1 ./...   (CI gate)
make vet        # go vet ./...
make build      # go build ./...
make proto      # regenerate internal/proto/*.pb.go (needs protoc + protoc-gen-go on PATH)
```

Run a single test:

```sh
go test -run TestName ./...
go test -race -run TestName ./        # race-flagged version, single package
```

Generated protobuf files (`internal/proto/*.pb.go`) are checked in; library consumers don't need protoc. Only re-run `make proto` after editing `internal/proto/control.proto`.

## Architecture — the load-bearing idea

**All game state lives behind one goroutine.** I/O goroutines copy bytes between sockets and a bounded MPSC FIFO event queue; the tick goroutine drains the queue, runs registered handlers in FIFO order, then runs the user `OnTick` callback. Result: handlers never need locks, and ordering is deterministic.

Goroutine inventory of a running server:

| Goroutine | Count | Job |
|---|---|---|
| Tick | 1 | drain events, run handlers + OnTick, send pings, clean up disconnects |
| TCP accept loop | 1 | accept conns, spawn reader+writer per conn |
| TCP reader | N (one per session) | read framed bytes, push to event queue |
| TCP writer | N (one per session) | drain `sess.sendTCP`, write framed bytes |
| UDP reader | 1 | read datagrams from shared UDP socket, push to event queue |

UDP writes happen directly from the tick goroutine — `PacketConn.WriteTo` is concurrency-safe and UDP is connectionless, so no per-player UDP writer exists.

### Wire protocol (the contract both repos must agree on)

Every TCP frame and UDP datagram is a serialized `ClientFrame` (client→server) or `ServerFrame` (server→client) from [internal/proto/control.proto](internal/proto/control.proto). TCP frames are length-prefixed with a 4-byte big-endian length; UDP datagrams are bare protobuf bytes.

The frame's `oneof body` separates **control plane** (`Login`, `UdpHandshake`, `Ping`, `Pong`, …) from **game plane** (`game_payload` — opaque bytes holding the user's marshaled top-level `ClientMessage` / `ServerMessage`) from **RPC plane** (`RpcRequest` / `RpcResponse` — library-owned envelopes carrying correlation IDs and opaque user request/response bytes). The library never parses `game_payload`; it unmarshals into a clone of `Config.ClientMessagePrototype` and dispatches by populated oneof variant. The RPC plane works the same way against `Config.RPCRequestPrototype`.

**Implication:** adding/changing user-defined game messages does NOT require a `control.proto` change or a paired client-plugin release. Only changes to `Login`, `LoginResponse`, `UdpHandshake`, `UdpHandshakeAck`, `Ping`, `Pong`, or the frame wrappers themselves break the client plugin — and `../godotnet-client/COMPATIBILITY.md` pins a specific upstream commit of `control.proto`. Bump that pin when shipping a wire-breaking change.

### Session lifecycle state machine

```
TCP accept → sessionPreLogin (ID=0, not in s.sessions)
   → Login frame → Authenticate() runs on tick goroutine
   → sessionAwaitingUDP (ID assigned, sessionToken issued, LoginResponse sent)
   → UdpHandshake datagram + token verified
   → sessionReady (udpAddr paired, OnConnect fires, game messages flow)
   → cleanupSession (TCP error / TickCtx.Disconnect / ping timeout / queue overflow)
```

`Session.disconnectOnce` coalesces racing disconnect triggers (e.g. TCP error and a `TickCtx.Disconnect` at the same time) into a single `eventDisconnect` so cleanup runs exactly once.

### File map

| File | Contents |
|---|---|
| [server.go](server.go) | `Server`, `Config`, `NewServer`, `Run`, `pushTCP`, `scheduleDisconnect`, `cleanupSession` |
| [session.go](session.go) | `Session`, `PlayerID`, `sessionAuthState`, `atomicOnce` |
| [events.go](events.go) | bounded MPSC FIFO `queue`, `event` struct, `eventKind` constants |
| [tick.go](tick.go) | `runTickLoop`, `handleEvent`, `dispatchMessage` |
| [tcp.go](tcp.go) | `runTCPAccept`, `serveTCPConn`, `runTCPReader`, `runTCPWriter` |
| [udp.go](udp.go) | `runUDPReader`, `udpSendRaw` |
| [frame.go](frame.go) | length-prefixed `ReadFrame` / `WriteFrame` |
| [handshake.go](handshake.go) | `handleLogin`, `handleUDPHandshake`, token generation |
| [dispatch.go](dispatch.go) | `handlerRegistry`, oneof extraction, `dispatchUserMessage`; `rpcRegistry`, `dispatchRPC`, response framing |
| [rpc/](rpc/) | Typed generic `Register[Req, Resp]` wrapper over `Server.HandleRPC` |
| [ping.go](ping.go) | `runPingTick`, `handlePong`, `sendPing` |
| [tickctx.go](tickctx.go) | `TickCtx` interface + implementation |
| [errors.go](errors.go) | sentinel errors (`ErrAuthRejected`, `ErrPingTimeout`, …) |
| [log.go](log.go) | `Logger` interface + default slog impl |
| [internal/proto/](internal/proto/) | `control.proto` wire schema + generated `control.pb.go` |
| [internal/transport/](internal/transport/) | `ListenTCP`, `ListenUDP`, `MemoryListener` (test fake) |

Top-level subpackages (`chat/`, `rpc/`, `sub/`, `interest/`, `snapshot/`) are v0.1 stubs reserved for future modules.

[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) is the long-form version of this section — read it when touching the tick loop, dispatch, or session state machine.

## Rules of the road for changes

- **Game logic never runs off the tick goroutine.** I/O goroutines (TCP readers/writers, UDP reader) are forbidden from calling user code. If you need slow work (DB, HTTP) from a handler, spawn a goroutine inside the handler, do the work there, and deliver results back via a channel that `OnTick` polls — or stash pending state in `Session.UserData`.
- **Backpressure = disconnect.** Per-player TCP send queue overflow (default 256) and shared event queue overflow (default 4096) both disconnect the offending player rather than blocking. UDP writes never block; kernel drops on socket-buffer-full.
- **Panic recovery is built in.** A panicking `ClientHandler` is recovered, logged, and disconnects only that player. A panicking `OnTick` is logged and the loop continues. A panicking `RPCHandler` is recovered, logged, and returned to the caller as `RpcResponse{ok=false, error_message=ErrRPCHandlerPanic.Error()}` — the player stays connected (an RPC panic is a bug in one handler, not grounds for kicking). Don't add try/recover yourself in handlers.
- **RPC always echoes a response.** Every code path in `dispatchRPC` sends exactly one `RpcResponse` with the matching `correlation_id` — no prototype, malformed payload, empty oneof, unknown type, handler error, panic, and marshal failure all produce error responses. The client's pending-call map relies on this; don't add a path that silently drops a request.
- **`Session.UserData any` is the user's per-player slot.** The library never reads or writes it.
- **v0.1 server has no rate-limiting, brute-force throttling, or IP banning.** It assumes friends-only or trusted-network deployments behind a reverse proxy / firewall. Don't add half-measures; either ship a full solution or leave it to the proxy.

## Pairing with godotnet-client

The Godot 4 client plugin in the sibling repo (`../godotnet-client/`) vendors a copy of `control.proto` at `addons/godotnet_client/control.proto` and hand-codes the control-plane codec in `control_codec.gd`. When you edit `internal/proto/control.proto`:

1. Commit the change here and note the commit hash.
2. In the client repo, update the vendored `.proto` to match byte-for-byte and update `control_codec.gd` for any field/type/numbering change.
3. Bump the version pair table in `../godotnet-client/COMPATIBILITY.md`.
