package room

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestIndependentIdentityQuota(t *testing.T) {
	cfg := DefaultConfig(time.Millisecond)
	cfg.PerIPConnections = 10
	cfg.PerIPIdentities = 2
	cfg.MaxIdentities = 3
	s, err := NewConfigured(cfg)
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, l) }()
	defer func() { cancel(); <-done }()
	var clients []net.Conn
	defer func() {
		for _, c := range clients {
			c.Close()
		}
	}()
	for i := 0; i < 3; i++ {
		c, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, c)
		c.SetDeadline(time.Now().Add(time.Second))
		if err = json.NewEncoder(c).Encode(Message{Type: "join"}); err != nil {
			t.Fatal(err)
		}
		var reply Message
		err = json.NewDecoder(c).Decode(&reply)
		if i < 2 && (err != nil || reply.Type != "welcome") {
			t.Fatalf("%d %v %+v", i, err, reply)
		}
		if i == 2 && err == nil {
			t.Fatal("identity quota ignored")
		}
	}
}
func TestConfigDefaultsAndValidation(t *testing.T) {
	c := DefaultConfig(time.Millisecond)
	if c.MaxIdentities != 128 || c.PerIPConnections != 16 || c.PerIPIdentities != 16 || c.MaxConnections != 256 || c.Retention != 30*time.Second {
		t.Fatal(c)
	}
	c.MaxIdentities = 0
	if _, err := NewConfigured(c); err == nil {
		t.Fatal("zero identities accepted")
	}
}
