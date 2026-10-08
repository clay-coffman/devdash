package work

import (
	"errors"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/clay-coffman/devdash/internal/collect"
)

type Provider struct {
	State       string    `json:"state"`
	Available   bool      `json:"available"`
	HasData     bool      `json:"has_data"`
	LastAttempt time.Time `json:"last_attempt"`
	LastSuccess time.Time `json:"last_success"`
	Error       string    `json:"error,omitempty"`
	Stale       bool      `json:"stale"`
}

func (p *Provider) Update(at time.Time, err error) {
	p.LastAttempt = at
	p.Available = !errors.Is(err, exec.ErrNotFound)
	if err == nil {
		p.State, p.Error, p.HasData, p.LastSuccess = "ready", "", true, at
	} else {
		p.State, p.Error = "error", err.Error()
		if !p.Available {
			p.State = "missing"
		}
	}
}
func Fresh(at, now time.Time, max time.Duration) bool {
	return !at.IsZero() && !at.After(now.Add(time.Second)) && now.Sub(at) <= max
}

type Activity struct {
	Key        string   `json:"key"`
	Checkout   string   `json:"checkout"`
	Cwd        string   `json:"cwd"`
	Workspace  string   `json:"workspace"`
	Repository string   `json:"repository,omitempty"`
	Panes      []string `json:"panes"`
}

// Membership is the sole validated owner of one physical checkout. The
// browser never joins a task to a path, pane or board observation itself.
type Membership struct {
	Checkout   string   `json:"checkout"`
	StreamKey  string   `json:"stream_key"`
	TaskKey    string   `json:"task_key,omitempty"`
	BindingKey string   `json:"binding_key,omitempty"`
	Panes      []string `json:"panes"`
	BoardPanes []string `json:"board_panes"`
}
type Projection struct {
	Version     int             `json:"version"`
	Now         time.Time       `json:"now"`
	Board       Provider        `json:"board"`
	Herdr       Provider        `json:"herdr"`
	Route       Route           `json:"route"`
	BoardServer string          `json:"board_server,omitempty"`
	SweepAt     time.Time       `json:"sweep_at"`
	Streams     []Stream        `json:"workstreams"`
	BoardAgents []BoardAgent    `json:"board_agents"`
	Agents      []collect.Agent `json:"agents"`
	Activity    []Activity      `json:"activity"`
	Memberships []Membership    `json:"memberships,omitempty"`
}

// The producer's registered: <absolute> provenance identifies the plan;
// ReadPlanFile's ~/... path is only a display label. Without that provenance
// a shortened label cannot establish identity. A missing plan read remains
// neither positive nor contradictory when scoped catalog evidence is exact.
func compatiblePlan(o Observation, binding Checkout) bool {
	if strings.HasPrefix(o.PlanSource, "registered: ") && strings.TrimPrefix(o.PlanSource, "registered: ") != binding.RegisteredPlan {
		return false
	}
	if o.PlanSource == "registered review assignment; no implementation plan" {
		return false
	}
	if o.Plan == nil || o.Plan.Path == "" {
		return true
	}
	if strings.HasPrefix(o.Plan.Path, "~/") {
		return binding.RegisteredPlan != "" && o.PlanSource == "registered: "+binding.RegisteredPlan
	}
	return strings.HasPrefix(o.Plan.Path, "/") && o.Plan.Path == binding.RegisteredPlan
}

