package collect

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Agent is one row of `herdr agent list`.
type Agent struct {
	Name       string `json:"name"`
	Status     string `json:"status"` // working, idle, blocked, done
	Cwd        string `json:"cwd"`    // effective foreground location, not necessarily the pane shell cwd
	Workspace  string `json:"workspace"`
	Title      string `json:"title,omitempty"`
	Group      string `json:"group,omitempty"`
	Context    string `json:"context,omitempty"`
	PR         string `json:"pr,omitempty"` // e.g. "#522 ✗" as Herdr reports it
	Pane       string `json:"pane"`
	Kind       string `json:"kind,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Checkout   string `json:"checkout,omitempty"`
	Repository string `json:"repository,omitempty"`
}

func ParseHerdrAgents(r io.Reader) ([]Agent, error) {
	var doc struct {
		Error  json.RawMessage `json:"error"`
		Result *struct {
			Agents *[]struct {
				PaneID        string            `json:"pane_id"`
				Kind          string            `json:"agent"`
				Name          string            `json:"name"`
				AgentStatus   string            `json:"agent_status"`
				Cwd           string            `json:"cwd"`
				ForegroundCwd string            `json:"foreground_cwd"`
				WorkspaceID   string            `json:"workspace_id"`
				Tokens        map[string]string `json:"tokens"`
			} `json:"agents"`
		} `json:"result"`
	}
	dec := json.NewDecoder(io.LimitReader(r, 8<<20+1))
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if doc.Result == nil || doc.Result.Agents == nil || (len(doc.Error) != 0 && string(doc.Error) != "null") {
		return nil, fmt.Errorf("herdr response missing agent result or reports an RPC error")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("trailing herdr response data")
	}
	out := make([]Agent, 0, len(*doc.Result.Agents))
	seen := map[string]bool{}
	for _, a := range *doc.Result.Agents {
		if a.PaneID == "" || seen[a.PaneID] {
			return nil, fmt.Errorf("missing or duplicate herdr pane identity")
		}
		seen[a.PaneID] = true
		// Normalize location once so Work membership, fallback, repository and
		// Resources attribution cannot disagree about the foreground agent.
		cwd := a.ForegroundCwd
		if cwd == "" {
			cwd = a.Cwd
		}
		ag := Agent{Name: a.Name, Status: a.AgentStatus, Cwd: cwd, Workspace: a.WorkspaceID, Pane: a.PaneID, Kind: a.Kind, Provider: a.Tokens["provider"],
			Title: a.Tokens["title"], Group: a.Tokens["hs_group"], Context: a.Tokens["context"], PR: a.Tokens["pr"]}
		if ag.Name == "" {
			ag.Name = strings.TrimSpace(ag.Title)
		}
		out = append(out, ag)
	}
	return out, nil
}

func HerdrAgents() ([]Agent, error) { return HerdrAgentsSocket("") }

func HerdrAgentsSocket(socket string) ([]Agent, error) {
	b, err := RunBoundedSocket(10*time.Second, 8<<20, socket, "herdr", "agent", "list")
	if err != nil {
		return nil, err
	}
	return ParseHerdrAgents(strings.NewReader(string(b)))
}

// StackClass is compose-stack-reaper's verdict for a Compose project.
type StackClass struct {
	Project    string
	Class      string // live, detached, orphaned, foreign
	WorkingDir string
}

var reaperLine = regexp.MustCompile(`^(live|detached|orphaned|foreign)\s+(\S+)\s+\((.*)\)\s*$`)

// ParseReaper reads the dry-run report. It also returns the projects that
// exist only as volumes.
func ParseReaper(r io.Reader) (map[string]StackClass, []string) {
	classes := map[string]StackClass{}
	var volumeOnly []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if m := reaperLine.FindStringSubmatch(line); m != nil {
			classes[m[2]] = StackClass{Project: m[2], Class: m[1], WorkingDir: m[3]}
			continue
		}
		if strings.HasPrefix(line, "volumes") {
			if _, names, ok := strings.Cut(line, ": "); ok {
				volumeOnly = strings.Fields(names)
			}
		}
	}
	return classes, volumeOnly
}

func Reaper() (map[string]StackClass, []string, error) {
	path, err := exec.LookPath("compose-stack-reaper")
	if err != nil {
		return nil, nil, err
	}
	b, err := run(30*time.Second, path)
	if err != nil && len(b) == 0 {
		return nil, nil, err
	}
	c, v := ParseReaper(strings.NewReader(string(b)))
	return c, v, nil
}

// Sample is one line of devbox-mem-sample's log.
type Sample struct {
	T      time.Time `json:"t"`
	Used   int64     `json:"used"`
	Avail  int64     `json:"avail"`
	SwapPC int       `json:"swap_pct"`
	Herdr  int64     `json:"herdr"`
	Hapi   int64     `json:"hapi"`
	Pi     int       `json:"pi"`
	Load1  float64   `json:"load1"`
}

// ParseSamples reads lines like
// 2026-10-06T02:21:33Z mem_used=32259M avail=30655M swap=0% herdr_cg=28555M hapi=5/6691M pi=17 test_procs=0 load1=3.67
// skipping ALERT lines and anything older than since.
func ParseSamples(r io.Reader, since time.Time) []Sample {
	var out []Sample
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 || f[1] == "ALERT" {
			continue
		}
		t, err := time.Parse(time.RFC3339, f[0])
		if err != nil || t.Before(since) {
			continue
		}
		s := Sample{T: t}
		for _, kv := range f[1:] {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			switch k {
			case "mem_used":
				s.Used = ParseBytes(v)
			case "avail":
				s.Avail = ParseBytes(v)
			case "swap":
				s.SwapPC = atoi(strings.TrimSuffix(v, "%"))
			case "herdr_cg":
				s.Herdr = ParseBytes(v)
			case "hapi":
				if _, mb, ok := strings.Cut(v, "/"); ok {
					s.Hapi = ParseBytes(mb)
				}
			case "pi":
				s.Pi = atoi(v)
			case "load1":
				s.Load1, _ = strconv.ParseFloat(v, 64)
			}
		}
		out = append(out, s)
	}
	return out
}

func ReadSamples(path string, since time.Time) ([]Sample, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseSamples(f, since), nil
}

// Git caches toplevel and branch lookups, which are exec calls.
type Git struct {
	mu       sync.Mutex
	toplevel map[string]gitEntry
	branch   map[string]gitEntry
	// IsRoot reports whether dir is a worktree root (has .git). Replaceable
	// in tests. Branch lookups are skipped when BranchOf is set.
	IsRoot   func(dir string) bool
	BranchOf func(top string) string
}

type gitEntry struct {
	v string
	t time.Time
}

func NewGit() *Git {
	return &Git{
		toplevel: map[string]gitEntry{},
		branch:   map[string]gitEntry{},
		IsRoot: func(dir string) bool {
			_, err := os.Lstat(filepath.Join(dir, ".git"))
			return err == nil
		},
	}
}

// Toplevel returns the worktree root containing dir, or "" if none.
func (g *Git) Toplevel(dir string) string {
	if dir == "" {
		return ""
	}
	g.mu.Lock()
	e, ok := g.toplevel[dir]
	g.mu.Unlock()
	if ok && time.Since(e.t) < 5*time.Minute {
		return e.v
	}
	var top string
	// Cheap pre-check: walk up looking for .git before paying for an exec.
	for d := dir; ; d = filepath.Dir(d) {
		if g.IsRoot(d) {
			top = d
			break
		}
		if d == "/" || d == "." {
			break
		}
	}
	if top != "" {
		if canonical, err := filepath.EvalSymlinks(top); err == nil {
			top = canonical
		}
	}
	g.mu.Lock()
	g.toplevel[dir] = gitEntry{top, time.Now()}
	g.mu.Unlock()
	return top
}

// Repository resolves Git's own worktree common-directory pointer. It does not
// infer registration, branch ownership, or a task from a directory name. Unusual
// separate Git directories stay unknown rather than guessing a main checkout.
func (g *Git) Repository(top string) string {
	if top == "" {
		return ""
	}
	gitdir := filepath.Join(top, ".git")
	st, err := os.Stat(gitdir)
	if err != nil {
		return ""
	}
	if st.IsDir() {
		return top
	}
	b, err := os.ReadFile(gitdir)
	if err != nil {
		return ""
	}
	pointer := strings.TrimSpace(strings.TrimPrefix(string(b), "gitdir:"))
	if pointer == strings.TrimSpace(string(b)) || pointer == "" {
		return ""
	}
	if !filepath.IsAbs(pointer) {
		pointer = filepath.Join(top, pointer)
	}
	common, err := os.ReadFile(filepath.Join(pointer, "commondir"))
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(common))
	if !filepath.IsAbs(path) {
		path = filepath.Join(pointer, path)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil || filepath.Base(path) != ".git" {
		return ""
	}
	return filepath.Dir(path)
}

// Branch returns the branch name or "detached @ <sha>".
func (g *Git) Branch(top string) string {
	g.mu.Lock()
	e, ok := g.branch[top]
	g.mu.Unlock()
	if ok && time.Since(e.t) < 30*time.Second {
		return e.v
	}
	var out string
	if g.BranchOf != nil {
		out = g.BranchOf(top)
	} else if b, err := run(5*time.Second, "git", "-C", top, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		out = strings.TrimSpace(string(b))
		if out == "HEAD" {
			if s, err := run(5*time.Second, "git", "-C", top, "rev-parse", "--short", "HEAD"); err == nil {
				out = "detached @ " + strings.TrimSpace(string(s))
			}
		}
	}
	g.mu.Lock()
	g.branch[top] = gitEntry{out, time.Now()}
	g.mu.Unlock()
	return out
}
