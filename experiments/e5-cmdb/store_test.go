package cmdb

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func create(s *Store, id string) error {
	_, err := s.Apply(Change{Expected: s.Current().Revision, Actor: "operator", Reason: "inventory", Operation: "create", Item: Item{ID: id, Kind: "service", Name: id, Attributes: map[string]string{"owner": "ops"}}})
	return err
}
func TestDurabilityHistoryAndIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmdb.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err = create(s, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range []Relation{{"a", "b", "depends_on"}, {"b", "c", "depends_on"}, {"c", "a", "depends_on"}} {
		if _, err = s.Apply(Change{Expected: s.Current().Revision, Actor: "ops", Reason: "dependency", Operation: "link", Relation: r}); err != nil {
			t.Fatal(err)
		}
	}
	g, err := GraphAt(s.Current(), "a", 10, "out")
	if err != nil || len(g.Items) != 3 || len(g.Relations) != 3 {
		t.Fatalf("cycle graph %+v %v", g, err)
	}
	initial, _ := s.Snapshot(1)
	if len(initial.Items) != 1 {
		t.Fatal(initial)
	}
	copy := s.Current()
	copy.Items["a"].Attributes["owner"] = "poison"
	delete(copy.Items, "b")
	if s.Current().Items["a"].Attributes["owner"] != "ops" || len(s.Current().Items) != 3 {
		t.Fatal("alias")
	}
	reopened, err := Open(path)
	if err != nil || reopened.Current().Revision != 6 {
		t.Fatalf("reopen %v", err)
	}
	audit, _ := reopened.Audits(3, 2)
	if len(audit) != 2 || audit[0].Before.Revision != 3 || audit[0].After.Revision != 4 {
		t.Fatal(audit)
	}
	if _, err = s.Apply(Change{Expected: 6, Actor: "ops", Reason: "remove", Operation: "delete", Item: Item{ID: "a"}}); err == nil {
		t.Fatal("dangling relation")
	}
	if _, err = s.Apply(Change{Expected: 0, Actor: "ops", Reason: "stale", Operation: "create"}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
func TestPersistenceFailureNoCommit(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "missing", "cmdb.json"))
	if err := create(s, "a"); err == nil {
		t.Fatal("write unexpectedly succeeded")
	}
	if s.Current().Revision != 0 {
		t.Fatal("partial state")
	}
	a, _ := s.Audits(0, 100)
	if len(a) != 0 {
		t.Fatal("partial audit")
	}
}
func TestConcurrentCAS(t *testing.T) {
	s, _ := Open("")
	var wg sync.WaitGroup
	var successes int
	var mu sync.Mutex
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Apply(Change{Expected: 0, Actor: "ops", Reason: "race", Operation: "create", Item: Item{ID: "one", Kind: "host", Name: "host"}})
			if err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if successes != 1 || s.Current().Revision != 1 {
		t.Fatal(successes)
	}
}
func TestHTTPAndHistoricalGraph(t *testing.T) {
	s, _ := Open("")
	h := Handler(s)
	body := `{"expected_revision":0,"actor":"ops","reason":"boot","operation":"create","item":{"id":"a","kind":"service","name":"API"}}`
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/changes", strings.NewReader(body)))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/changes", strings.NewReader(body)))
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/graph?root=a&depth=0&direction=out&revision=1", nil))
	var graph Graph
	if json.Unmarshal(w.Body.Bytes(), &graph) != nil || len(graph.Items) != 1 {
		t.Fatal(w.Body.String())
	}
	for _, path := range []string{"/audit?limit=0", "/state?revision=-1", "/graph?root=a&depth=50&direction=out"} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 400 {
			t.Fatal(path, w.Code)
		}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/changes", strings.NewReader(body+` {}`)))
	if w.Code != 400 {
		t.Fatal("trailing payload")
	}
}
func TestUpdateUnlinkDeleteAndReverse(t *testing.T) {
	s, _ := Open("")
	create(s, "a")
	create(s, "b")
	r := Relation{"a", "b", "runs_on"}
	s.Apply(Change{2, "ops", "link", "link", Item{}, r})
	g, err := GraphAt(s.Current(), "b", 1, "in")
	if err != nil || len(g.Items) != 2 {
		t.Fatal(g, err)
	}
	state, err := s.Apply(Change{3, "ops", "rename", "update", Item{ID: "a", Kind: "service", Name: "new"}, Relation{}})
	if err != nil || state.Items["a"].Name != "new" {
		t.Fatal(err)
	}
	if _, err = s.Apply(Change{4, "ops", "unlink", "unlink", Item{}, r}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(Change{5, "ops", "retire", "delete", Item{ID: "a"}, Relation{}}); err != nil {
		t.Fatal(err)
	}
	if len(s.Current().Items) != 1 {
		t.Fatal("delete")
	}
	old, _ := s.Snapshot(3)
	if len(old.Items) != 2 || len(old.Relations) != 1 {
		t.Fatal("history overwritten")
	}
}
