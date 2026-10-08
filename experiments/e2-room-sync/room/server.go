// Package room implements a single authoritative room over newline-delimited TCP JSON.
package room

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"sync"
	"time"
)

type Player struct {
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
	Seq uint64  `json:"seq"`
}
type Message struct {
	Type    string            `json:"type"`
	ID      string            `json:"id,omitempty"`
	Token   string            `json:"token,omitempty"`
	Frame   uint64            `json:"frame,omitempty"`
	Players map[string]Player `json:"players,omitempty"`
	Seq     uint64            `json:"seq,omitempty"`
	DX      float64           `json:"dx,omitempty"`
	DY      float64           `json:"dy,omitempty"`
}
type session struct {
	token          string
	player         Player
	connection     net.Conn
	out            chan Message
	commands       chan input
	disconnectedAt time.Time
}
type input struct {
	id         string
	connection net.Conn
	seq        uint64
	dx, dy     float64
}
type Server struct {
	mu          sync.Mutex
	sessions    map[string]*session
	connections map[net.Conn]bool
	retention   time.Duration
	frame       uint64
	interval    time.Duration
	wg          sync.WaitGroup
}

func New(interval time.Duration) *Server { return NewWithRetention(interval, 30*time.Second) }

// Retention bounds reconnect identities; expiration invalidates resume credentials.
func NewWithRetention(interval, retention time.Duration) *Server {
	if interval <= 0 || retention <= 0 {
		panic("frame interval and retention must be positive")
	}
	return &Server{sessions: make(map[string]*session), connections: make(map[net.Conn]bool),
		retention: retention, interval: interval}
}
func randomToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// Serve owns listener lifetime. Cancellation closes all clients and joins every worker.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopped := make(chan struct{})
	go func() { <-ctx.Done(); _ = listener.Close() }()
	go func() { defer close(stopped); s.frames(ctx) }()
	defer func() {
		cancel()
		<-stopped
		s.mu.Lock()
		for c := range s.connections {
			_ = c.Close()
		}
		s.mu.Unlock()
		s.wg.Wait()
	}()
	for {
		c, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		s.mu.Lock()
		s.connections[c] = true
		s.mu.Unlock()
		s.wg.Add(1)
		go func() { defer s.wg.Done(); s.handle(ctx, c) }()
	}
}

func read(scanner *bufio.Scanner) (Message, error) {
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return Message{}, err
		}
		return Message{}, io.EOF
	}
	var message Message
	decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&message); err != nil {
		return message, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return message, errors.New("trailing JSON")
	}
	return message, nil
}

func (s *Server) handle(ctx context.Context, c net.Conn) {
	defer func() { _ = c.Close(); s.mu.Lock(); delete(s.connections, c); s.mu.Unlock() }()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	scanner := bufio.NewScanner(c) // 64 KiB maximum record; no unbounded input allocation.
	hello, err := read(scanner)
	if err != nil || hello.Type != "join" {
		return
	}
	s.mu.Lock()
	s.expireLocked(time.Now())
	id := hello.ID
	peer := s.sessions[id]
	if id != "" {
		if peer == nil || subtle.ConstantTimeCompare([]byte(peer.token), []byte(hello.Token)) != 1 {
			s.mu.Unlock()
			return
		}
		if peer.connection != nil {
			_ = peer.connection.Close()
		}
	} else {
		if len(s.sessions) >= 128 {
			s.mu.Unlock()
			return
		} // Connected plus unexpired disconnected identities are bounded.
		id = randomToken()
		peer = &session{token: randomToken()}
		s.sessions[id] = peer
	}
	out := make(chan Message, 4)
	peer.connection, peer.out = c, out
	peer.commands = make(chan input, 64)
	peer.disconnectedAt = time.Time{}
	commands := peer.commands
	out <- Message{Type: "welcome", ID: id, Token: peer.token}
	out <- s.snapshotLocked()
	s.mu.Unlock()
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		defer c.Close()
		encoder := json.NewEncoder(c)
		for {
			select {
			case message := <-out:
				_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
				if encoder.Encode(message) != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	defer func() {
		_ = c.Close()
		s.mu.Lock()
		if peer.connection == c {
			peer.connection = nil
			peer.out = nil
			peer.commands = nil
			peer.disconnectedAt = time.Now()
		}
		s.mu.Unlock()
		// Writer sees closure on next frame at worst; force wakeup without touching other sessions.
		select {
		case out <- Message{Type: "closed"}:
		default:
		}
		<-writerDone
	}()
	for {
		_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))
		command, err := read(scanner)
		if err != nil {
			return
		}
		if command.Type != "move" || command.Seq == 0 || math.IsNaN(command.DX) ||
			math.IsNaN(command.DY) || math.IsInf(command.DX, 0) || math.IsInf(command.DY, 0) ||
			math.Abs(command.DX) > 1 || math.Abs(command.DY) > 1 {
			return
		}
		select {
		case commands <- input{id, c, command.Seq, command.DX, command.DY}:
		case <-ctx.Done():
			return
		default:
			return // A flooding client cannot grow memory or block the frame loop.
		}
	}
}

func (s *Server) snapshotLocked() Message {
	players := make(map[string]Player, len(s.sessions))
	for id, peer := range s.sessions {
		players[id] = peer.player
	}
	return Message{Type: "snapshot", Frame: s.frame, Players: players}
}
func (s *Server) frames(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			s.expireLocked(time.Now())
			// Each connection gets its own bounded queue and frame budget.
			// A flood cannot consume another player's admission slots.
			for _, peer := range s.sessions {
				for count := 0; count < 32; count++ {
					select {
					case cmd := <-peer.commands:
						if peer.connection == cmd.connection && cmd.seq > peer.player.Seq {
							peer.player.X += cmd.dx
							peer.player.Y += cmd.dy
							peer.player.Seq = cmd.seq
						}
					default:
						count = 32
					}
				}
			}
			s.frame++
			snapshot := s.snapshotLocked()
			for _, peer := range s.sessions {
				if peer.connection == nil {
					continue
				}
				select {
				case peer.out <- snapshot:
				default:
					_ = peer.connection.Close()
				}
			}
			s.mu.Unlock()
		}
	}
}

// Caller holds mu. Live connections are never expired.
func (s *Server) expireLocked(now time.Time) {
	for id, peer := range s.sessions {
		if peer.connection == nil && !peer.disconnectedAt.IsZero() && now.Sub(peer.disconnectedAt) >= s.retention {
			delete(s.sessions, id)
		}
	}
}
