package work

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/clay-coffman/devdash/internal/collect"
)

const modernBoard = `{"server":"0123456789abcdef","workstreams":[{"id":"multi","name":"Across repositories","repo":"/catalog-host","source":"/private/host.json","source_mode":"catalog","derived":{"version":1,"host_catalog":"/private/host.json","section":"Now","runtime_known":true,"runtime_observed_at":"2026-01-01T12:00:00Z","fact_max_age":"5m","unregistered_agents":[{"task_id":"A","pane":"w1:p9","agent":"visitor","checkout":"/checkouts/shared"}],"tasks":[{"id":"A","checkouts":[{"path":"/checkouts/shared","repo":"same","server":"0123456789abcdef","registered_plan":"/checkouts/shared/a/plan.md","agent":"builder"},{"path":"/checkouts/shared","repo":"same","server":"fedcba9876543210","registered_plan":"/checkouts/shared/a/plan.md"}]},{"id":"B","checkouts":[{"path":"/checkouts/shared","repo":"same","server":"0123456789abcdef","registered_plan":"/checkouts/shared/b/plan.md"}]}]}},{"id":"multi","repo":"/elsewhere","name":"Same name","derived":{"version":1,"runtime_known":true,"runtime_observed_at":"2026-01-01T12:00:00Z","fact_max_age":"5m","tasks":[{"id":"A","checkouts":[{"path":"/checkouts/other","repo":"same","server":"fedcba9876543210"}]}]}}],"agents":[],"sweep":{"at":"2026-01-01T12:00:00Z"}}`

func TestModernContractPrivacyAndIdentity(t *testing.T) {
	b, err := ParseBoard(strings.NewReader(modernBoard))
	if err != nil {
		t.Fatal(err)
	}
	if b.Server != "0123456789abcdef" || b.Streams[0].Derived.Tasks[0].Checkouts[0].Repo != "same" || b.Streams[0].Source != "" || b.Streams[0].Repo != "" {
		t.Fatal("contract fields dropped")
	}
	a := b.Streams[0].Derived.Tasks[0].Checkouts
	if a[0].Key == a[1].Key || a[0].Key == b.Streams[0].Derived.Tasks[1].Checkouts[0].Key || b.Streams[0].Derived.Tasks[0].Key == b.Streams[1].Derived.Tasks[0].Key {
		t.Fatal("binding or scoped task collision")
	}
	raw, _ := json.Marshal(b)
	if strings.Contains(string(raw), "/private/host.json") {
		t.Fatal("host catalog leaked")
	}
}

func TestModernFailClosedAndAmbiguity(t *testing.T) {
	b, _ := ParseBoard(strings.NewReader(modernBoard))
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)
	p := Provider{}
	p.Update(now, nil)
	agents := []collect.Agent{{Pane: "w1:p1", Name: "builder", Checkout: "/checkouts/shared", Cwd: "/checkouts/shared", Repository: "/actual/repo"}}
	check := func(route Route, count int) Projection {
		t.Helper()
		result := ProjectWithRoute(b, p, p, agents, now, route)
		if len(result.Memberships) != count || len(result.Activity) != 1 {
			t.Fatalf("route %+v: memberships %d activity %d", route, len(result.Memberships), len(result.Activity))
		}
		return result
	}
	check(Route{Server: "0123456789abcdef", Verified: true}, 0) // two local plans
	check(Route{Server: "fedcba9876543210", Verified: true}, 0) // foreign producer
	check(Route{}, 0)
	// Only one declared local binding, but an unregistered pane must not attach.
	b.Streams[0].Derived.Tasks = b.Streams[0].Derived.Tasks[:1]
	result := ProjectWithRoute(b, p, p, []collect.Agent{{Pane: "w1:p9", Name: "visitor", Checkout: "/checkouts/shared", Cwd: "/checkouts/shared"}}, now, Route{Server: b.Server, Verified: true})
	if len(result.Memberships) != 0 || len(result.Activity) != 1 {
		t.Fatal("unregistered agent joined")
	}
	// Exact observed pane/assignment evidence can establish an agent-constrained binding.
	b.Streams[0].Derived.Unregistered = nil
	b.Agents = []BoardAgent{{Observation: Observation{Pane: "w1:p1", Name: "builder", Checkout: "/checkouts/shared", TaskID: "A", WorkstreamID: "multi", Repository: "/actual/repo", HostCatalog: "/private/host.json"}}}
	b.Agents[0].Meta.At = now
	result = ProjectWithRoute(b, p, p, agents, now, Route{Server: b.Server, Verified: true})
	if len(result.Memberships) != 1 || len(result.Memberships[0].Panes) != 1 {
		t.Fatalf("exact membership absent: %+v", result.Memberships)
	}
}

func TestModernLocalForeignReviewerAndAppOnly(t *testing.T) {
	b, _ := ParseBoard(strings.NewReader(modernBoard))
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)
	p := Provider{}
	p.Update(now, nil)
	b.Streams[0].Derived.Tasks = b.Streams[0].Derived.Tasks[:1]
	route := Route{Server: b.Server, Verified: true}
	result := ProjectWithRoute(b, p, p, nil, now, route)
	if len(result.Memberships) != 1 || result.Memberships[0].Checkout != "/checkouts/shared" {
		t.Fatalf("foreign binding blocked unique local app: %+v", result.Memberships)
	}
	b.Streams[0].Derived.Tasks[0].Checkouts[0].ObserverOnly = true
	result = ProjectWithRoute(b, p, p, nil, now, route)
	if len(result.Memberships) != 0 {
		t.Fatal("reviewer claimed app")
	}
	b.Streams[0].Derived.Tasks[0].Checkouts[0].ObserverOnly = false
	b.Streams[0].Derived.Tasks[0].Checkouts[0].Server = "malformed"
	result = ProjectWithRoute(b, p, p, nil, now, route)
	if len(result.Memberships) != 0 {
		t.Fatal("malformed binding claimed app")
	}
	b.Server = "malformed"
	result = ProjectWithRoute(b, p, p, nil, now, route)
	if len(result.Memberships) != 0 || result.Route.Reason == "" {
		t.Fatal("malformed producer did not fail closed")
	}
	b.Server = ""
	result = ProjectWithRoute(b, p, p, nil, now, route)
	if len(result.Memberships) != 0 || result.Route.Reason == "" {
		t.Fatal("serverless producer wildcarded scoped binding")
	}
}

