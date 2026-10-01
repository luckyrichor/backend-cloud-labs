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
	peer := &session{connection: current, out: make(chan Message, 1)}
	server.sessions["player"] = peer
	server.commands <- input{"player", old, 99, 1, 1}
	server.commands <- input{"player", current, 1, 1, 0}
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
