package work

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/clay-coffman/devdash/internal/collect"
)

const fixServer = "0123456789abcdef"

func fixNow() time.Time  { return time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC) }
func fixReady() Provider { p := Provider{}; p.Update(fixNow(), nil); return p }
func fixRoute() Route    { return Route{Server: fixServer, Verified: true} }

// R1: two workspace sessions with identical labels must not poison the board.
func TestFixDuplicateSessionLabelsAndUnmapped(t *testing.T) {
	raw := `{"server":"` + fixServer + `","workstreams":[{"name":"unfiled · scratch","source_mode":"standalone","panes":["w1:p1"]},{"name":"unfiled · scratch","source_mode":"standalone","panes":["w2:p1"]},{"name":"coordination","source_mode":"coordination","panes":["w3:p1"]}],"agents":[],"sweep":{"at":"2026-01-01T12:00:00Z"}}`
	b, err := ParseBoard(strings.NewReader(raw))
	if err != nil || len(b.Streams) != 3 || b.Streams[0].Key == b.Streams[1].Key {
		t.Fatalf("session identity: %+v, %v", b.Streams, err)
	}
	legacy := strings.ReplaceAll(strings.ReplaceAll(raw, `"server":"`+fixServer+`",`, ``), `standalone`, `unmapped`)
	b, err = ParseBoard(strings.NewReader(legacy))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range b.Streams {
		if s.SourceMode == "unmapped" {
			t.Fatal("unfiled declared ownership")
		}
	}
}