// Project is retained for legacy callers and fixture compatibility. New
// collection always passes independently verified routing evidence.
func Project(b Board, bp, hp Provider, agents []collect.Agent, now time.Time) Projection {
	return ProjectWithRoute(b, bp, hp, agents, now, Route{})
}
func ProjectWithRoute(b Board, bp, hp Provider, agents []collect.Agent, now time.Time, route Route) Projection {
	bp.Stale = !Fresh(b.Sweep.At, now, 5*time.Minute) || bp.State != "ready"
	hp.Stale = !Fresh(hp.LastSuccess, now, 15*time.Second) || hp.State != "ready"
	p := Projection{Version: Version, Now: now, Board: bp, Herdr: hp, Route: route, BoardServer: b.Server, SweepAt: b.Sweep.At, Streams: make([]Stream, len(b.Streams)), BoardAgents: b.Agents, Agents: agents, Activity: []Activity{}, Memberships: []Membership{}}
	if p.BoardAgents == nil {
		p.BoardAgents = []BoardAgent{}
	}
	if p.Agents == nil {
		p.Agents = []collect.Agent{}
	}
	type candidate struct {
		stream, task, binding int
		legacy                bool
	}
	byPath := map[string][]candidate{}
	legacyPanes := map[string][]candidate{}
	modern := b.Server != ""
	// A host-scoped row or unregistered/scoped fact without the producer's
	// server ID is incomplete modern input, never an old wildcard payload.
	for _, s := range b.Streams {
		if s.Derived != nil {
			if s.Derived.HostCatalog != "" || len(s.Derived.Unregistered) > 0 {
				modern = true
			}
			for _, t := range s.Derived.Tasks {
				for _, co := range t.Checkouts {
					if co.Server != "" {
						modern = true
					}
				}
			}
		}
	}
	validServer := serverID.MatchString(b.Server) && route.Verified && route.Server == b.Server
	if modern && !validServer && p.Route.Reason == "" {
		p.Route.Reason = "Board server identity is missing, invalid or differs from verified Herdr endpoint"
	}
	for i, s := range b.Streams {
		p.Streams[i] = s
		p.Streams[i].LivePanes = []string{}
		if s.Derived == nil {
			if (!modern || validServer) && s.SourceMode != "coordination" && s.SourceMode != "standalone" {
				for _, pane := range s.Panes {
					legacyPanes[pane] = append(legacyPanes[pane], candidate{stream: i, task: -1, legacy: true})
				}
			}
			continue
		}
		d := *s.Derived
		d.Tasks = append([]Task(nil), d.Tasks...)
		p.Streams[i].Derived = &d
		max, err := time.ParseDuration(d.FactMaxAge)
		if err != nil || max <= 0 {
			max = 5 * time.Minute
		}
		usable := !bp.Stale && d.Version == 1 && d.InputError == "" && d.RuntimeKnown && Fresh(d.RuntimeAt, now, max) && d.Section != "Coordination" && d.Section != "Standalone sessions"
		for j := range d.Tasks {
			d.Tasks[j].LivePanes = []string{}
			if !usable {
				continue
			}
			for k, c := range d.Tasks[j].Checkouts {
				path := Path(c.Path)
				if path == "" || c.ObserverOnly {
					continue
				}
				if modern {
					if !validServer || c.Server != "" && (!serverID.MatchString(c.Server) || c.Server != b.Server) {
						continue
					}
				} else if c.Server != "" {
					p.Route.Reason = "Server-scoped checkout has no producer server identity"
					continue
				} // no wildcard for server-scoped facts without producer identity
				byPath[path] = append(byPath[path], candidate{i, j, k, false})
			}
		}
	}
	boardPanes := map[string][]BoardAgent{}
	for _, a := range b.Agents {
		boardPanes[a.Observation.Pane] = append(boardPanes[a.Observation.Pane], a)
	}
	// A binding is unique only among independently declared local bindings.
	// Exact duplicate keys are one fact; conflicting facts remain ambiguous.
	unique := map[string][]candidate{}
	for path, candidates := range byPath {
		seen := map[string]Checkout{}
		for _, c := range candidates {
			co := p.Streams[c.stream].Derived.Tasks[c.task].Checkouts[c.binding]
			key := co.Key
			if key == "" {
				key = p.Streams[c.stream].Derived.Tasks[c.task].Key + "\x00" + path
			}
			if previous, ok := seen[key]; ok && reflect.DeepEqual(previous, co) {
				continue
			}
			seen[key] = co
			unique[path] = append(unique[path], c)
		}
	}
	matched := map[string]bool{}
	legacyGroups := map[string]map[int]*Membership{}
	legacyPending := map[string]collect.Agent{}
	confirmedLegacy := map[string]bool{}
	// Membership requires all agents at the physical checkout to validate the
	// same unique declaration. App-only checkouts may use that unique declaration.
	for path, candidates := range unique {
		if len(candidates) != 1 || bp.Stale {
			continue
		}
		c := candidates[0]
		stream := &p.Streams[c.stream]
		task := &stream.Derived.Tasks[c.task]
		binding := task.Checkouts[c.binding]
		m := Membership{Checkout: path, StreamKey: stream.Key, TaskKey: task.Key, BindingKey: binding.Key, Panes: []string{}, BoardPanes: []string{}}
		ok := true
		ownerSeen := false
		for _, a := range agents {
			if Path(a.Checkout) != path {
				continue
			}
			if hp.Stale || Path(a.Cwd) == "" || Path(a.Cwd) != path && Path(a.Checkout) != path {
				ok = false
				break
			}
			observations := boardPanes[a.Pane]
			if modern {
				if len(observations) != 1 || !Fresh(observations[0].Meta.At, now, 5*time.Minute) {
					ok = false
					break
				}
				o := observations[0].Observation
				reviewer := false
				for _, co := range task.Checkouts {
					if co.ObserverOnly && co.Agent == a.Name && Path(co.Path) == path && co.Server == binding.Server && o.RoleSource == "registered" && o.Role == "reviewer" && o.PlanSource == "registered review assignment; no implementation plan" && o.Plan == nil {
						reviewer = true
						break
					}
				}
				if o.Name != a.Name || Path(o.Checkout) != path || (Path(o.Cwd) != "" && Path(o.Cwd) != Path(a.Cwd)) || o.TaskID != task.ID || o.WorkstreamID != stream.ID || o.Repository == "" || o.Repository != a.Repository || o.CatalogError != "" || (stream.Derived.HostCatalog != "" && o.HostCatalog != stream.Derived.HostCatalog) || (stream.Derived.HostCatalog == "" && (o.HostCatalog != "" || Path(o.Repository) != Path(stream.Repo))) || (binding.Agent != "" && binding.Agent != a.Name && !reviewer) || (!reviewer && !compatiblePlan(o, binding)) {
					ok = false
					break
				}
				if !ok {
					break
				}
				if !reviewer {
					ownerSeen = true
				}
				m.BoardPanes = append(m.BoardPanes, a.Pane)
			}
			if !modern && len(observations) == 1 && Fresh(observations[0].Meta.At, now, 5*time.Minute) && Path(observations[0].Observation.Checkout) == path {
				m.BoardPanes = append(m.BoardPanes, a.Pane)
			}
			for _, u := range stream.Derived.Unregistered {
				if u.Pane == a.Pane && Path(u.Checkout) == path {
					ok = false
					break
				}
			}
			if !ok {
				break
			}
			m.Panes = append(m.Panes, a.Pane)
		}
		if !ok || modern && len(m.Panes) > 0 && !ownerSeen {
			continue
		}
		// Without a fresh Herdr read, an app-only binding remains eligible but
		// cannot claim any cached pane. Browser checks both source freshnesses.
		p.Memberships = append(p.Memberships, m)
		for _, pane := range m.Panes {
			matched[pane] = true
			stream.LivePanes = append(stream.LivePanes, pane)
			task.LivePanes = append(task.LivePanes, pane)
		}
	}
	grouped := map[string]*Activity{}
	for _, a := range agents {
		if matched[a.Pane] {
			continue
		}
		obs := boardPanes[a.Pane]
		// Legacy rows use only a fresh exact pane/location, never a path label.
		if !hp.Stale && !bp.Stale && len(obs) == 1 && Fresh(obs[0].Meta.At, now, 5*time.Minute) {
			o := obs[0].Observation
			same := (Path(a.Checkout) != "" && Path(o.Checkout) == Path(a.Checkout)) || (a.Checkout == "" && Path(a.Cwd) != "" && Path(o.Cwd) == Path(a.Cwd))
			matches := legacyPanes[a.Pane]
			if Path(a.Checkout) != "" && same && (Path(o.Cwd) == "" || Path(a.Cwd) == "" || Path(o.Cwd) == Path(a.Cwd)) && len(matches) == 1 && len(unique[Path(a.Checkout)]) == 0 && (!modern || (obs[0].Observation.Name == a.Name && obs[0].Observation.Repository != "" && obs[0].Observation.Repository == a.Repository && Path(p.Streams[matches[0].stream].Repo) == Path(a.Repository) && obs[0].Observation.HostCatalog == "")) {
				path := Path(a.Checkout)
				if legacyGroups[path] == nil {
					legacyGroups[path] = map[int]*Membership{}
				}
				m := legacyGroups[path][matches[0].stream]
				if m == nil {
					m = &Membership{Checkout: path, StreamKey: p.Streams[matches[0].stream].Key, Panes: []string{}, BoardPanes: []string{}}
					legacyGroups[path][matches[0].stream] = m
				}
				m.Panes = append(m.Panes, a.Pane)
				m.BoardPanes = append(m.BoardPanes, a.Pane)
				legacyPending[a.Pane] = a
				continue
			}
		}
		path := Path(a.Checkout)
		key := "checkout:" + path
		if path == "" {
			key = "cwd:" + Path(a.Cwd) + "\x00workspace:" + a.Workspace
		}
		g := grouped[key]
		if g == nil {
			g = &Activity{Key: key, Checkout: path, Cwd: a.Cwd, Workspace: a.Group, Repository: a.Repository, Panes: []string{}}
			if g.Repository == "" && !modern && !bp.Stale && len(obs) == 1 && Fresh(obs[0].Meta.At, now, 5*time.Minute) && path != "" && Path(obs[0].Observation.Checkout) == path {
				g.Repository = obs[0].Observation.Repository
			}
			grouped[key] = g
		}
		g.Panes = append(g.Panes, a.Pane)
	}
	for path, streams := range legacyGroups {
		if len(streams) != 1 || len(unique[path]) != 0 {
			continue
		}
		for i, m := range streams {
			total := 0
			for _, a := range agents {
				if Path(a.Checkout) == path {
					total++
				}
			}
			if total != len(m.Panes) {
				continue
			}
			p.Memberships = append(p.Memberships, *m)
			confirmedLegacy[path] = true
			p.Streams[i].LivePanes = append(p.Streams[i].LivePanes, m.Panes...)
			delete(grouped, "checkout:"+path)
		}
	}
	for _, a := range legacyPending {
		path := Path(a.Checkout)
		if confirmedLegacy[path] {
			continue
		}
		key := "checkout:" + path
		g := grouped[key]
		if g == nil {
			g = &Activity{Key: key, Checkout: path, Cwd: a.Cwd, Workspace: a.Group, Repository: a.Repository, Panes: []string{}}
			grouped[key] = g
		}
		g.Panes = append(g.Panes, a.Pane)
	}
	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		p.Activity = append(p.Activity, *grouped[key])
	}
	sort.Slice(p.Memberships, func(i, j int) bool { return p.Memberships[i].Checkout < p.Memberships[j].Checkout })
	return p
}
