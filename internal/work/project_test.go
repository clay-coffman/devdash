package work

import (
	"encoding/json"
	"github.com/clay-coffman/devdash/internal/collect"
	"os"
	"strings"
	"testing"
	"time"
)

func TestProjectionGolden(t *testing.T) {
	b := fixture(t)
	now := b.Sweep.At.Add(time.Minute)
	provider := Provider{}
	provider.Update(now, nil)
	agents := []collect.Agent{
		{Pane: "w1:p1", Name: "nav-build", Status: "working", Cwd: "/projects/atlas-nav", Checkout: "/projects/atlas-nav", Workspace: "w1", Group: "Atlas navigation", Context: "40%", PR: "#20 pending"},
		{Pane: "w2:p1", Name: "unregistered-builder", Status: "working", Cwd: "/projects/unregistered", Checkout: "/projects/unregistered", Workspace: "w2", Group: "Scratch task", Context: "22%"},
		{Pane: "w3:p1", Name: "research", Status: "idle", Cwd: "/scratch/notes", Workspace: "w3", Group: "Notes", Context: "10%"},
	}
	actual, err := json.Marshal(Project(b, provider, provider, agents, now))
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("testdata/work-api.json")
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(actual, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(golden, &want); err != nil {
		t.Fatal(err)
	}
	// Legacy golden predates additive routing and binding projection fields.
	var m map[string]any
	m = got.(map[string]any)
	delete(m, "route")
	delete(m, "memberships")
	actual, _ = json.Marshal(m)
	golden, _ = json.Marshal(want)
	if strings.TrimSpace(string(actual)) != strings.TrimSpace(string(golden)) {
		t.Fatal("work API contract differs from synthetic golden fixture")
	}
}

func TestProjectIdentityAndFreshness(t *testing.T) {
	b := fixture(t)
	now := b.Sweep.At.Add(time.Minute)
	bp := Provider{}
	bp.Update(now, nil)
	hp := bp
	agents := []collect.Agent{{Pane: "w1:p1", Name: "duplicate", Checkout: "/projects/atlas-nav", Cwd: "/projects/atlas-nav"}, {Pane: "w9:p1", Name: "duplicate", Checkout: "/other/atlas-nav", Cwd: "/other/atlas-nav"}, {Pane: "w9:p2", Cwd: "/scratch", Workspace: "w9", Status: "working"}}
	p := Project(b, bp, hp, agents, now)
	if len(p.Streams[0].Derived.Tasks[0].LivePanes) != 1 || len(p.Activity) != 2 {
		t.Fatalf("exact joins %+v", p)
	}
	if !p.SweepAt.Equal(b.Sweep.At) || !p.BoardAgents[0].Meta.At.Equal(b.Agents[0].Meta.At) {
		t.Fatal("re-read freshened observations")
	}
	// Cache re-reads are not new sweeps, so stale declarations never hide live agents.
	bp.Update(now.Add(time.Hour), nil)
	hp.Update(now.Add(time.Hour), nil)
	p = Project(b, bp, hp, agents, now.Add(time.Hour))
	if !p.Board.Stale || len(p.Activity) != 3 {
		t.Fatal("stale mapping hid agents")
	}
	bp.Update(now, nil)
	bp.State = "error"
	p = Project(b, bp, hp, agents, now)
	if len(p.Activity) != 3 {
		t.Fatal("failed board hid fallback")
	}
	// Provider-free and truly empty states are well formed.
	p = Project(Board{}, Provider{}, Provider{}, nil, now)
	if len(p.Streams) != 0 || p.Agents == nil || p.Activity == nil {
		t.Fatal("empty state")
	}
}
func TestAmbiguousMappingsAndScopedTasks(t *testing.T) {
	b := fixture(t)
	now := b.Sweep.At
	bp := Provider{}
	bp.Update(now, nil)
	s := b.Streams[0]
	s.Key = "other-stream"
	s.ID = "other"
	s.Repo = "/projects/beacon"
	d := *s.Derived
	d.Tasks = append([]Task(nil), d.Tasks...)
	d.Tasks[0].Key = "other-task"
	s.Derived = &d
	b.Streams = append(b.Streams, s)
	p := Project(b, bp, bp, []collect.Agent{{Pane: "w1:p1", Checkout: "/projects/atlas-nav"}}, now)
	if len(p.Activity) != 1 {
		t.Fatal("ambiguous checkout must remain unmatched")
	}
	if len(b.Streams[0].Derived.Tasks[0].LivePanes) != 0 {
		t.Fatal("projection mutated cache")
	}
	raw := `{"workstreams":[{"id":"one","name":"One","repo":"/a","derived":{"version":1,"tasks":[{"id":"SAME"}]}},{"id":"one","name":"One","repo":"/b","derived":{"version":1,"tasks":[{"id":"SAME"}]}}],"agents":[],"sweep":{}}`
	parsed, err := ParseBoard(strings.NewReader(raw))
	if err != nil || parsed.Streams[0].Derived.Tasks[0].Key == parsed.Streams[1].Derived.Tasks[0].Key {
		t.Fatal("task keys must be scoped", err)
	}
}
func TestProjectionPrivateFieldsAndLegacyPane(t *testing.T) {
	b := fixture(t)
	now := b.Sweep.At
	bp := Provider{}
	bp.Update(now, nil)
	b.Streams[1].Panes = []string{"w1:p1"}
	// A pane may not claim two streams: ambiguity falls back without duplication.
	p := Project(b, bp, bp, []collect.Agent{{Pane: "w1:p1", Checkout: "/projects/atlas-nav"}}, now)
	if len(p.Activity) != 1 {
		t.Fatal("ambiguous pane")
	}
	b.Streams = b.Streams[1:2]
	p = Project(b, bp, bp, []collect.Agent{{Pane: "w1:p1", Checkout: "/projects/atlas-nav"}}, now)
	if len(p.Activity) != 0 || len(p.Streams[0].LivePanes) != 1 {
		t.Fatal("fresh exact legacy pane")
	}
	p = Project(b, bp, bp, []collect.Agent{{Pane: "w1:p1", Checkout: "/somewhere/else"}}, now)
	if len(p.Activity) != 1 {
		t.Fatal("reused pane mapping")
	}
	encoded, _ := json.Marshal(p)
	if strings.Contains(string(encoded), "PRIVATE") {
		t.Fatal("privacy")
	}
}
