package state

import (
	"errors"
	"github.com/clay-coffman/devdash/internal/collect"
	"github.com/clay-coffman/devdash/internal/work"
	"os/exec"
	"sync"
	"testing"
	"time"
)

func TestWorkRefreshRecovery(t *testing.T) {
	c := New("token")
	c.git.IsRoot = func(s string) bool { return s == "/projects/atlas" }
	source := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	b := work.Board{Streams: []work.Stream{{Key: "stream", Name: "Atlas"}}}
	b.Sweep.At = source
	c.readBoard = func() (work.Board, error) { return b, nil }
	c.refreshBoard()
	c.readAgents = func() ([]collect.Agent, error) {
		return []collect.Agent{{Pane: "p1", Cwd: "/projects/atlas/src"}, {Pane: "p2", Cwd: "/scratch"}}, nil
	}
	c.refreshHerdr()
	p := c.WorkSnapshot()
	readAt := p.Board.LastSuccess
	if !p.SweepAt.Equal(source) || p.Agents[0].Checkout != "/projects/atlas" || p.Agents[1].Checkout != "" {
		t.Fatal("identity/timestamp")
	}
	c.readBoard = func() (work.Board, error) { return work.Board{}, errors.New("synthetic failure") }
	c.refreshBoard()
	c.readAgents = func() ([]collect.Agent, error) { return nil, exec.ErrNotFound }
	c.refreshHerdr()
	p = c.WorkSnapshot()
	if len(p.Streams) != 1 {
		t.Fatal("retain last good")
	}
	if !p.Board.LastSuccess.Equal(readAt) || p.Board.State != "error" || p.Herdr.Available || !p.Herdr.HasData || len(p.Agents) != 2 {
		t.Fatal("failure status")
	}
	c.readBoard = func() (work.Board, error) { return work.Board{}, nil }
	c.refreshBoard()
	c.readAgents = func() ([]collect.Agent, error) { return []collect.Agent{}, nil }
	c.refreshHerdr()
	p = c.WorkSnapshot()
	if len(p.Streams) != 0 || len(p.Agents) != 0 || p.Board.State != "ready" || p.Herdr.State != "ready" {
		t.Fatal("empty recovery")
	}
}
func TestSlowBoardDoesNotBlockSnapshot(t *testing.T) {
	c := New("token")
	started, release := make(chan struct{}), make(chan struct{})
	c.readBoard = func() (work.Board, error) { close(started); <-release; return work.Board{}, nil }
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); c.refreshBoard() }()
	<-started
	done := make(chan struct{})
	go func() { c.WorkSnapshot(); c.Snapshot(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		close(release)
		wg.Wait()
		t.Fatal("snapshot blocked on board")
	}
	close(release)
	wg.Wait()
}
