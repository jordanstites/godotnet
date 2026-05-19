# Getting Started

This guide walks you through building a minimal multiplayer server with
godotnet and verifying it end-to-end with a small Go test client. The
Godot 4 client is covered at the end.

What you'll have at the finish:

- A Go server listening on TCP + UDP that accepts logins and broadcasts
  player movement.
- A Go test client that connects, sends a Move, and sees the broadcast
  echo back.
- Confidence that the wire protocol is correct before you write any
  Godot code.

Estimated time: **30 minutes.**

## Prerequisites

- **Go 1.23+** — check with `go version`.
- **protoc** — protocol-buffer compiler. On Windows, `winget install Google.Protobuf`.
- **protoc-gen-go** — install with `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest`. Ensure `$GOPATH/bin` (typically `~/go/bin`) is on `PATH` so `protoc` can find it.

Verify:

```sh
go version
protoc --version
protoc-gen-go --version
```

## Step 1: Create a new game project

Make a new directory for your game and initialize a Go module. Pick any
module path you like — the examples below use `example.com/neonera`.

```sh
mkdir neonera
cd neonera
go mod init example.com/neonera
```

Add godotnet and the protobuf runtime:

```sh
go get github.com/jordanstites/godotnet@latest
go get google.golang.org/protobuf
```

Your `go.mod` should now look roughly like:

```go
module example.com/neonera

go 1.23

require (
    github.com/jordanstites/godotnet v0.1.1
    google.golang.org/protobuf v1.36.11
)
```

## Step 2: Define your game protocol

Create `pb/game.proto`:

```protobuf
syntax = "proto3";

package neonera;
option go_package = "example.com/neonera/pb;pb";

// Credentials sent in Login.credentials. The library unmarshals into
// this and hands it to Authenticate.
message Credentials {
  string username = 1;
  string token    = 2;
}

// A player movement update from a client.
message Move {
  float x = 1;
  float y = 2;
}

// Server's broadcast that some player moved.
message PlayerMoved {
  uint32 player_id = 1;
  float  x         = 2;
  float  y         = 3;
}

// The library expects every game-plane frame from the client to be a
// ClientMessage. Add more `oneof` cases here as you add features.
message ClientMessage {
  oneof body {
    Move move = 1;
  }
}

// Symmetric top-level message for server → client game-plane messages.
message ServerMessage {
  oneof body {
    PlayerMoved moved = 1;
  }
}
```

## Step 3: Generate Go code

From the `neonera` directory:

```sh
protoc --go_out=. --go_opt=paths=source_relative pb/game.proto
```

That should create `pb/game.pb.go`.

## Step 4: Write the server

Create `cmd/server/main.go`:

```go
package main

import (
	"context"
	"log"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/jordanstites/godotnet"
	"google.golang.org/protobuf/proto"

	pb "example.com/neonera/pb"
)

func main() {
	var nextID atomic.Uint32

	srv := godotnet.NewServer(godotnet.Config{
		TCPAddr: ":7777",
		UDPAddr: ":7778",

		LoginPrototype: &pb.Credentials{},
		Authenticate: func(_ context.Context, creds proto.Message) (godotnet.PlayerID, error) {
			// No real auth in this demo — just hand out IDs.
			c := creds.(*pb.Credentials)
			id := godotnet.PlayerID(nextID.Add(1))
			log.Printf("login: %q assigned id=%d", c.Username, id)
			return id, nil
		},

		ClientMessagePrototype: &pb.ClientMessage{},

		OnConnect: func(s *godotnet.Session) {
			log.Printf("player %d connected", s.ID)
		},
		OnDisconnect: func(s *godotnet.Session, err error) {
			log.Printf("player %d disconnected: %v", s.ID, err)
		},
	})

	// When a player sends a Move, log it and broadcast to everyone via UDP.
	srv.HandleClient(&pb.Move{}, func(tc godotnet.TickCtx, sess *godotnet.Session, m proto.Message) {
		move := m.(*pb.Move)
		log.Printf("player %d moved to (%.1f, %.1f)", sess.ID, move.X, move.Y)

		tc.BroadcastUDP(&pb.ServerMessage{
			Body: &pb.ServerMessage_Moved{
				Moved: &pb.PlayerMoved{
					PlayerId: uint32(sess.ID),
					X:        move.X,
					Y:        move.Y,
				},
			},
		})
	})

	srv.OnTick(func(tc godotnet.TickCtx) {
		// Per-tick simulation step would go here. Empty for this demo.
		_ = tc
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Println("server listening: tcp :7777, udp :7778")
	if err := srv.Run(ctx); err != nil {
		log.Printf("server stopped: %v", err)
	}
}
```

## Step 5: Run the server

```sh
go run ./cmd/server
```

You should see:

```
server listening: tcp :7777, udp :7778
```

Leave this running. Open a second terminal for the test client.

## Step 6: Write a test client to verify the wire protocol

Create `cmd/testclient/main.go`. This client does the full handshake
and sends one Move:

```go
package main

import (
	"encoding/binary"
	"io"
	"log"
	"net"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/jordanstites/godotnet/controlpb"
	pb "example.com/neonera/pb"
)

func main() {
	// 1. Dial TCP.
	tcp, err := net.Dial("tcp", "127.0.0.1:7777")
	if err != nil {
		log.Fatalf("tcp dial: %v", err)
	}
	defer tcp.Close()

	// 2. Send Login wrapped in ClientFrame.
	creds, _ := proto.Marshal(&pb.Credentials{Username: "alice", Token: "dev"})
	writeFrame(tcp, &controlpb.ClientFrame{
		Body: &controlpb.ClientFrame_Login{
			Login: &controlpb.Login{Credentials: creds},
		},
	})

	// 3. Read LoginResponse.
	respBytes := readFrame(tcp)
	var resp controlpb.ServerFrame
	if err := proto.Unmarshal(respBytes, &resp); err != nil {
		log.Fatalf("unmarshal LoginResponse frame: %v", err)
	}
	lr := resp.GetLoginResponse()
	if lr == nil || !lr.Ok {
		log.Fatalf("login rejected: %+v", lr)
	}
	log.Printf("logged in as player %d, token=%s", lr.PlayerId, lr.SessionToken)

	// 4. Open UDP socket and send UdpHandshake.
	udpServer, err := net.ResolveUDPAddr("udp", "127.0.0.1:7778")
	if err != nil {
		log.Fatal(err)
	}
	udp, err := net.DialUDP("udp", nil, udpServer)
	if err != nil {
		log.Fatal(err)
	}
	defer udp.Close()

	hsBytes, _ := proto.Marshal(&controlpb.ClientFrame{
		Body: &controlpb.ClientFrame_UdpHandshake{
			UdpHandshake: &controlpb.UdpHandshake{
				PlayerId:     lr.PlayerId,
				SessionToken: lr.SessionToken,
			},
		},
	})
	if _, err := udp.Write(hsBytes); err != nil {
		log.Fatalf("udp handshake write: %v", err)
	}

	// 5. Read UdpHandshakeAck.
	buf := make([]byte, 2048)
	udp.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := udp.Read(buf)
	if err != nil {
		log.Fatalf("udp handshake ack read: %v", err)
	}
	var ackFrame controlpb.ServerFrame
	if err := proto.Unmarshal(buf[:n], &ackFrame); err != nil {
		log.Fatalf("unmarshal UdpHandshakeAck: %v", err)
	}
	ack := ackFrame.GetUdpHandshakeAck()
	if ack == nil || !ack.Ok {
		log.Fatalf("udp handshake rejected: %+v", ack)
	}
	log.Printf("udp paired")

	// 6. Send a Move over UDP, wrapped as game_payload.
	inner, _ := proto.Marshal(&pb.ClientMessage{
		Body: &pb.ClientMessage_Move{Move: &pb.Move{X: 42.5, Y: 17.0}},
	})
	moveBytes, _ := proto.Marshal(&controlpb.ClientFrame{
		Body: &controlpb.ClientFrame_GamePayload{GamePayload: inner},
	})
	if _, err := udp.Write(moveBytes); err != nil {
		log.Fatalf("move write: %v", err)
	}
	log.Printf("sent Move(42.5, 17.0)")

	// 7. Read the broadcast echo (since we're the only player).
	udp.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err = udp.Read(buf)
	if err != nil {
		log.Fatalf("broadcast read: %v", err)
	}
	var bf controlpb.ServerFrame
	if err := proto.Unmarshal(buf[:n], &bf); err != nil {
		log.Fatalf("unmarshal broadcast frame: %v", err)
	}
	var sm pb.ServerMessage
	if err := proto.Unmarshal(bf.GetGamePayload(), &sm); err != nil {
		log.Fatalf("unmarshal ServerMessage: %v", err)
	}
	moved := sm.GetMoved()
	log.Printf("server broadcast: player %d at (%.1f, %.1f)", moved.PlayerId, moved.X, moved.Y)
}

// writeFrame marshals msg and writes it as a length-prefixed TCP frame.
func writeFrame(w io.Writer, msg proto.Message) {
	data, err := proto.Marshal(msg)
	if err != nil {
		log.Fatalf("marshal: %v", err)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		log.Fatalf("write header: %v", err)
	}
	if _, err := w.Write(data); err != nil {
		log.Fatalf("write payload: %v", err)
	}
}

// readFrame reads one length-prefixed TCP frame.
func readFrame(r io.Reader) []byte {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		log.Fatalf("read header: %v", err)
	}
	n := binary.BigEndian.Uint32(hdr[:])
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		log.Fatalf("read payload: %v", err)
	}
	return payload
}
```

Run it:

```sh
go run ./cmd/testclient
```

Expected output on the client side:

```
logged in as player 1, token=...
udp paired
sent Move(42.5, 17.0)
server broadcast: player 1 at (42.5, 17.0)
```

And on the server:

```
login: "alice" assigned id=1
player 1 connected
player 1 moved to (42.5, 17.0)
player 1 disconnected: godotnet: read frame header: EOF
```

