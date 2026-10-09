package room

import (
	"context"
	"encoding/json"
	"net"
	"reflect"
	"testing"
	"time"
)

type client struct {
	conn    net.Conn
	encoder *json.Encoder
	decoder *json.Decoder
	welcome Message
}

func connect(t *testing.T, address string, id, token string) *client {
	t.Helper()
	c, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result := &client{conn: c, encoder: json.NewEncoder(c), decoder: json.NewDecoder(c)}
	if err := result.encoder.Encode(Message{Type: "join", ID: id, Token: token}); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := result.decoder.Decode(&result.welcome); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return result
}
func snapshot(t *testing.T, c *client, accepts func(Message) bool) Message {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var message Message
		if err := c.decoder.Decode(&message); err != nil {
			t.Fatal(err)
		}
		if message.Type == "snapshot" && accepts(message) {
			return message
		}
	}
}
func launch(t *testing.T) (string, *Server) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := New(2 * time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("server leaked workers")
		}
	})
	return listener.Addr().String(), server
}

func TestClientsAgreeAtSameFrameAndReconnectWithoutReplay(t *testing.T) {
	address, _ := launch(t)
	a, b := connect(t, address, "", ""), connect(t, address, "", "")
	id, token := a.welcome.ID, a.welcome.Token
	if id == "" || token == "" {
		t.Fatal("missing resume credentials")
	}
	if err := a.encoder.Encode(Message{Type: "move", Seq: 1, DX: 1}); err != nil {
		t.Fatal(err)
	}
	first := snapshot(t, a, func(m Message) bool { return m.Players[id].Seq == 1 })
	second := snapshot(t, b, func(m Message) bool { return m.Frame == first.Frame })
	if !reflect.DeepEqual(first.Players, second.Players) {
		t.Fatal("clients diverged")
	}
	_ = a.conn.Close()
	resumed := connect(t, address, id, token)
	saved := snapshot(t, resumed, func(m Message) bool { return m.Players[id].Seq == 1 })
	if saved.Players[id].X != 1 {
		t.Fatal("reconnect lost state")
	}
	// Retry sequence 1, then sequence 2: duplicate movement cannot be applied twice.
	_ = resumed.encoder.Encode(Message{Type: "move", Seq: 1, DX: 1})
	_ = resumed.encoder.Encode(Message{Type: "move", Seq: 2, DY: 1})
	final := snapshot(t, resumed, func(m Message) bool { return m.Players[id].Seq == 2 })
	if final.Players[id].X != 1 || final.Players[id].Y != 1 {
		t.Fatal("replay changed state")
	}
}

func TestWrongResumeTokenAndMalformedInputAreClosed(t *testing.T) {
	address, _ := launch(t)
	owner := connect(t, address, "", "")
	for _, message := range []Message{
		{Type: "join", ID: owner.welcome.ID, Token: "wrong"},
		{Type: "move", Seq: 1, DX: 1},
	} {
		conn, err := net.Dial("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(conn).Encode(message)
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		err = json.NewDecoder(conn).Decode(new(Message))
		_ = conn.Close()
		if err == nil {
			t.Fatal("invalid identity accepted")
		}
	}
}

func TestTakeoverFencesOldConnectionAndSlowQueueDoesNotBlockFrames(t *testing.T) {
	server := New(time.Millisecond)
	old, remote := net.Pipe()
	defer old.Close()
	defer remote.Close()
	current, other := net.Pipe()
	defer current.Close()
	defer other.Close()
	peer := &session{connection: current, out: make(chan Message, 1), commands: make(chan input, 64)}
	server.sessions["player"] = peer
	peer.commands <- input{"player", old, 99, 1, 1}
	peer.commands <- input{"player", current, 1, 1, 0}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { server.frames(ctx); close(done) }()
	initial := <-peer.out
	if initial.Players["player"].Seq != 1 || initial.Players["player"].Y != 0 {
		t.Fatal("old connection mutated state")
	}
	// Fill consumer queue. The next tick must close just that connection.
	select {
	case peer.out <- Message{}:
	default:
	}
	_ = other.SetReadDeadline(time.Now().Add(time.Second))
	_, err := other.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("slow client not closed")
	}
	cancel()
	<-done
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.frame < 2 {
		t.Fatal("frame loop blocked by consumer")
	}
}

