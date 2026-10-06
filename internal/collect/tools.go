package collect

import (
	"bufio"
	"encoding/json"
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
	Name      string `json:"name"`
	Status    string `json:"status"` // working, idle, blocked, done
	Cwd       string `json:"cwd"`
	Workspace string `json:"workspace"`
	Title     string `json:"title,omitempty"`
	Group     string `json:"group,omitempty"`
	Context   string `json:"context,omitempty"`
	PR        string `json:"pr,omitempty"` // e.g. "#522 ✗" as Herdr reports it
}

func ParseHerdrAgents(r io.Reader) ([]Agent, error) {
	var doc struct {
		Result struct {
			Agents []struct {
				Name        string            `json:"name"`
				AgentStatus string            `json:"agent_status"`
				Cwd         string            `json:"cwd"`
				WorkspaceID string            `json:"workspace_id"`
				Tokens      map[string]string `json:"tokens"`
			} `json:"agents"`
		} `json:"result"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, err
	}
	out := make([]Agent, 0, len(doc.Result.Agents))
	for _, a := range doc.Result.Agents {
		ag := Agent{Name: a.Name, Status: a.AgentStatus, Cwd: a.Cwd, Workspace: a.WorkspaceID,
			Title: a.Tokens["title"], Group: a.Tokens["hs_group"], Context: a.Tokens["context"], PR: a.Tokens["pr"]}
		if ag.Name == "" {
			ag.Name = strings.TrimSpace(ag.Title)
		}
		out = append(out, ag)
	}
	return out, nil
}

func HerdrAgents() ([]Agent, error) {
	b, err := run(10*time.Second, "herdr", "agent", "list")
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
	g.mu.Lock()
	g.toplevel[dir] = gitEntry{top, time.Now()}
	g.mu.Unlock()
	return top
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
