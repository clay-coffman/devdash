// Package work is devdash's deliberately small, read-only work contract.
// It is not the board's catalog, classifier, or cleanup policy.
package work

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

const Version = 1
const MaxDocument = 8 << 20

// The structs below are allowlists. Unknown input fields are never retained.
type Decision struct {
	At        time.Time `json:"at"`
	Reason    string    `json:"reason"`
	Reference string    `json:"reference"`
}
type Plan struct {
	Path         string   `json:"path"`
	Done         int      `json:"done"`
	Open         int      `json:"open"`
	Blocked      int      `json:"blocked"`
	BlockedLines []string `json:"blocked_lines"`
	AgeMinutes   float64  `json:"plan_age_min"`
}
type Git struct {
	Head        string `json:"head,omitempty"`
	StatusKnown bool   `json:"status_known"`
	Branch      string `json:"branch"`
	DirtyFiles  int    `json:"dirty_files"`
}
type Check struct {
	Name     string `json:"name"`
	Workflow string `json:"workflow,omitempty"`
	State    string `json:"state"`
	URL      string `json:"url,omitempty"`
}
type Checks struct {
	Rollup           string  `json:"rollup,omitempty"`
	Total            int     `json:"total"`
	Failing          int     `json:"failing"`
	Pending          int     `json:"pending"`
	Passing          int     `json:"passing"`
	AwaitingApproval int     `json:"awaiting_approval"`
	Failed           []Check `json:"failed,omitempty"`
	Waiting          []Check `json:"waiting,omitempty"`
	Running          []Check `json:"running,omitempty"`
	Truncated        bool    `json:"truncated,omitempty"`
	Unnamed          int     `json:"unnamed,omitempty"`
}
type Queue struct {
	Position   int       `json:"position"`
	State      string    `json:"state"`
	EnqueuedAt time.Time `json:"enqueued_at"`
	ETASeconds *int      `json:"eta_seconds,omitempty"`
	Group      *Checks   `json:"group,omitempty"`
}
type QueueExit struct {
	ObservedAt time.Time `json:"observed_at"`
	Position   int       `json:"position"`
	LastState  string    `json:"last_state"`
	Group      *Checks   `json:"group,omitempty"`
}
type PR struct {
	Reference  string     `json:"reference,omitempty"`
	Repository string     `json:"repository,omitempty"`
	Title      string     `json:"title,omitempty"`
	Number     int        `json:"number"`
	State      string     `json:"state"`
	Draft      bool       `json:"is_draft"`
	Review     string     `json:"review_decision,omitempty"`
	URL        string     `json:"url,omitempty"`
	ObservedAt time.Time  `json:"observed_at"`
	Error      string     `json:"error,omitempty"`
	Checks     *Checks    `json:"checks,omitempty"`
	Queue      *Queue     `json:"merge_queue,omitempty"`
	Exit       *QueueExit `json:"merge_queue_exit,omitempty"`
}

