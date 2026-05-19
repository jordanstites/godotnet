# godotnet

A Go networking library for Godot 4 multiplayer games. Owns transport,
framing, session lifecycle, dispatch, and the tick loop — stays out of
game logic.

**Status:** v0.1 in progress. API will change. Not yet recommended for
external use.

## What it gives you

- TCP (TLS-wrapped) for reliable messages: login, chat, RPC.
- UDP for time-sensitive messages: positions, snapshots.
- A single deterministic tick goroutine where all handlers and your
  `OnTick` callback run, in order, with no locks on game state.
- Pluggable logger, pluggable transport for tests.

## Status of v0.1

This commit is the project skeleton — package compiles, nothing else.
See the build-order checklist below.

- [x] 1. init: go module + license + README skeleton + Makefile
- [ ] 2. feat: Logger interface + default slog impl
- [ ] 3. feat: framing — length-prefixed protobuf reader/writer
- [ ] 4. feat: control.proto + generated code + go:generate
- [ ] 5. feat: transport interface + in-memory fake
- [ ] 6. feat: Server skeleton — NewServer, Config, no-op Run
- [ ] 7. feat: tick loop + mailbox
- [ ] 8. feat: TCP listener + per-conn goroutines (plain)
- [ ] 9. feat: TLS-wrapped TCP listener
- [ ] 10. feat: dispatch — HandleClient registration + typed handlers
- [ ] 11. feat: login handshake over TLS-TCP
- [ ] 12. feat: UDP listener + send queue
- [ ] 13. feat: UDP handshake + session pairing
- [ ] 14. feat: outbound primitives — SendUDP/TCP, broadcasts
- [ ] 15. feat: disconnect detection + cleanup
- [ ] 16. feat: ping/pong + UDP keepalive
- [ ] 17. docs: README quick-start example
- [ ] 18. feat: module stubs — chat, rpc, sub, interest, snapshot

## Requirements

- Go 1.23+
- For regenerating `internal/proto/*.pb.go`: `protoc` and
  `protoc-gen-go` on PATH. Pre-generated files are checked in, so
  consumers of the library don't need protoc.

## Development

```sh
make test       # run tests
make test-race  # run tests under -race (the must-pass gate)
make vet        # go vet
make proto      # regenerate protobuf code
```

## License

MIT. See [LICENSE](LICENSE).
