package godotnet

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	controlpb "github.com/jordanstites/godotnet/controlpb"
)

func TestHandleLogin_SuccessTransitionsAndQueuesResponse(t *testing.T) {
	s := NewServer(Config{
		UDPAdvertiseAddr: "127.0.0.1:7778",
		LoginPrototype:   &controlpb.Ping{},
		Authenticate: func(_ context.Context, _ proto.Message) (PlayerID, error) {
			return 42, nil
		},
	})

	sess := &Session{
		sendTCP:   make(chan []byte, 16),
		authState: sessionPreLogin,
	}

	creds, err := proto.Marshal(&controlpb.Ping{Nonce: 99})
	if err != nil {
		t.Fatal(err)
	}
	tc := &tickCtx{server: s, tick: 1, now: time.Now()}
	s.handleLogin(tc, sess, &controlpb.Login{Credentials: creds})

	if sess.ID != 42 {
		t.Errorf("sess.ID: got %d, want 42", sess.ID)
	}
	if sess.authState != sessionAwaitingUDP {
		t.Errorf("authState: got %v, want sessionAwaitingUDP", sess.authState)
	}
	if sess.sessionToken == "" {
		t.Error("sessionToken not set")
	}
	if got := s.sessions[42]; got != sess {
		t.Errorf("session not added to s.sessions: got %v", got)
	}

	select {
	case data := <-sess.sendTCP:
		var frame controlpb.ServerFrame
		if err := proto.Unmarshal(data, &frame); err != nil {
			t.Fatal(err)
		}
		resp := frame.GetLoginResponse()
		if resp == nil {
			t.Fatal("ServerFrame body is not LoginResponse")
		}
		if !resp.Ok {
			t.Errorf("Ok: got %v, want true", resp.Ok)
		}
		if resp.PlayerId != 42 {
			t.Errorf("PlayerId: got %d, want 42", resp.PlayerId)
		}
		if resp.SessionToken == "" {
			t.Error("SessionToken empty")
		}
		if resp.UdpEndpoint != "127.0.0.1:7778" {
			t.Errorf("UdpEndpoint: got %q", resp.UdpEndpoint)
		}
	case <-time.After(time.Second):
		t.Fatal("no LoginResponse queued")
	}
}

func TestHandleLogin_AuthRejectedSchedulesDisconnect(t *testing.T) {
	s := NewServer(Config{
		LoginPrototype: &controlpb.Ping{},
		Authenticate: func(_ context.Context, _ proto.Message) (PlayerID, error) {
			return 0, errors.New("bad password")
		},
	})

	sess := &Session{
		sendTCP:   make(chan []byte, 16),
		authState: sessionPreLogin,
	}

	creds, _ := proto.Marshal(&controlpb.Ping{})
	tc := &tickCtx{server: s, tick: 1, now: time.Now()}
	s.handleLogin(tc, sess, &controlpb.Login{Credentials: creds})

	if sess.authState != sessionPreLogin {
		t.Errorf("authState: got %v, want sessionPreLogin", sess.authState)
	}
	if sess.ID != 0 {
		t.Errorf("sess.ID: got %d, want 0", sess.ID)
	}

	// The failing LoginResponse and a disconnect event should both be
	// queued.
	select {
	case <-sess.sendTCP:
	case <-time.After(time.Second):
		t.Fatal("no LoginResponse queued")
	}

	envs := s.events.drain()
	found := false
	for _, e := range envs {
		if e.kind == eventDisconnect && errors.Is(e.reason, ErrAuthRejected) {
			found = true
		}
	}
	if !found {
		t.Errorf("no auth-rejected disconnect event; got %d events", len(envs))
	}
}

func TestHandleLogin_MisconfiguredServer(t *testing.T) {
	// Authenticate is nil.
	s := NewServer(Config{LoginPrototype: &controlpb.Ping{}})
	sess := &Session{sendTCP: make(chan []byte, 16)}
	creds, _ := proto.Marshal(&controlpb.Ping{})
	tc := &tickCtx{server: s, tick: 1, now: time.Now()}
	s.handleLogin(tc, sess, &controlpb.Login{Credentials: creds})

	envs := s.events.drain()
	if len(envs) != 1 || envs[0].kind != eventDisconnect ||
		!errors.Is(envs[0].reason, ErrServerMisconfigured) {
		t.Errorf("expected misconfigured disconnect; got %v", envs)
	}
}

func TestHandleUDPHandshake_SuccessPairsSession(t *testing.T) {
	s := NewServer(Config{})

	sess := &Session{
		ID:           7,
		sessionToken: "secret-token",
		authState:    sessionAwaitingUDP,
	}
	s.sessions[7] = sess

	var connectCalled bool
	s.cfg.OnConnect = func(got *Session) {
		if got != sess {
			t.Errorf("OnConnect called with %v, want %v", got, sess)
		}
		connectCalled = true
	}

	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9999}
	hs := &controlpb.UdpHandshake{PlayerId: 7, SessionToken: "secret-token"}
	tc := &tickCtx{server: s, tick: 1, now: time.Now()}
	s.handleUDPHandshake(tc, addr, hs)

	if sess.authState != sessionReady {
		t.Errorf("authState: got %v, want sessionReady", sess.authState)
	}
	if sess.udpAddr != addr {
		t.Errorf("udpAddr: got %v, want %v", sess.udpAddr, addr)
	}
	if got := s.udpSessions[addr.String()]; got != sess {
		t.Errorf("udpSessions: got %v, want %v", got, sess)
	}
	if !connectCalled {
		t.Error("OnConnect not called")
	}
}

func TestHandleUDPHandshake_BadTokenRejects(t *testing.T) {
	// Run a real UDP loopback so the rejection ACK has somewhere to go.
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	s := NewServer(Config{})
	s.udpConn = conn

	sess := &Session{ID: 7, sessionToken: "right", authState: sessionAwaitingUDP}
	s.sessions[7] = sess

	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1}
	hs := &controlpb.UdpHandshake{PlayerId: 7, SessionToken: "wrong"}
	tc := &tickCtx{server: s, tick: 1, now: time.Now()}
	s.handleUDPHandshake(tc, addr, hs)

	if sess.authState != sessionAwaitingUDP {
		t.Errorf("authState changed: got %v, want sessionAwaitingUDP", sess.authState)
	}
	if sess.udpAddr != nil {
		t.Errorf("udpAddr was set: %v", sess.udpAddr)
	}
}

func TestHandleUDPHandshake_UnknownPlayerRejects(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	s := NewServer(Config{})
	s.udpConn = conn

	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1}
	hs := &controlpb.UdpHandshake{PlayerId: 999, SessionToken: "x"}
	tc := &tickCtx{server: s, tick: 1, now: time.Now()}
	s.handleUDPHandshake(tc, addr, hs)

	if len(s.udpSessions) != 0 {
		t.Errorf("udpSessions populated: %v", s.udpSessions)
	}
}