// Board workstream PRs use older camelCase keys, while agent PRs use snake_case.
func (p *PR) UnmarshalJSON(b []byte) error {
	type plain PR
	var v struct {
		plain
		OldDraft  bool   `json:"isDraft"`
		OldReview string `json:"reviewDecision"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*p = PR(v.plain)
	p.Draft = p.Draft || v.OldDraft
	if p.Review == "" {
		p.Review = v.OldReview
	}
	p.URL = SafeURL(p.URL)
	cleanChecks(p.Checks)
	if p.Queue != nil {
		cleanChecks(p.Queue.Group)
	}
	if p.Exit != nil {
		cleanChecks(p.Exit.Group)
	}
	return nil
}
func cleanChecks(c *Checks) {
	if c == nil {
		return
	}
	for _, list := range [][]Check{c.Failed, c.Waiting, c.Running} {
		for i := range list {
			list[i].URL = SafeURL(list[i].URL)
		}
	}
}
func SafeURL(s string) string {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return ""
	}
	return u.String()
}

// Path identities must be absolute. No basename or branch-name joins.
func Path(s string) string {
	if !filepath.IsAbs(s) {
		return ""
	}
	return filepath.Clean(s)
}

type Checkout struct {
	Key            string    `json:"key,omitempty"`
	Path           string    `json:"path"`
	Repo           string    `json:"repo,omitempty"`
	Server         string    `json:"server,omitempty"`
	ExpectedBranch string    `json:"expected_branch"`
	Status         string    `json:"status"`
	ObservedAt     time.Time `json:"observed_at"`
	Git            *Git      `json:"git,omitempty"`
	Plan           *Plan     `json:"plan,omitempty"`
	RegisteredPlan string    `json:"registered_plan,omitempty"`
	ObserverOnly   bool      `json:"observer_only,omitempty"`
	Agent          string    `json:"agent,omitempty"`
	PlanError      string    `json:"plan_error,omitempty"`
	HeadCovered    *bool     `json:"head_covered_by_merged_pr,omitempty"`
}
type Task struct {
	Key          string     `json:"key"`
	ID           string     `json:"id"`
	Intent       string     `json:"intent"`
	Decision     Decision   `json:"decision"`
	PRReferences []string   `json:"pr_references"`
	Checkouts    []Checkout `json:"checkouts"`
	LivePanes    []string   `json:"live_panes"`
}
type Derived struct {
	HostCatalog     string              `json:"-"`
	Version         int                 `json:"version"`
	CatalogRevision uint64              `json:"catalog_revision"`
	Intent          string              `json:"intent"`
	Decision        Decision            `json:"decision"`
	Section         string              `json:"section"`
	Reasons         []string            `json:"reasons"`
	Diagnostics     []string            `json:"diagnostics,omitempty"`
	InputError      string              `json:"input_error,omitempty"`
	Tasks           []Task              `json:"tasks"`
	RuntimeKnown    bool                `json:"runtime_known"`
	RuntimeAt       time.Time           `json:"runtime_observed_at"`
	FactMaxAge      string              `json:"fact_max_age"`
	Unregistered    []UnregisteredAgent `json:"unregistered_agents,omitempty"`
}

// Future derived versions may have completely different task shapes. Keep the
// version visible without decoding those structures using today's schema.
func (d *Derived) UnmarshalJSON(b []byte) error {
	var header struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(b, &header); err != nil {
		return err
	}
	if header.Version != 1 {
		var scope struct {
			HostCatalog string `json:"host_catalog"`
		}
		if err := json.Unmarshal(b, &scope); err != nil {
			return err
		}
		*d = Derived{Version: header.Version, HostCatalog: scope.HostCatalog}
		return nil
	}
	type plain Derived
	var value plain
	if err := json.Unmarshal(b, &value); err != nil {
		return err
	}
	*d = Derived(value)
	var scope struct {
		HostCatalog string `json:"host_catalog"`
	}
	if err := json.Unmarshal(b, &scope); err != nil {
		return err
	}
	d.HostCatalog = scope.HostCatalog
	return nil
}

type UnregisteredAgent struct {
	TaskID   string `json:"task_id"`
	Pane     string `json:"pane"`
	Agent    string `json:"agent"`
	Checkout string `json:"checkout"`
}

type Stream struct {
	Key           string    `json:"key"`
	ID            string    `json:"id,omitempty"`
	Name          string    `json:"name"`
	Repo          string    `json:"repo"`
	Source        string    `json:"source"`
	SourceMode    string    `json:"source_mode"`
	HostScoped    bool      `json:"host_scoped,omitempty"`
	ModifiedAt    time.Time `json:"modified_at"`
	Stage         string    `json:"stage"`
	DeclaredNeeds string    `json:"declared_needs"`
	Next          string    `json:"next"`
	Needs         string    `json:"needs"`
	Warning       string    `json:"warning,omitempty"`
	Panes         []string  `json:"panes"`
	Derived       *Derived  `json:"derived,omitempty"`
	PRs           []PR      `json:"prs"`
	LivePanes     []string  `json:"live_panes"`
}
type Observation struct {
	HostCatalog    string `json:"-"`
	Name           string `json:"agent_name"`
	Role           string `json:"role_from_name"`
	Kind           string `json:"agent_kind"`
	Pane           string `json:"pane"`
	Workspace      string `json:"workspace"`
	WorkspaceLabel string `json:"workspace_label"`
	Cwd            string `json:"cwd"`
	Repository     string `json:"repository,omitempty"`
	Checkout       string `json:"checkout,omitempty"`
	TaskID         string `json:"task_id,omitempty"`
	WorkstreamID   string `json:"workstream_id,omitempty"`
	RoleSource     string `json:"role_source,omitempty"`
	PlanSource     string `json:"plan_source,omitempty"`
	TaskKey        string `json:"task_key,omitempty"`
	CatalogError   string `json:"catalog_error,omitempty"`
	Status         string `json:"herdr_status"`
	Context        string `json:"context_used,omitempty"`
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model_id,omitempty"`
	Plan           *Plan  `json:"plan,omitempty"`
	Git            *Git   `json:"git,omitempty"`
	PR             *PR    `json:"pr,omitempty"`
	PRLookup       string `json:"pr_lookup"`
}

func (o *Observation) UnmarshalJSON(b []byte) error {
	type plain Observation
	var value struct {
		plain
		Host string `json:"host_catalog"`
	}
	if err := json.Unmarshal(b, &value); err != nil {
		return err
	}
	*o = Observation(value.plain)
	o.HostCatalog = value.Host
	return nil
}

type Assessment struct {
	Status    string    `json:"status"`
	At        time.Time `json:"assessed_at"`
	Model     string    `json:"model"`
	WaitingOn string    `json:"waiting_on,omitempty"`
	Finished  *float64  `json:"finished,omitempty"`
	Stuck     *float64  `json:"stuck,omitempty"`
	Attention *float64  `json:"attention_score,omitempty"`
}
type BoardAgent struct {
	Observation Observation `json:"observation"`
	Assessment  Assessment  `json:"assessment"`
	Meta        struct {
		At time.Time `json:"collected_at"`
	} `json:"meta"`
}
type Board struct {
	Server  string       `json:"server,omitempty"`
	Streams []Stream     `json:"workstreams"`
	Agents  []BoardAgent `json:"agents"`
	Sweep   struct {
		At time.Time `json:"at"`
	} `json:"sweep"`
}

// hideHostScope replaces only the producer's known host catalog path in
// host-scope diagnostics. No global regex scrub guesses which paths are safe.
func hideHostScope(s *Stream) {
	path := s.Derived.HostCatalog
	redact := func(v string) string { return strings.ReplaceAll(v, path, "[host catalog]") }
	s.Source = ""
	s.Repo = ""
	s.Warning = redact(s.Warning)
	s.Stage = redact(s.Stage)
	s.Next = redact(s.Next)
	s.Needs = redact(s.Needs)
	s.DeclaredNeeds = redact(s.DeclaredNeeds)
	d := s.Derived
	d.InputError = redact(d.InputError)
	for i := range d.Reasons {
		d.Reasons[i] = redact(d.Reasons[i])
	}
	for i := range d.Diagnostics {
		d.Diagnostics[i] = redact(d.Diagnostics[i])
	}
	d.Decision.Reason = redact(d.Decision.Reason)
	d.Decision.Reference = redact(d.Decision.Reference)
	for i := range d.Tasks {
		t := &d.Tasks[i]
		t.Decision.Reason = redact(t.Decision.Reason)
		t.Decision.Reference = redact(t.Decision.Reference)
		for j := range t.Checkouts {
			t.Checkouts[j].PlanError = redact(t.Checkouts[j].PlanError)
		}
	}
}

func ParseBoard(r io.Reader) (Board, error) {
	var out Board
	b, err := io.ReadAll(io.LimitReader(r, MaxDocument+1))
	if err != nil {
		return out, err
	}
	if len(b) > MaxDocument {
		return out, fmt.Errorf("board output exceeds 8 MiB")
	}
	// Presence is significant: {} or an RPC error isn't an empty board.
	var shape map[string]json.RawMessage
	if err = json.Unmarshal(b, &shape); err != nil {
		return out, fmt.Errorf("invalid board JSON: %w", err)
	}
	for _, k := range []string{"workstreams", "agents", "sweep"} {
		if _, ok := shape[k]; !ok {
			return out, fmt.Errorf("board JSON missing %s", k)
		}
	}
	if string(shape["sweep"]) == "null" {
		return out, fmt.Errorf("board sweep must be an object")
	}
	if err = json.Unmarshal(b, &out); err != nil {
		return Board{}, fmt.Errorf("invalid board contract: %w", err)
	}
	var streamShape []json.RawMessage
	if err := json.Unmarshal(shape["workstreams"], &streamShape); err != nil {
		return Board{}, err
	}
	for _, row := range streamShape {
		if len(row) == 0 || row[0] != '{' {
			return Board{}, fmt.Errorf("board workstream must be an object")
		}
	}
	streams := make([]Stream, 0, len(out.Streams))
	seen := map[string]bool{}
	for _, s := range out.Streams {
		session := s.SourceMode == "coordination" || s.SourceMode == "standalone" || s.SourceMode == "unmapped"
		// Old unmapped/unfiled rows have never been declarations.
		if s.SourceMode == "unmapped" || s.ID == "" && s.Repo == "" && s.Source == "" && s.SourceMode == "" {
			if s.Name == "" {
				return Board{}, fmt.Errorf("board workstream identity missing")
			}
			continue
		}
		identity := s.ID
		if identity == "" {
			if out.Server != "" || s.Derived != nil && s.Derived.HostCatalog != "" {
				identity = s.Name
			} else {
				identity = s.Source + "\x00" + s.Name
			}
		}
		s.Key = "stream:" + Path(s.Repo) + "\x00" + identity
		if out.Server != "" || s.Derived != nil && s.Derived.HostCatalog != "" {
			// Host scope stays opaque even when a malformed modern payload lacks a server.
			// Source and repository scope are inputs to identity, not public keys.
			scope := s.Source
			if s.Derived != nil && s.Derived.HostCatalog != "" {
				scope = s.Derived.HostCatalog
			}
			sum := sha256.Sum256([]byte(scope + "\x00" + Path(s.Repo)))
			s.Key = "stream:scope:" + hex.EncodeToString(sum[:]) + "\x00" + identity
		}
		if session {
			// Session display labels are not identities. Pane IDs include their
			// workspace; rows without a stable pane cannot be independently selected.
			if len(s.Panes) == 0 {
				continue
			}
			panes := append([]string(nil), s.Panes...)
			sort.Strings(panes)
			id, _ := json.Marshal([]any{s.SourceMode, panes})
			s.Key = "session:" + string(id)
		}
		if seen[s.Key] {
			if session {
				continue
			}
			return Board{}, fmt.Errorf("duplicate board workstream identity")
		}
		seen[s.Key] = true
		if s.Derived != nil && s.Derived.HostCatalog != "" {
			hideHostScope(&s)
		}
		s.LivePanes = []string{}
		if s.Derived != nil {
			s.HostScoped = s.Derived.HostCatalog != "" || s.Derived.Version != 1 && s.SourceMode == "catalog"
			if s.Derived.Version != 1 {
				s.Warning = strings.TrimSpace(s.Warning + " Unsupported derived version; task projection is not consumed.")
				s.Derived.Tasks = nil
			} else {
				for i := range s.Derived.Tasks {
					t := &s.Derived.Tasks[i]
					t.Key = s.Key + "\x00task:" + t.ID
					if t.ID == "" || seen[t.Key] {
						return Board{}, fmt.Errorf("missing or duplicate board task identity")
					}
					seen[t.Key] = true
					t.LivePanes = []string{}
					distinct := make([]Checkout, 0, len(t.Checkouts))
					for _, c := range t.Checkouts {
						duplicate := false
						for _, old := range distinct {
							if reflect.DeepEqual(old, c) {
								duplicate = true
								break
							}
						}
						if !duplicate {
							distinct = append(distinct, c)
						}
					}
					t.Checkouts = distinct
					for k := range t.Checkouts {
						c := &t.Checkouts[k]
						if out.Server != "" || c.Server != "" {
							identity := []string{s.Key, t.ID, Path(c.Path), c.Server}
							if c.ObserverOnly {
								identity = append(identity, "reviewer", c.Agent)
							} else {
								identity = append(identity, "owner", c.RegisteredPlan)
							}
							encoded, _ := json.Marshal(identity)
							c.Key = string(encoded)
						}
					}
				}
			}
		}
		streams = append(streams, s)
	}
	out.Streams = streams
	if out.Agents == nil {
		out.Agents = []BoardAgent{}
	}
	// Host paths may appear in catalog errors before the producer assigns an
	// observation's host_catalog. Gather every known host scope from the parsed
	// board, and redact only those exact values in public diagnostics.
	hostPaths := map[string]bool{}
	for _, s := range out.Streams {
		if s.Derived != nil && s.Derived.HostCatalog != "" {
			hostPaths[s.Derived.HostCatalog] = true
		}
	}
	orderedHostPaths := make([]string, 0, len(hostPaths))
	for path := range hostPaths {
		orderedHostPaths = append(orderedHostPaths, path)
	}
	sort.Slice(orderedHostPaths, func(i, j int) bool { return len(orderedHostPaths[i]) > len(orderedHostPaths[j]) })
	panes := map[string]bool{}
	for i := range out.Agents {
		pane := out.Agents[i].Observation.Pane
		if pane == "" || panes[pane] {
			return Board{}, fmt.Errorf("missing or duplicate board agent pane identity")
		}
		panes[pane] = true
		o := &out.Agents[i].Observation
		originalError := strings.ToLower(strings.TrimSpace(o.CatalogError))
		for _, path := range orderedHostPaths {
			o.CatalogError = strings.ReplaceAll(o.CatalogError, path, "[host catalog]")
		}
		if o.HostCatalog != "" {
			o.CatalogError = strings.ReplaceAll(o.CatalogError, o.HostCatalog, "[host catalog]")
		}
		// An unscoped read/consistency error can mention an unknown host path.
		// Keep the failure class, not the arbitrary filesystem error text.
		if o.HostCatalog == "" {
			switch {
			case strings.HasPrefix(originalError, "host catalog unreadable:"):
				o.CatalogError = "host catalog unreadable"
			case strings.Contains(originalError, "host catalog"):
				o.CatalogError = "host catalog check failed"
			}
		}
		if o.TaskID != "" && o.WorkstreamID != "" {
			for _, s := range out.Streams {
				if s.ID != o.WorkstreamID || s.Derived == nil || s.Derived.Version != 1 {
					continue
				}
				if s.Derived.HostCatalog != "" {
					if o.HostCatalog != s.Derived.HostCatalog || Path(o.Repository) == "" {
						continue
					}
				} else if o.HostCatalog != "" || Path(o.Repository) != Path(s.Repo) {
					continue
				}
				for _, t := range s.Derived.Tasks {
					if t.ID == o.TaskID {
						o.TaskKey = t.Key
						break
					}
				}
			}
		}
		if out.Agents[i].Assessment.WaitingOn == "clay" {
			out.Agents[i].Assessment.WaitingOn = "you"
		}
	}
	return out, nil
}
