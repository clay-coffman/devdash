package collect

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBoundedCommand(t *testing.T) {
	if _, err := RunBounded(100*time.Millisecond, 1024, "sh", "-c", "exec sleep 5"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	if _, err := RunBounded(5*time.Second, 10, "sh", "-c", "printf 12345678901"); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("limit: %v", err)
	}
	if _, err := RunBounded(5*time.Second, 100, "sh", "-c", "printf PRIVATE >&2; exit 2"); err == nil || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatalf("failure leaked stderr: %v", err)
	}
	// Service process environment passes through untouched to both providers.
	t.Setenv("HERDR_SOCKET_PATH", "/tmp/synthetic-work-socket")
	dir := t.TempDir()
	for _, tool := range []string{"herdr", "herdr-board"} {
		if err := os.WriteFile(filepath.Join(dir, tool), []byte("#!/bin/sh\nprintf '%s|%s' \"$HERDR_SOCKET_PATH\" \"$*\"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, tt := range []struct {
		name string
		args []string
	}{{"herdr", []string{"agent", "list"}}, {"herdr-board", []string{"json", "--no-sweep"}}} {
		b, err := RunBounded(time.Second, 1024, tt.name, tt.args...)
		if err != nil || string(b) != "/tmp/synthetic-work-socket|"+strings.Join(tt.args, " ") {
			t.Fatalf("environment: %s %v", b, err)
		}
	}
}
func TestGitCanonicalRepository(t *testing.T) {
	dir := t.TempDir()
	main, lane := filepath.Join(dir, "main"), filepath.Join(dir, "lane")
	admin := filepath.Join(main, ".git", "worktrees", "lane")
	if err := os.MkdirAll(admin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(lane, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lane, ".git"), []byte("gitdir: "+admin+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(admin, "commondir"), []byte("../..\n"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(lane, alias); err != nil {
		t.Fatal(err)
	}
	canonicalLane, _ := filepath.EvalSymlinks(lane)
	canonicalMain, _ := filepath.EvalSymlinks(main)
	g := NewGit()
	if g.Toplevel(alias) != canonicalLane || g.Repository(canonicalLane) != canonicalMain || g.Repository(canonicalMain) != canonicalMain || g.Repository(dir) != "" {
		t.Fatal("canonical identity / common directory")
	}
}

func TestHerdrIdentityAndErrors(t *testing.T) {
	s := `{"result":{"agents":[{"agent":"pi","pane_id":"w1:p1","cwd":"/projects/not-git","workspace_id":"w1","agent_status":"working","tokens":{"title":"Same label","context":"20%","provider":"example"},"agent_session":{"value":"PRIVATE"}},{"pane_id":"w1:p2","tokens":{"title":"Same label"}}]}}`
	a, err := ParseHerdrAgents(strings.NewReader(s))
	if err != nil || len(a) != 2 || a[0].Name != "Same label" || a[0].Pane == a[1].Pane || a[0].Kind != "pi" {
		t.Fatalf("agents: %+v %v", a, err)
	}
	b, _ := json.Marshal(a)
	if strings.Contains(string(b), "PRIVATE") {
		t.Fatal("leaked session")
	}
	a, err = ParseHerdrAgents(strings.NewReader(`{"result":{"agents":[]}}`))
	if err != nil || len(a) != 0 {
		t.Fatal("empty success")
	}
	for _, s := range []string{`{}`, `{"error":{"message":"stopped"}}`, `{"result":{}}`, `{"result":{"agents":[]},"error":{"message":"failure"}}`, `{"result":{"agents":[{}]}}`, `{"result":{"agents":[{"pane_id":"p"},{"pane_id":"p"}]}}`, `{"result":{"agents":[]}} {}`} {
		if _, err := ParseHerdrAgents(strings.NewReader(s)); err == nil {
			t.Fatalf("accepted %s", s)
		}
	}
}