func reviewerBoard(t *testing.T) Board {
	t.Helper()
	raw := `{"server":"` + fixServer + `","workstreams":[{"id":"ws","name":"WS","repo":"/host","source":"/host/c.json","source_mode":"catalog","derived":{"version":1,"host_catalog":"/host/c.json","section":"Now","runtime_known":true,"runtime_observed_at":"2026-01-01T12:00:00Z","fact_max_age":"5m","tasks":[{"id":"T","checkouts":[{"path":"/co/lead","server":"` + fixServer + `","registered_plan":"/co/lead/.fleet/t/plan.md","agent":"lead"},{"path":"/co/lead","server":"` + fixServer + `","observer_only":true,"agent":"rev"}]}]} }],"agents":[],"sweep":{"at":"2026-01-01T12:00:00Z"}}`
	b, err := ParseBoard(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func fixObservation(pane, name string) BoardAgent {
	o := Observation{Pane: pane, Name: name, Checkout: "/co/lead", Cwd: "/co/lead", Repository: "/repo", TaskID: "T", WorkstreamID: "ws", HostCatalog: "/host/c.json"}
	if name == "rev" {
		o.Role = "reviewer"
		o.RoleSource = "registered"
		o.PlanSource = "registered review assignment; no implementation plan"
	}
	a := BoardAgent{Observation: o}
	a.Meta.At = fixNow()
	return a
}
func fixAgent(pane, name, status string) collect.Agent {
	return collect.Agent{Pane: pane, Name: name, Checkout: "/co/lead", Cwd: "/co/lead", Repository: "/repo", Status: status}
}
func TestFixOwnerAndRegisteredReviewer(t *testing.T) {
	b := reviewerBoard(t)
	owner := fixAgent("p1", "lead", "working")
	rev := fixAgent("p2", "rev", "idle")
	b.Agents = []BoardAgent{fixObservation("p1", "lead")}
	out := ProjectWithRoute(b, fixReady(), fixReady(), []collect.Agent{owner}, fixNow(), fixRoute())
	if len(out.Memberships) != 1 {
		t.Fatal("owner alone lost")
	}
	b.Agents = append(b.Agents, fixObservation("p2", "rev"))
	out = ProjectWithRoute(b, fixReady(), fixReady(), []collect.Agent{owner, rev}, fixNow(), fixRoute())
	if len(out.Memberships) != 1 || len(out.Memberships[0].Panes) != 2 || len(out.Memberships[0].BoardPanes) != 2 || len(out.Activity) != 0 {
		t.Fatalf("owner+quiet reviewer %+v %+v", out.Memberships, out.Activity)
	}
	out = ProjectWithRoute(b, fixReady(), fixReady(), []collect.Agent{rev}, fixNow(), fixRoute())
	if len(out.Memberships) != 0 || len(out.Activity) != 1 {
		t.Fatal("reviewer alone cannot establish active owner membership")
	}
	// A reviewer declaration alone cannot establish app or task ownership.
	b.Streams[0].Derived.Tasks[0].Checkouts = b.Streams[0].Derived.Tasks[0].Checkouts[1:]
	out = ProjectWithRoute(b, fixReady(), fixReady(), []collect.Agent{rev}, fixNow(), fixRoute())
	if len(out.Memberships) != 0 || len(out.Activity) != 1 {
		t.Fatal("reviewer-only claimed checkout")
	}
}
func TestFixReviewerCannotBorrowOtherScope(t *testing.T) {
	b := reviewerBoard(t)
	owner := fixAgent("p1", "lead", "working")
	rev := fixAgent("p2", "rev", "working")
	b.Agents = []BoardAgent{fixObservation("p1", "lead"), fixObservation("p2", "rev")}
	mutate := func(f func()) {
		t.Helper()
		f()
		out := ProjectWithRoute(b, fixReady(), fixReady(), []collect.Agent{owner, rev}, fixNow(), fixRoute())
		if len(out.Memberships) != 0 {
			t.Fatalf("foreign reviewer claimed owner membership: %+v", out.Memberships)
		}
	}
	mutate(func() { b.Streams[0].Derived.Tasks[0].Checkouts[1].Server = "2222222222222222" })
	b.Streams[0].Derived.Tasks[0].Checkouts[1].Server = fixServer
	mutate(func() { b.Agents[1].Observation.TaskID = "OTHER" })
	b.Agents[1].Observation.TaskID = "T"
	mutate(func() { b.Agents[1].Observation.HostCatalog = "/other/catalog" })
	b.Agents[1].Observation.HostCatalog = "/host/c.json"
	mutate(func() { b.Agents[1].Observation.RoleSource = "" })
	b.Agents[1].Observation.RoleSource = "registered"
	// Two owners at this checkout remain ambiguous regardless of a reviewer.
	b.Streams[0].Derived.Tasks[0].Checkouts = append(b.Streams[0].Derived.Tasks[0].Checkouts, Checkout{Path: "/co/lead", Server: fixServer, RegisteredPlan: "/other/plan"})
	mutate(func() {})
}

// R3: coalesce two matching panes into one physical membership.
func TestFixLegacyMultiPaneAndConflictingPane(t *testing.T) {
	b := Board{Streams: []Stream{{Key: "legacy", Repo: "/repo", SourceMode: "legacy", Panes: []string{"p1", "p2"}}}}
	b.Sweep.At = fixNow()
	for _, name := range []string{"p1", "p2"} {
		a := BoardAgent{Observation: Observation{Pane: name, Name: name, Checkout: "/co/x", Cwd: "/co/x", Repository: "/repo"}}
		a.Meta.At = fixNow()
		b.Agents = append(b.Agents, a)
	}
	agents := []collect.Agent{{Pane: "p1", Name: "p1", Checkout: "/co/x", Cwd: "/co/x", Repository: "/repo"}, {Pane: "p2", Name: "p2", Checkout: "/co/x", Cwd: "/co/x", Repository: "/repo"}}
	out := Project(b, fixReady(), fixReady(), agents, fixNow())
	if len(out.Memberships) != 1 || len(out.Memberships[0].Panes) != 2 || len(out.Activity) != 0 {
		t.Fatalf("legacy multi pane: %+v %+v", out.Memberships, out.Activity)
	}
	agents = append(agents, collect.Agent{Pane: "p3", Name: "visitor", Checkout: "/co/x", Cwd: "/co/x"})
	out = Project(b, fixReady(), fixReady(), agents, fixNow())
	if len(out.Memberships) != 0 || len(out.Activity) != 1 || len(out.Activity[0].Panes) != 3 {
		t.Fatal("unrelated pane was hidden or grouped")
	}
}

// R4: old unfiled rows never own a Git checkout.
func TestFixOldUnmappedNeverOwns(t *testing.T) {
	raw := `{"workstreams":[{"name":"unfiled · misc","source_mode":"unmapped","panes":["p1"]}],"agents":[{"observation":{"pane":"p1","agent_name":"a","checkout":"/co/y","cwd":"/co/y"},"meta":{"collected_at":"2026-01-01T12:01:00Z"}}],"sweep":{"at":"2026-01-01T12:01:00Z"}}`
	b, err := ParseBoard(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	out := Project(b, fixReady(), fixReady(), []collect.Agent{{Pane: "p1", Name: "a", Checkout: "/co/y", Cwd: "/co/y"}}, fixNow())
	if len(out.Memberships) != 0 || len(out.Activity) != 1 {
		t.Fatal("unmapped session became owner")
	}
}
func TestFixPlanSourceConflictsAndUnavailableEvidence(t *testing.T) {
	b := reviewerBoard(t)
	b.Agents = []BoardAgent{fixObservation("p1", "lead")}
	owner := fixAgent("p1", "lead", "working")
	route := fixRoute()
	expected := b.Streams[0].Derived.Tasks[0].Checkouts[0].RegisteredPlan
	b.Agents[0].Observation.PlanSource = "registered: " + expected
	b.Agents[0].Observation.Plan = &Plan{Path: expected}
	success := func() bool {
		return len(ProjectWithRoute(b, fixReady(), fixReady(), []collect.Agent{owner}, fixNow(), route).Memberships) == 1
	}
	if !success() {
		t.Fatal("matching registered plan rejected")
	}
	b.Agents[0].Observation.PlanSource = "registered: /co/lead/old.md"
	if success() {
		t.Fatal("contradictory registered: source joined")
	}
	b.Agents[0].Observation.PlanSource = "registered: " + expected
	b.Agents[0].Observation.Plan = &Plan{Path: "/co/lead/old.md"}
	if success() {
		t.Fatal("contradictory plan.path joined")
	}
	b.Agents[0].Observation.PlanSource = ""
	b.Agents[0].Observation.Plan = nil
	if !success() {
		t.Fatal("unavailable plan evidence should not be fabricated as contradictory when scoped assignment is exact")
	}
}

// R5: producer source equals its host_catalog; no direct or indirect key leak.
func TestFixHostCatalogPrivacyAcrossPublicProjection(t *testing.T) {
	private := "/home/u/.local/share/herdr-workstreams/host.json"
	raw := `{"server":"` + fixServer + `","workstreams":[{"id":"ws","name":"WS","repo":"/home/u/.local/share/herdr-workstreams","source":"` + private + `","source_mode":"catalog","warning":"cannot read ` + private + `","derived":{"version":1,"host_catalog":"` + private + `","section":"Now","reasons":["check ` + private + `"],"diagnostics":["path ` + private + `"],"input_error":"source ` + private + `","tasks":[{"id":"T","checkouts":[{"path":"/co/a","server":"` + fixServer + `","plan_error":"at ` + private + `"}]}]}}],"agents":[{"observation":{"pane":"p","agent_name":"x","repository":"/repo","checkout":"/co/a","host_catalog":"` + private + `","task_id":"T","workstream_id":"ws","catalog_error":"cannot open ` + private + `"}}],"sweep":{"at":"2026-01-01T12:00:00Z"}}`
	b, err := ParseBoard(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if b.Streams[0].Derived.HostCatalog != private || b.Agents[0].Observation.HostCatalog != private {
		t.Fatal("internal scope lost")
	}
	out := ProjectWithRoute(b, fixReady(), fixReady(), nil, fixNow(), fixRoute())
	data, _ := json.Marshal(out)
	if strings.Contains(string(data), private) || strings.Contains(string(data), "/home/u/.local/share/herdr-workstreams") {
		t.Fatalf("private host catalog serialized: %s", data)
	}
	for _, key := range []string{b.Streams[0].Key, b.Streams[0].Derived.Tasks[0].Key, b.Streams[0].Derived.Tasks[0].Checkouts[0].Key, b.Agents[0].Observation.TaskKey} {
		if strings.Contains(key, private) || key == "" {
			t.Fatalf("unsafe or missing key: %s", key)
		}
	}
	// A missing top-level modern server may suppress joins, but must never
	// silently fall back to legacy path-bearing public keys.
	noServer := strings.Replace(raw, `"server":"`+fixServer+`",`, "", 1)
	without, err := ParseBoard(strings.NewReader(noServer))
	if err != nil {
		t.Fatal(err)
	}
	missing, _ := json.Marshal(ProjectWithRoute(without, fixReady(), fixReady(), nil, fixNow(), fixRoute()))
	if strings.Contains(string(missing), private) || strings.Contains(string(missing), "/home/u/.local/share/herdr-workstreams") {
		t.Fatalf("missing-server host identity leaked: %s", missing)
	}
}

func TestFixDuplicateSessionJSONContainsNoPrivateFields(t *testing.T) {
	b := reviewerBoard(t)
	v, _ := json.Marshal(b)
	if strings.Contains(string(v), "host_catalog") {
		t.Fatal("host scope serialized")
	}
}
