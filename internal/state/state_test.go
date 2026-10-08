package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/clay-coffman/devdash/internal/collect"
)

func open(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

const home = "/home/clay-coffman"

// Worktree roots on the devbox when the fixtures were captured.
var roots = map[string]bool{
	home + "/Dev/carepilot/repos/ehr":                                   true,
	home + "/.herdr/worktrees/ehr/ibx-e2e-clay":                         true,
	home + "/.herdr/worktrees/ehr/feat-iam-staff-invitations-t02a":      true,
	home + "/.herdr/worktrees/ehr/feat-iam-staff-invitations-t02b":      true,
	home + "/.herdr/worktrees/ehr/feat-admin-ui-audit-operator-console": true,
	home + "/.herdr/worktrees/ehr/chore-customer-onboarding-plan":       true,
	home + "/.herdr/worktrees/ehr/feat-ibx-inbox-operational-spine":     true,
	home + "/.herdr/worktrees/ehr/feat-lum-1818-reseed-rehearse-undo":   true,
	home + "/.herdr/worktrees/ehr/feat-patient-messaging-rail":          true,
}

func fixtureCollector(t *testing.T) *Collector {
	c := New("tok")
	c.home = home
	c.git.IsRoot = func(d string) bool { return roots[d] }
	c.git.BranchOf = func(top string) string { return "branch-of-" + filepath.Base(top) }
	ps := collect.ParseDockerPS(open(t, "docker-ps.tsv"))
	collect.MergeStats(ps, collect.ParseDockerStats(open(t, "docker-stats.jsonl")))
	c.containers, c.dockerOK = ps, true
	var err error
	if c.agents, err = collect.ParseHerdrAgents(open(t, "herdr-agents.json")); err != nil {
		t.Fatal(err)
	}
	c.classes, c.volumeOnly = collect.ParseReaper(open(t, "reaper.txt"))
	c.volumes = map[string]int{"cp-onb-lead": 3, "carepilot-ips-lead": 2}
	c.mem, _ = collect.ParseMeminfo(open(t, "meminfo"))
	// Two synthetic processes: a vite server in a worktree and a stray python.
	c.procs = []collect.Process{
		{PID: 10, Name: "node", Cwd: home + "/.herdr/worktrees/ehr/ibx-e2e-clay/apps/staff-web", RSS: 500 << 20, Ports: []int{5273}},
		{PID: 11, Name: "pi", Cwd: home + "/.herdr/worktrees/ehr/ibx-e2e-clay", RSS: 400 << 20},
		{PID: 12, Name: "python3", Cwd: home + "/show-me", RSS: 10 << 20, Ports: []int{8765}},
	}
	return c
}

func find(s State, suffix string) *Checkout {
	for i := range s.Checkouts {
		if strings.HasSuffix(s.Checkouts[i].Path, suffix) {
			return &s.Checkouts[i]
		}
	}
	return nil
}

func TestSnapshotAttribution(t *testing.T) {
	s := fixtureCollector(t).Snapshot()
	if s.Token != "tok" || !s.NoSwap || s.Mem.Total == 0 {
		t.Errorf("header: token=%q noswap=%v total=%d", s.Token, s.NoSwap, s.Mem.Total)
	}
	ibx := find(s, "/ibx-e2e-clay")
	if ibx == nil {
		t.Fatal("no ibx-e2e-clay checkout")
	}
	if ibx.Display != "~/.herdr/worktrees/ehr/ibx-e2e-clay" || ibx.Branch != "branch-of-ibx-e2e-clay" {
		t.Errorf("display/branch = %q %q", ibx.Display, ibx.Branch)
	}
	if len(ibx.Processes) != 2 || ibx.ProcBytes != 900<<20 {
		t.Errorf("processes = %d, bytes = %d", len(ibx.Processes), ibx.ProcBytes)
	}
	if len(ibx.Stacks) != 1 || ibx.Stacks[0].Project != "carepilot-dev-ibx-e2e" || ibx.Stacks[0].Class != "live" {
		t.Fatalf("stacks = %+v", ibx.Stacks)
	}
	if ibx.Stacks[0].Bytes == 0 || ibx.ContainerBytes != ibx.Stacks[0].Bytes || ibx.TotalBytes != ibx.ProcBytes+ibx.ContainerBytes {
		t.Errorf("bytes: stack=%d container=%d total=%d", ibx.Stacks[0].Bytes, ibx.ContainerBytes, ibx.TotalBytes)
	}
	// Containers in a stack are sorted by memory; FHIR is the big one.
	if !strings.HasSuffix(ibx.Stacks[0].Containers[0].Name, "-fhir-1") {
		t.Errorf("largest container = %s", ibx.Stacks[0].Containers[0].Name)
	}
	// The ehr main checkout has an agent and the carepilot-dev stack.
	ehr := find(s, "/Dev/carepilot/repos/ehr")
	if ehr == nil || len(ehr.Agents) == 0 || len(ehr.Stacks) != 1 || ehr.Stacks[0].Project != "carepilot-dev" {
		t.Errorf("ehr = %+v", ehr)
	}
	// Stray python lands in Unattributed, with its port in the flat list.
	if len(s.Unattributed.Processes) != 1 || s.Unattributed.Processes[0].Name != "python3" {
		t.Errorf("unattributed = %+v", s.Unattributed.Processes)
	}
	if len(s.Ports) != 2 || s.Ports[0].Port != 5273 || s.Ports[1].Cwd != "~/show-me" {
		t.Errorf("ports = %+v", s.Ports)
	}
	// Sorted by total memory, descending.
	for i := 1; i < len(s.Checkouts); i++ {
		if s.Checkouts[i].TotalBytes > s.Checkouts[i-1].TotalBytes {
			t.Errorf("checkouts not sorted at %d", i)
		}
	}
	if s.Counts.Agents < 10 || s.Counts.Stacks != 9 || s.Counts.Containers != 65 || s.Buckets.Containers == 0 {
		t.Errorf("counts = %+v buckets = %+v", s.Counts, s.Buckets)
	}
	if len(s.Docker.VolumeOnly) != 11 || s.Docker.VolumeOnly[1].Volumes != 2 {
		t.Errorf("volume-only = %+v", s.Docker.VolumeOnly)
	}
}

func TestClassFallback(t *testing.T) {
	c := fixtureCollector(t)
	if got := c.Class("carepilot-dev", ""); got != "live" {
		t.Errorf("reaper class = %q", got)
	}
	if got := c.Class("coolify-proxy", "/data/coolify/proxy"); got != "foreign" {
		t.Errorf("outside home = %q, want foreign", got)
	}
	if got := c.Class("new-stack", home+"/Dev/x"); got != "unknown" {
		t.Errorf("inside home, unclassified = %q, want unknown", got)
	}
}

func TestSnapshotOmitsReclaimRecommendations(t *testing.T) {
	b, err := json.Marshal(fixtureCollector(t).Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	// Covers the top-level candidate list and per-checkout verdicts.
	if strings.Contains(string(b), `"reclaim":`) {
		t.Fatal("snapshot still exposes reclaim recommendations")
	}
}