func TestModernScopedObservationAndStaleRow(t *testing.T) {
	b, _ := ParseBoard(strings.NewReader(modernBoard))
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)
	p := Provider{}
	p.Update(now, nil)
	b.Streams[0].Derived.Tasks = b.Streams[0].Derived.Tasks[:1]
	b.Agents = []BoardAgent{{Observation: Observation{Pane: "p", Name: "builder", Checkout: "/checkouts/shared", Repository: "/actual/repo", HostCatalog: "/other/catalog", TaskID: "A", WorkstreamID: "multi"}}}
	b.Agents[0].Meta.At = now
	agents := []collect.Agent{{Pane: "p", Name: "builder", Checkout: "/checkouts/shared", Cwd: "/checkouts/shared", Repository: "/actual/repo"}}
	route := Route{Server: b.Server, Verified: true}
	result := ProjectWithRoute(b, p, p, agents, now, route)
	if len(result.Memberships) != 0 || len(result.Activity) != 1 {
		t.Fatal("other catalog task ID borrowed pane")
	}
	b.Agents[0].Observation.HostCatalog = "/private/host.json"
	result = ProjectWithRoute(b, p, p, agents, now, route)
	if len(result.Memberships) != 1 || len(result.Activity) != 0 {
		t.Fatal("exact catalog scope not joined")
	}
	agents[0].Cwd = "/other/foreground"
	b.Agents[0].Observation.Cwd = "/checkouts/shared"
	result = ProjectWithRoute(b, p, p, agents, now, route)
	if len(result.Memberships) != 0 {
		t.Fatal("changed foreground location borrowed pane")
	}
	agents[0].Cwd = "/checkouts/shared"
	b.Streams[0].Derived.InputError = "catalog unavailable"
	result = ProjectWithRoute(b, p, p, agents, now, route)
	if len(result.Memberships) != 0 || len(result.Activity) != 1 {
		t.Fatal("input error hid observed agent")
	}
}

func TestModernLegacyPaneStillNeedsExactServerAndLocation(t *testing.T) {
	b, _ := ParseBoard(strings.NewReader(modernBoard))
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)
	p := Provider{}
	p.Update(now, nil)
	b.Streams = []Stream{{Key: "legacy", Repo: "/actual/repo", SourceMode: "legacy", Panes: []string{"p"}}}
	b.Agents = []BoardAgent{{Observation: Observation{Pane: "p", Name: "builder", Checkout: "/checkouts/shared", Repository: "/actual/repo"}}}
	b.Agents[0].Meta.At = now
	agent := collect.Agent{Pane: "p", Name: "builder", Checkout: "/checkouts/shared", Cwd: "/checkouts/shared", Repository: "/actual/repo"}
	route := Route{Server: b.Server, Verified: true}
	if out := ProjectWithRoute(b, p, p, []collect.Agent{agent}, now, route); len(out.Memberships) != 1 || len(out.Activity) != 0 {
		t.Fatal("modern board lost exact legacy pane")
	}
	agent.Cwd = "/changed"
	b.Agents[0].Observation.Cwd = "/checkouts/shared"
	if out := ProjectWithRoute(b, p, p, []collect.Agent{agent}, now, route); len(out.Memberships) != 0 {
		t.Fatal("reused pane joined legacy stream")
	}
	route.Verified = false
	if out := ProjectWithRoute(b, p, p, []collect.Agent{agent}, now, route); len(out.Memberships) != 0 {
		t.Fatal("unverified server joined legacy stream")
	}
}

func TestConflictingDuplicateBindingFailsClosed(t *testing.T) {
	b, _ := ParseBoard(strings.NewReader(modernBoard))
	now := time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)
	p := Provider{}
	p.Update(now, nil)
	secondPlan := b.Streams[0].Derived.Tasks[1].Checkouts[0]
	b.Streams[0].Derived.Tasks = b.Streams[0].Derived.Tasks[:1]
	checkouts := b.Streams[0].Derived.Tasks[0].Checkouts
	duplicate := checkouts[0]
	b.Streams[0].Derived.Tasks[0].Checkouts = append(checkouts, duplicate)
	route := Route{Server: b.Server, Verified: true}
	if len(ProjectWithRoute(b, p, p, nil, now, route).Memberships) != 1 {
		t.Fatal("exact duplicate was not coalesced")
	}
	b.Streams[0].Derived.Tasks[0].Checkouts[2].Agent = "different"
	if len(ProjectWithRoute(b, p, p, nil, now, route).Memberships) != 0 {
		t.Fatal("conflicting duplicate claimed app")
	}
	b.Streams[0].Derived.Tasks[0].Checkouts[2] = secondPlan
	if len(ProjectWithRoute(b, p, p, nil, now, route).Memberships) != 0 {
		t.Fatal("two plans in one task claimed app")
	}

}
