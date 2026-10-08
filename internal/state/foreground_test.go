package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/clay-coffman/devdash/internal/collect"
	"github.com/clay-coffman/devdash/internal/work"
)

func TestForegroundLocationDrivesWorkAndResources(t *testing.T) {
	for _, tt := range []struct {
		name           string
		foreground     string // actual, non-Git, empty, or missing
		registerActual bool
	}{
		{"different checkout stays unmatched", "actual", false},
		{"different checkout matches only its declared task", "actual", true},
		{"non-Git foreground does not acquire shell task", "non-Git", false},
		{"empty foreground uses shell checkout", "empty", false},
		{"missing foreground uses shell checkout", "missing", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			main := filepath.Join(dir, "repository")
			old := filepath.Join(dir, "old-checkout")
			actual := filepath.Join(dir, "actual-checkout")
			nonGit := filepath.Join(dir, "notes")
			// Synthetic Git worktree pointers exercise real repository resolution
			// without shelling out to Git or inventing identity from a basename.
			for _, top := range []string{old, actual} {
				admin := filepath.Join(main, ".git", "worktrees", filepath.Base(top))
				for _, p := range []string{top, admin} {
					if err := os.MkdirAll(p, 0700); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(top, ".git"), []byte("gitdir: "+admin+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(admin, "commondir"), []byte("../..\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(nonGit, 0700); err != nil {
				t.Fatal(err)
			}

			row := map[string]any{"pane_id": "w1:p1", "cwd": old, "agent_status": "working", "workspace_id": "w1"}
			wantCwd, wantCheckout, wantRepo := old, old, main
			switch tt.foreground {
			case "actual":
				row["foreground_cwd"] = actual
				wantCwd, wantCheckout = actual, actual
			case "non-Git":
				row["foreground_cwd"] = nonGit
				wantCwd, wantCheckout, wantRepo = nonGit, "", ""
			case "empty":
				row["foreground_cwd"] = ""
			}
			b, err := json.Marshal(map[string]any{"result": map[string]any{"agents": []any{row}}})
			if err != nil {
				t.Fatal(err)
			}
			c := New("synthetic-token")
			c.git.BranchOf = func(string) string { return "synthetic-branch" }
			c.readAgents = func() ([]collect.Agent, error) { return collect.ParseHerdrAgents(strings.NewReader(string(b))) }
			at := time.Now()
			tasks := []work.Task{{Key: "old-task", ID: "OLD", Checkouts: []work.Checkout{{Path: old, ObservedAt: at}}}}
			if tt.registerActual {
				tasks = append(tasks, work.Task{Key: "actual-task", ID: "ACTUAL", Checkouts: []work.Checkout{{Path: actual, ObservedAt: at}}})
			}
			board := work.Board{Streams: []work.Stream{{Key: "stream", Repo: main, Derived: &work.Derived{Version: 1, RuntimeKnown: true, RuntimeAt: at, FactMaxAge: "5m", Tasks: tasks}}}}
			board.Sweep.At = at
			c.readBoard = func() (work.Board, error) { return board, nil }
			c.refreshBoard()
			c.refreshHerdr()

			p := c.WorkSnapshot()
			if len(p.Agents) != 1 || p.Agents[0].Cwd != wantCwd || p.Agents[0].Checkout != wantCheckout || p.Agents[0].Repository != wantRepo {
				t.Fatalf("observed effective identity: %+v", p.Agents)
			}
			oldPanes := p.Streams[0].Derived.Tasks[0].LivePanes
			wantOld := wantCheckout == old
			if (len(oldPanes) == 1) != wantOld {
				t.Fatalf("shell task panes = %v, want membership %v", oldPanes, wantOld)
			}
			if tt.registerActual && len(p.Streams[0].Derived.Tasks[1].LivePanes) != 1 {
				t.Fatal("effective checkout's explicit task was not matched")
			}
			wantUnmatched := !wantOld && !tt.registerActual
			if (len(p.Activity) == 1) != wantUnmatched {
				t.Fatalf("fallback groups = %+v, want unmatched %v", p.Activity, wantUnmatched)
			}
			if wantUnmatched && (p.Activity[0].Cwd != wantCwd || p.Activity[0].Checkout != wantCheckout || p.Activity[0].Repository != wantRepo) {
				t.Fatalf("wrong fallback identity: %+v", p.Activity[0])
			}

			resources := c.Snapshot()
			if wantCheckout == "" {
				if len(resources.Checkouts) != 0 || len(resources.Unattributed.Agents) != 1 || resources.Unattributed.Agents[0].Cwd != wantCwd {
					t.Fatalf("non-Git foreground resources: %+v", resources)
				}
			} else if len(resources.Checkouts) != 1 || resources.Checkouts[0].Path != wantCheckout || len(resources.Checkouts[0].Agents) != 1 || len(resources.Unattributed.Agents) != 0 {
				t.Fatalf("wrong resource checkout: %+v", resources.Checkouts)
			}

			// Without board data the very same effective identity drives fallback.
			c.readBoard = func() (work.Board, error) { return work.Board{}, nil }
			c.refreshBoard()
			fallback := c.WorkSnapshot().Activity
			if len(fallback) != 1 || fallback[0].Cwd != wantCwd || fallback[0].Checkout != wantCheckout || fallback[0].Repository != wantRepo {
				t.Fatalf("Herdr-only identity: %+v", fallback)
			}
		})
	}
}
