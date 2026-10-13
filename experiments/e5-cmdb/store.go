package cmdb

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var ErrConflict = errors.New("REVISION_CONFLICT")

type Item struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	Name       string            `json:"name"`
	Attributes map[string]string `json:"attributes"`
}
type Relation struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}
type State struct {
	Revision  uint64          `json:"revision"`
	Items     map[string]Item `json:"items"`
	Relations []Relation      `json:"relations"`
}
type Change struct {
	Expected  uint64   `json:"expected_revision"`
	Actor     string   `json:"actor"`
	Reason    string   `json:"reason"`
	Operation string   `json:"operation"`
	Item      Item     `json:"item"`
	Relation  Relation `json:"relation"`
}
type Audit struct {
	Revision uint64    `json:"revision"`
	At       time.Time `json:"at"`
	Change   Change    `json:"change"`
	Before   State     `json:"before"`
	After    State     `json:"after"`
}
type disk struct {
	State   State   `json:"state"`
	History []Audit `json:"history"`
}
type Store struct {
	mu   sync.RWMutex
	path string
	data disk
}

func empty() State { return State{Items: map[string]Item{}, Relations: []Relation{}} }
func clone[T any](v T) T {
	data, _ := json.Marshal(v)
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		panic(err)
	}
	return out
}
func Open(path string) (*Store, error) {
	s := &Store{path: path, data: disk{State: empty(), History: []Audit{}}}
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, &s.data); err != nil {
		return nil, err
	}
	previous := empty()
	for i, a := range s.data.History {
		if a.Revision != uint64(i+1) || a.After.Revision != a.Revision || !equal(previous, a.Before) {
			return nil, errors.New("invalid audit chain")
		}
		previous = a.After
	}
	if !equal(previous, s.data.State) {
		return nil, errors.New("state/audit mismatch")
	}
	return s, nil
}
func equal(a, b State) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func (s *Store) persist(data disk) error {
	if s.path == "" {
		return nil
	}
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	f, err := os.CreateTemp(dir, ".cmdb-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}
func (s *Store) Apply(c Change) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.Expected != s.data.State.Revision {
		return State{}, ErrConflict
	}
	if c.Actor == "" || c.Reason == "" || len(c.Actor) > 128 || len(c.Reason) > 1024 {
		return State{}, errors.New("actor/reason required")
	}
	next := clone(s.data.State)
	switch c.Operation {
	case "create", "update":
		if c.Item.ID == "" || c.Item.Name == "" || (c.Item.Kind != "service" && c.Item.Kind != "host" && c.Item.Kind != "database") {
			return State{}, errors.New("invalid item")
		}
		_, exists := next.Items[c.Item.ID]
		if (c.Operation == "create" && exists) || (c.Operation == "update" && !exists) {
			return State{}, errors.New("item existence conflict")
		}
		next.Items[c.Item.ID] = clone(c.Item)
	case "delete":
		if _, exists := next.Items[c.Item.ID]; !exists {
			return State{}, errors.New("item missing")
		}
		for _, r := range next.Relations {
			if r.From == c.Item.ID || r.To == c.Item.ID {
				return State{}, errors.New("item has relations")
			}
		}
		delete(next.Items, c.Item.ID)
	case "link", "unlink":
		r := c.Relation
		if r.From == r.To || r.Kind == "" {
			return State{}, errors.New("invalid relation")
		}
		if _, ok := next.Items[r.From]; !ok {
			return State{}, errors.New("source missing")
		}
		if _, ok := next.Items[r.To]; !ok {
			return State{}, errors.New("target missing")
		}
		found := -1
		for i, x := range next.Relations {
			if x == r {
				found = i
				break
			}
		}
		if c.Operation == "link" {
			if found >= 0 {
				return State{}, errors.New("relation exists")
			}
			next.Relations = append(next.Relations, r)
		} else {
			if found < 0 {
				return State{}, errors.New("relation missing")
			}
			next.Relations = append(next.Relations[:found], next.Relations[found+1:]...)
		}
	default:
		return State{}, errors.New("invalid operation")
	}
	next.Revision++
	data := clone(s.data)
	data.State = next
	data.History = append(data.History, Audit{next.Revision, time.Now().UTC(), clone(c), clone(s.data.State), clone(next)})
	if err := s.persist(data); err != nil {
		return State{}, err
	}
	s.data = data
	return clone(next), nil
}
func (s *Store) Snapshot(rev uint64) (State, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if rev == 0 {
		return empty(), nil
	}
	if rev > s.data.State.Revision {
		return State{}, errors.New("unknown revision")
	}
	return clone(s.data.History[rev-1].After), nil
}
func (s *Store) Current() State { s.mu.RLock(); defer s.mu.RUnlock(); return clone(s.data.State) }
func (s *Store) Audits(offset, limit int) ([]Audit, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if offset < 0 || limit < 1 || limit > 100 {
		return nil, errors.New("invalid pagination")
	}
	end := offset + limit
	if end > len(s.data.History) {
		end = len(s.data.History)
	}
	if offset > end {
		return []Audit{}, nil
	}
	return clone(s.data.History[offset:end]), nil
}

type Graph struct {
	Revision  uint64     `json:"revision"`
	Items     []Item     `json:"items"`
	Relations []Relation `json:"relations"`
}

func GraphAt(state State, root string, depth int, direction string) (Graph, error) {
	if depth < 0 || depth > 10 || (direction != "out" && direction != "in" && direction != "both") {
		return Graph{}, errors.New("invalid traversal")
	}
	if _, ok := state.Items[root]; !ok {
		return Graph{}, fmt.Errorf("root missing")
	}
	seen := map[string]bool{root: true}
	front := []string{root}
	for level := 0; level < depth; level++ {
		next := []string{}
		for _, id := range front {
			for _, r := range state.Relations {
				target := ""
				if r.From == id && direction != "in" {
					target = r.To
				}
				if r.To == id && direction != "out" {
					target = r.From
				}
				if target != "" && !seen[target] {
					seen[target] = true
					next = append(next, target)
				}
			}
		}
		front = next
	}
	g := Graph{Revision: state.Revision, Items: []Item{}, Relations: []Relation{}}
	ids := []string{}
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		g.Items = append(g.Items, clone(state.Items[id]))
	}
	for _, r := range state.Relations {
		if seen[r.From] && seen[r.To] {
			g.Relations = append(g.Relations, r)
		}
	}
	sort.Slice(g.Relations, func(i, j int) bool {
		a, b := g.Relations[i], g.Relations[j]
		return a.From+"\x00"+a.To+"\x00"+a.Kind < b.From+"\x00"+b.To+"\x00"+b.Kind
	})
	return g, nil
}
