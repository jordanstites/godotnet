# godotnet

A Go networking library for Godot 4 multiplayer games. Owns transport,
framing, session lifecycle, dispatch, and the tick loop — stays out of
game logic.

**Status:** v0.1. API may still change. Not yet recommended for external
use.

## Architecture in 30 seconds

- TCP carries reliable messages: login, chat, NPC interaction, item pickup.
- UDP carries time-sensitive messages: positions, snapshots.
- One deterministic **tick goroutine** runs all handlers and your
  `OnTick` callback. No locks on game state, ever.
- All I/O goroutines (TCP reader/writer pairs, UDP reader) push events
  into a bounded queue; the tick loop drains it once per tick and runs
  each event's handler in FIFO order.

## Wire protocol contract

Every TCP frame and UDP datagram is a serialized `ClientFrame` (client →
server) or `ServerFrame` (server → client) defined in
[`controlpb/control.proto`](controlpb/control.proto). The
oneof body discriminates control plane (`Login`, `Ping`, etc.) from game
plane (`game_payload`, which holds your marshaled `ClientMessage` or
`ServerMessage` bytes).

TCP frames are length-prefixed with a 4-byte big-endian length.

## Quick-start

```go
package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/jordanstites/godotnet"
	"google.golang.org/protobuf/proto"

	mygame "example.com/neonera/pb" // your generated package
)

func main() {
	srv := godotnet.NewServer(godotnet.Config{
		TCPAddr: ":7777",
		UDPAddr: ":7778",

		// What credentials look like on the wire and how to verify them.
		LoginPrototype: &mygame.LoginCredentials{},
		Authenticate: func(ctx context.Context, creds proto.Message) (godotnet.PlayerID, error) {
			lc := creds.(*mygame.LoginCredentials)
			return verifyAndAssignID(lc.Username, lc.Token) // game-side logic
		},

		// Your top-level ClientMessage type, with a oneof covering every
		// inbound message variant. The library unmarshals every game-plane
		// frame into a clone of this and dispatches by the populated
		// oneof body.
		ClientMessagePrototype: &mygame.ClientMessage{},

		OnConnect:    func(s *godotnet.Session) { log.Printf("player %d in", s.ID) },
		OnDisconnect: func(s *godotnet.Session, err error) { log.Printf("player %d out: %v", s.ID, err) },
	})

	// Register per-message-type handlers. Handler signatures are
	// (TickCtx, *Session, proto.Message); they run on the tick goroutine.
	srv.HandleClient(&mygame.Move{}, func(tc godotnet.TickCtx, s *godotnet.Session, m proto.Message) {
		move := m.(*mygame.Move)
		// ... apply to world state, optionally call tc.SendUDP/Broadcast* ...
		_ = move
	})

	srv.OnTick(func(tc godotnet.TickCtx) {
		// Per-tick simulation step. Read state, advance, send snapshots.
		// tc.BroadcastUDP(&mygame.StateSnapshot{...})
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := srv.Run(ctx); err != nil {
		log.Printf("server stopped: %v", err)
	}
}

func verifyAndAssignID(username, token string) (godotnet.PlayerID, error) {
	// Your auth logic here.
	return 0, nil
}
```

## Operational notes (v0.1)

The v0.1 server is intended for friends-only or trusted-network
deployments. It does **not** include:

- Connection-rate limiting / DoS protection
- Brute-force login throttling
- IP banning

For public-internet deployments, put the server behind a reverse proxy
(NPM / nginx / HAProxy) or firewall the host. The proxy can also
terminate TLS if you need encrypted traffic — godotnet itself accepts
plain TCP and your proxy handles certs.

## Requirements

- Go 1.23+
- For regenerating `controlpb/*.pb.go`: `protoc` and `protoc-gen-go`
  on `PATH`. Pre-generated files are checked in, so consumers of the
  library don't need protoc.

## Development

```sh
make test       # run all tests
make test-race  # tests under -race (the must-pass CI gate)
make vet        # go vet
make proto      # regenerate protobuf code
```

## Further reading

- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — how the library works inside.
- [docs/GETTING_STARTED.md](docs/GETTING_STARTED.md) — build your first server.
- [godotnet-demos](../godotnet-demos) — a complete Godot project + Go server with one demo per communication style (auth, chat, RPC shop, multiplayer pong, sub-backed stock ticker). The fastest way to see every API in action.

## v0.1 build status

- [x] 1. init: go module + license + README skeleton + Makefile
- [x] 2. Logger interface + default slog impl
- [x] 3. framing — length-prefixed protobuf reader/writer
- [x] 4. control.proto + generated code + go:generate
- [x] 5. transport interface + in-memory fake
- [x] 6. Server skeleton — NewServer, Config, Run
- [x] 7. tick loop + event queue
- [x] 8. TCP listener + per-conn goroutines
- [x] 9. TLS-wrapped TCP listener (optional; off by default)
- [x] 10. dispatch — HandleClient registration + typed handlers
- [x] 11. login handshake over TCP
- [x] 12. UDP listener + send queue
- [x] 13. UDP handshake + session pairing
- [x] 14. outbound primitives — SendUDP/TCP, broadcasts
- [x] 15. disconnect detection + cleanup
- [x] 16. ping/pong + UDP keepalive
- [x] 17. README quick-start example
- [x] 18. module stubs — chat, rpc, sub, interest, snapshot

## License

MIT. See [LICENSE](LICENSE).