func TestPerConnectionQueueBudgetProtectsHealthyPlayer(t *testing.T) {
	s := New(time.Hour)
	flood, remote := net.Pipe()
	defer flood.Close()
	defer remote.Close()
	healthy, other := net.Pipe()
	defer healthy.Close()
	defer other.Close()
	a := &session{connection: flood, commands: make(chan input, 64), out: make(chan Message, 4)}
	b := &session{connection: healthy, commands: make(chan input, 64), out: make(chan Message, 4)}
	s.sessions["flood"], s.sessions["healthy"] = a, b
	for i := 1; i <= 64; i++ {
		a.commands <- input{"flood", flood, uint64(i), 1, 0}
	}
	b.commands <- input{"healthy", healthy, 1, 0, 1}
	s.interval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.frames(ctx); close(done) }()
	select {
	case m := <-b.out:
		if m.Players["healthy"].Seq != 1 || m.Players["flood"].Seq != 32 {
			t.Fatalf("unfair frame: %+v", m)
		}
	case <-time.After(time.Second):
		t.Error("frame blocked")
	}
	cancel()
	<-done
}

func TestDisconnectedIdentitiesExpireButLiveAndRetainedSessionsSurvive(t *testing.T) {
	s := NewWithRetention(time.Millisecond, time.Second)
	now := time.Now()
	for i := 0; i < 128; i++ {
		s.sessions[string(rune(i))] = &session{disconnectedAt: now.Add(-time.Second)}
	}
	s.mu.Lock()
	s.expireLocked(now)
	s.mu.Unlock()
	if len(s.sessions) != 0 {
		t.Fatal("expired identities still consume capacity")
	}
	live, remote := net.Pipe()
	defer live.Close()
	defer remote.Close()
	s.sessions["live"] = &session{connection: live, disconnectedAt: now.Add(-time.Hour)}
	s.sessions["retained"] = &session{disconnectedAt: now.Add(-500 * time.Millisecond)}
	s.expireLocked(now)
	if len(s.sessions) != 2 {
		t.Fatal("live or resumable player prematurely removed")
	}
	s.expireLocked(now.Add(time.Second))
	if len(s.sessions) != 1 || s.sessions["live"] == nil {
		t.Fatal("retention deadline not enforced")
	}
}

func TestExpiredRoomCapacityReclaimedAndOldTokenRejected(t *testing.T) {
	address, s := launch(t)
	s.mu.Lock()
	for i := 0; i < 128; i++ {
		s.sessions[string(rune(i))] = &session{token: "expired-token", disconnectedAt: time.Now().Add(-time.Minute)}
	}
	s.mu.Unlock()
	fresh := connect(t, address, "", "")
	snapshot(t, fresh, func(m Message) bool { return len(m.Players) == 1 })
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = json.NewEncoder(conn).Encode(Message{Type: "join", ID: string(rune(1)), Token: "expired-token"})
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if err := json.NewDecoder(conn).Decode(new(Message)); err == nil {
		t.Fatal("expired token resumed")
	}
}

func TestPerIPQuotaClosesFloodAndReleasesDisconnectedSlots(t *testing.T) {
	address, s := launch(t)
	s.mu.Lock()
	s.perIP = 2
	s.mu.Unlock()
	a := connect(t, address, "", "")
	_ = connect(t, address, "", "")
	denied, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer denied.Close()
	_ = denied.SetReadDeadline(time.Now().Add(time.Second))
	if err = json.NewDecoder(denied).Decode(new(Message)); err == nil {
		t.Fatal("IP quota bypassed")
	}
	_ = a.conn.Close()
	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		count := s.ipCounts["127.0.0.1"]
		s.mu.Unlock()
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("connection slot leaked")
		}
		time.Sleep(time.Millisecond)
	}
	// A fresh identity cannot bypass the quota by rapidly closing connections.
	extra, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.NewEncoder(extra).Encode(Message{Type: "join"})
	_ = extra.SetReadDeadline(time.Now().Add(time.Second))
	if err = json.NewDecoder(extra).Decode(new(Message)); err == nil {
		t.Fatal("retained identity quota bypassed")
	}
	_ = extra.Close()
	deadline = time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		count := s.ipCounts["127.0.0.1"]
		s.mu.Unlock()
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slot leaked")
		}
		time.Sleep(time.Millisecond)
	}
	// The owner can still resume the retained identity without claiming a new one.
	_ = connect(t, address, a.welcome.ID, a.welcome.Token)
}

func TestSnapshotExplicitlyMarksRetainedDisconnectAndExpiry(t *testing.T) {
	s := NewWithRetention(time.Millisecond, time.Second)
	now := time.Now()
	live, other := net.Pipe()
	defer live.Close()
	defer other.Close()
	s.sessions["live"] = &session{connection: live}
	s.sessions["gone"] = &session{disconnectedAt: now}
	first := s.snapshotLocked()
	if !first.Players["live"].Connected || first.Players["gone"].Connected {
		t.Fatal(first)
	}
	s.expireLocked(now.Add(time.Second))
	if _, exists := s.snapshotLocked().Players["gone"]; exists {
		t.Fatal("expired player retained")
	}
}