Run two test clients side by side to see broadcasts go to both. The
wire protocol is now verified end-to-end. Anything else you build on
top — chat, items, NPCs — follows the same pattern: define a message
in `ClientMessage` or `ServerMessage`, register a handler with
`HandleClient`, mutate state in the handler, broadcast via `tc.*`.

## Step 7: Connecting from Godot 4

You don't need to write the handshake state machine yourself —
[**godotnet-client**](https://github.com/jordanstites/godotnet-client)
is a Godot 4 plugin that owns the TCP+UDP handshake, length-prefixed
framing, ping/pong, and auto-reconnect. Your game scripts just send
and receive marshaled bytes.

### 7a. Install the plugin

1. Clone or download godotnet-client and copy its `addons/godotnet_client/`
   into your Godot project's `addons/` folder.
2. Project → Project Settings → Plugins → enable **godotnet_client**.
3. A `GodotNet` autoload singleton is now available globally.

### 7b. Install godobuf to generate types from your `game.proto`

The plugin handles control-plane messages internally, but your own
`game.proto` (`Credentials`, `Move`, `ClientMessage`, `ServerMessage`,
etc.) still needs to be turned into GDScript classes.
[oniksan/godobuf](https://github.com/oniksan/godobuf) is the standard
choice.

1. Copy godobuf into `addons/godobuf/` in your Godot project and enable it.
2. Copy your `game.proto` into the Godot project.
3. Project → Tools → Godobuf → point at `game.proto` → generate
   `game_pb.gd`.

### 7c. Connect and play

A minimal script that mirrors the Go test client:

```gdscript
extends Node

func _ready() -> void:
    GodotNet.connected.connect(_on_connected)
    GodotNet.disconnected.connect(_on_disconnected)
    GodotNet.login_failed.connect(_on_login_failed)
    GodotNet.server_message.connect(_on_server_message)
    GodotNet.set_auto_reconnect(true)

    var creds := GamePb.Credentials.new()
    creds.set_username("alice")
    creds.set_token("dev")
    GodotNet.connect_to_server("127.0.0.1", 7777, creds.to_bytes(), 7778)

func _on_connected(player_id: int) -> void:
    print("connected as player ", player_id)
    var move := GamePb.Move.new()
    move.set_x(42.5); move.set_y(17.0)
    var cm := GamePb.ClientMessage.new()
    cm.set_move(move)
    GodotNet.send_unreliable(cm.to_bytes())

func _on_disconnected(code: int, reason: String) -> void:
    print("disconnected (%d): %s" % [code, reason])

func _on_login_failed(error_message: String) -> void:
    print("login failed: ", error_message)

func _on_server_message(payload: PackedByteArray, _reliable: bool) -> void:
    var sm := GamePb.ServerMessage.new()
    if sm.from_bytes(payload) != GamePb.PB_ERR.NO_ERRORS:
        return
    if sm.has_moved():
        var m := sm.get_moved()
        print("player %d at (%.1f, %.1f)" % [
            m.get_player_id(), m.get_x(), m.get_y()])
```

Run this against the server from Step 5 and you should see the same
"player N at (42.5, 17.0)" log on the Godot side that the Go test
client produced in Step 6.

See the [godotnet-client README](https://github.com/jordanstites/godotnet-client)
for the full API surface (signals, `send_reliable` vs `send_unreliable`,
`DisconnectCode` branches, etc.).

## Common pitfalls

- **Forgot `paths=source_relative` on protoc.** Without it, `protoc-gen-go`
  generates the .pb.go in a nested path matching the go_package, which
  rarely matches your repo layout.
- **Hyphen in the proto `package` line.** Protobuf package names are
  identifiers — letters, digits, underscores only. If your Go module
  has a hyphen (e.g. `example.com/my-game`), don't mirror it as
  `package my-game;`; protoc will fail with `Expected ";"`. Use
  `package my_game;` instead. (The `go_package` option *is* a Go
  import path and can have hyphens — that's why one works and the
  other doesn't.)
- **UDP messages don't arrive.** Make sure you actually completed the
  UDP handshake first — pre-handshake UDP datagrams from unknown
  remotes are dropped silently. Look for the "udp handshake rejected"
  log line on the server.
- **`Move` handler never fires.** Check that the message is wrapped
  exactly twice: outer `ClientFrame` with `game_payload`, inner
  `ClientMessage` with the populated `oneof`. The library unwraps one
  layer for you (ClientFrame → game_payload bytes), then unmarshals the
  inner bytes into `ClientMessagePrototype` and dispatches by the
  inner type.

## Next steps

- Add more message types to your `ClientMessage`/`ServerMessage` and
  register handlers for each.
- Stash per-player state in `Session.UserData` (the library never
  touches it).
- Read [ARCHITECTURE.md](./ARCHITECTURE.md) for the mental model — the
  tick loop, the event queue, the session lifecycle.
- When ready to ship, put the server behind a reverse proxy (NPM /
  nginx) for TLS and ratelimiting.
