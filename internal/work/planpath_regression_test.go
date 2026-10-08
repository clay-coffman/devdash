package work

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/clay-coffman/devdash/internal/collect"
)

// These paths have the precise producer shape: ReadPlanFile shortens both plan
// display paths, while the catalog's registered_plan and plan_source stay absolute.
func planPathProducerBoard(t *testing.T) Board {
	t.Helper()
	raw := `{"server":"` + fixServer + `","workstreams":[{"id":"ws","name":"WS","repo":"/home/u/.local/share/herdr-workstreams","source":"/home/u/.local/share/herdr-workstreams/host.json","source_mode":"catalog","derived":{"version":1,"host_catalog":"/home/u/.local/share/herdr-workstreams/host.json","section":"Now","runtime_known":true,"runtime_observed_at":"2026-01-01T12:00:00Z","fact_max_age":"5m","tasks":[{"id":"T","checkouts":[{"path":"/home/u/co/lead","server":"` + fixServer + `","registered_plan":"/home/u/co/lead/.fleet/t/plan.md","agent":"lead","plan":{"path":"~/co/lead/.fleet/t/plan.md","done":3,"open":1}},{"path":"/home/u/co/lead","server":"` + fixServer + `","agent":"rev","observer_only":true}]}]}}],"agents":[{"observation":{"pane":"p1","agent_name":"lead","checkout":"/home/u/co/lead","cwd":"/home/u/co/lead","repository":"/home/u/repo","task_id":"T","workstream_id":"ws","host_catalog":"/home/u/.local/share/herdr-workstreams/host.json","role_from_name":"lead","role_source":"registered","plan_source":"registered: /home/u/co/lead/.fleet/t/plan.md","plan":{"path":"~/co/lead/.fleet/t/plan.md","done":3,"open":1}},"meta":{"collected_at":"2026-01-01T12:00:30Z"}},{"observation":{"pane":"p2","agent_name":"rev","checkout":"/home/u/co/lead","cwd":"/home/u/co/lead","repository":"/home/u/repo","task_id":"T","workstream_id":"ws","host_catalog":"/home/u/.local/share/herdr-workstreams/host.json","role_from_name":"reviewer","role_source":"registered","plan_source":"registered review assignment; no implementation plan"},"meta":{"collected_at":"2026-01-01T12:00:30Z"}}],"sweep":{"at":"2026-01-01T12:00:30Z"}}`
	b, err := ParseBoard(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func planPathAgents() []collect.Agent {
	return []collect.Agent{
		{Pane: "p1", Name: "lead", Status: "working", Checkout: "/home/u/co/lead", Cwd: "/home/u/co/lead", Repository: "/home/u/repo"},
		{Pane: "p2", Name: "rev", Status: "idle", Checkout: "/home/u/co/lead", Cwd: "/home/u/co/lead", Repository: "/home/u/repo"},
	}
}
func TestPlanPathProducerDisplayAndReviewer(t *testing.T) {
	b := planPathProducerBoard(t)
	out := ProjectWithRoute(b, fixReady(), fixReady(), planPathAgents(), fixNow(), fixRoute())
	if len(out.Memberships) != 1 || len(out.Memberships[0].Panes) != 2 || len(out.Memberships[0].BoardPanes) != 2 {
		t.Fatalf("registered owner and non-owning reviewer lost on shortened plan.path: %+v", out.Memberships)
	}
	if b.Streams[0].Derived.Tasks[0].Checkouts[0].Plan.Path != "~/co/lead/.fleet/t/plan.md" || out.BoardAgents[0].Observation.Plan.Path != "~/co/lead/.fleet/t/plan.md" {
		t.Fatal("producer display evidence was normalized away")
	}
	b.Agents = b.Agents[1:]
	out = ProjectWithRoute(b, fixReady(), fixReady(), planPathAgents()[1:], fixNow(), fixRoute())
	if len(out.Memberships) != 0 {
		t.Fatal("reviewer alone cannot establish owner membership")
	}
}
func TestPlanPathEvidenceBoundary(t *testing.T) {
	b := planPathProducerBoard(t)
	agents := planPathAgents()
	owner := &b.Agents[0].Observation
	registered := b.Streams[0].Derived.Tasks[0].Checkouts[0].RegisteredPlan
	joined := func() bool {
		return len(ProjectWithRoute(b, fixReady(), fixReady(), agents, fixNow(), fixRoute()).Memberships) == 1
	}
	if !joined() {
		t.Fatal("producer owner rejected")
	}
	owner.PlanSource = "registered: /elsewhere/.fleet/t/plan.md" // same display basename does not prove identity
	if joined() {
		t.Fatal("different registered source borrowed matching display basename")
	}
	owner.PlanSource = ""
	if joined() {
		t.Fatal("home display label alone established a registered plan")
	}
	owner.PlanSource = "registered: " + registered
	owner.Plan.Path = "/elsewhere/.fleet/t/plan.md"
	if joined() {
		t.Fatal("explicit contradictory absolute plan path accepted")
	}
	owner.Plan.Path = registered
	if !joined() {
		t.Fatal("matching absolute non-home plan path rejected")
	}
	owner.PlanSource = ""
	if !joined() {
		t.Fatal("exact absolute plan path rejected without provenance")
	}
	owner.Plan = nil
	if !joined() {
		t.Fatal("unavailable plan evidence disqualified an otherwise exact scoped assignment")
	}
	owner.PlanSource = "registered: " + registered
	owner.Plan = &Plan{Path: "~/co/lead/.fleet/t/plan.md"}
	b.Streams[0].Derived.Tasks[0].Checkouts[0].RegisteredPlan = "/home/u/co/other/.fleet/t/plan.md"
	if joined() {
		t.Fatal("changed registered plan retained old owner evidence")
	}
}
func TestPlanPathUnscopedCatalogErrorsWithholdHostPaths(t *testing.T) {
	private := "/home/u/.local/share/herdr-workstreams/host.json"
	raw := `{"server":"` + fixServer + `","workstreams":[{"id":"ws","name":"WS","repo":"/home/u/.local/share/herdr-workstreams","source":"` + private + `","source_mode":"catalog","derived":{"version":1,"host_catalog":"` + private + `","section":"Now","tasks":[]}}],"agents":[{"observation":{"pane":"p1","agent_name":"x","checkout":"/co/x","repository":"/repo","catalog_error":"host catalog unreadable: open ` + private + `: permission denied"}},{"observation":{"pane":"p2","agent_name":"y","catalog_error":"catalog validation failed: ` + private + `"}},{"observation":{"pane":"p3","agent_name":"z","catalog_error":"host catalog unreadable: open /unseen/private.json: denied"}},{"observation":{"pane":"p4","agent_name":"other","catalog_error":"registered checkout repository/branch mismatch"}}],"sweep":{"at":"2026-01-01T12:00:30Z"}}`
	b, err := ParseBoard(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if b.Streams[0].Derived.HostCatalog != private {
		t.Fatal("internal host scope lost")
	}
	encoded, _ := json.Marshal(ProjectWithRoute(b, fixReady(), fixReady(), nil, fixNow(), fixRoute()))
	if strings.Contains(string(encoded), private) || strings.Contains(string(encoded), "/home/u/.local/share/herdr-workstreams") || strings.Contains(string(encoded), "/unseen/private.json") {
		t.Fatalf("raw host path escaped public projection: %s", encoded)
	}
	if !strings.Contains(b.Agents[0].Observation.CatalogError, "host catalog unreadable") || b.Agents[0].Observation.CatalogError == "" {
		t.Fatal("unscoped error classification lost")
	}
	if !strings.Contains(b.Agents[1].Observation.CatalogError, "[host catalog]") {
		t.Fatal("known scope path not redacted from unscoped diagnostic")
	}
	if b.Agents[2].Observation.CatalogError != "host catalog unreadable" {
		t.Fatal("unknown scope fallback did not suppress raw read error")
	}
	if b.Agents[3].Observation.CatalogError != "registered checkout repository/branch mismatch" {
		t.Fatal("unrelated error altered")
	}
}
