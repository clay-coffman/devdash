// Package server serves the dashboard page, the state API, and actions.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/clay-coffman/devdash/internal/state"
	"github.com/clay-coffman/devdash/internal/web"
)

type Server struct {
	c        *state.Collector
	auditLog string
	actionMu sync.Mutex // one action at a time
	mux      *http.ServeMux
}

func New(c *state.Collector) *Server {
	home, _ := os.UserHomeDir()
	s := &Server{c: c, auditLog: filepath.Join(home, ".local", "state", "devdash", "actions.log"), mux: http.NewServeMux()}
	static, _ := fs.Sub(web.FS, "static")
	s.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	s.mux.HandleFunc("/", s.index)
	s.mux.HandleFunc("/api/state", s.state)
	s.mux.HandleFunc("/api/history", s.history)
	s.mux.HandleFunc("/api/action", s.action)
	s.mux.HandleFunc("/api/logs", s.logs)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	s.mux.ServeHTTP(w, r)
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := web.FS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	if err := enc.Encode(v); err != nil {
		log.Printf("encode: %v", err)
	}
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.c.Snapshot())
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	hours, _ := strconv.ParseFloat(r.URL.Query().Get("hours"), 64)
	if hours <= 0 || hours > 24*8 {
		hours = 24
	}
	since := time.Now().Add(-time.Duration(hours * float64(time.Hour)))
	samples, err := s.c.History(since)
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error(), "samples": []any{}})
		return
	}
	checkouts, _ := s.c.CheckoutHistory(since)
	writeJSON(w, map[string]any{"samples": samples, "checkouts": checkouts})
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("container")
	ct, _, ok := s.c.FindContainer(name)
	if !ok {
		http.Error(w, "unknown container", http.StatusNotFound)
		return
	}
	tailN := r.URL.Query().Get("tail")
	if n, err := strconv.Atoi(tailN); err != nil || n < 1 || n > 5000 {
		tailN = "200"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "logs", "--tail", tailN, "--timestamps", ct.ID).CombinedOutput()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err != nil && len(out) == 0 {
		fmt.Fprintf(w, "docker logs failed: %v\n", err)
		return
	}
	w.Write(out)
}

type actionRequest struct {
	Type    string `json:"type"`   // stack.down, container.stop, process.kill
	Target  string `json:"target"` // project, container name, or pid
	Volumes bool   `json:"volumes,omitempty"`
	Signal  string `json:"signal,omitempty"` // TERM (default) or KILL
}

type actionResult struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func (s *Server) action(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("X-Devdash-Token") != s.c.Token {
		http.Error(w, "missing or stale token; reload the page", http.StatusForbidden)
		return
	}
	var req actionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	detail, err := s.perform(r.Context(), req)
	s.audit(req, detail, err)
	if err != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
		writeJSON(w, actionResult{OK: false, Detail: err.Error()})
		return
	}
	writeJSON(w, actionResult{OK: true, Detail: detail})
}

var errForeign = errors.New("refused: project is foreign (its working directory is outside your checkouts)")

func (s *Server) perform(ctx context.Context, req actionRequest) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	switch req.Type {
	case "stack.down":
		if req.Target == "" || strings.HasPrefix(req.Target, "-") {
			return "", errors.New("bad project name")
		}
		wd, ok := s.c.ProjectWorkingDir(req.Target)
		if !ok {
			return "", fmt.Errorf("unknown project %q", req.Target)
		}
		if s.c.Class(req.Target, wd) == "foreign" {
			return "", errForeign
		}
		args := []string{"compose", "-p", req.Target, "down", "--remove-orphans"}
		if req.Volumes {
			args = append(args, "-v")
		}
		out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("docker compose down: %v: %s", err, tail(out))
		}
		s.c.RefreshDocker()
		return "docker compose -p " + req.Target + " down" + map[bool]string{true: " -v", false: ""}[req.Volumes], nil
	case "container.stop":
		ct, class, ok := s.c.FindContainer(req.Target)
		if !ok {
			return "", fmt.Errorf("unknown container %q", req.Target)
		}
		if class == "foreign" {
			return "", errForeign
		}
		out, err := exec.CommandContext(ctx, "docker", "stop", ct.ID).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("docker stop: %v: %s", err, tail(out))
		}
		return "stopped " + ct.Name, nil
	case "process.kill":
		pid, err := strconv.Atoi(req.Target)
		if err != nil || pid <= 1 {
			return "", errors.New("bad pid")
		}
		if pid == os.Getpid() {
			return "", errors.New("refused: that is the dashboard itself")
		}
		p, ok := s.c.FindProcess(pid)
		if !ok {
			return "", fmt.Errorf("pid %d is not one of your processes (or has already exited)", pid)
		}
		sig, sigName := syscall.SIGTERM, "SIGTERM"
		if strings.EqualFold(req.Signal, "KILL") {
			sig, sigName = syscall.SIGKILL, "SIGKILL"
		}
		if err := kill(pid, sig); err != nil {
			return "", fmt.Errorf("kill %d: %v", pid, err)
		}
		return fmt.Sprintf("sent %s to %d (%s)", sigName, pid, p.Name), nil
	case "checkout.stop":
		co, ok := s.c.FindCheckout(req.Target)
		if !ok {
			return "", fmt.Errorf("unknown checkout %q", req.Target)
		}
		if co.Working > 0 {
			return "", fmt.Errorf("refused: %d agent(s) in %s are working", co.Working, co.Display)
		}
		var parts []string
		for _, st := range co.Stacks {
			if st.Class == "foreign" {
				continue
			}
			if out, err := exec.CommandContext(ctx, "docker", "compose", "-p", st.Project, "down", "--remove-orphans").CombinedOutput(); err != nil {
				return strings.Join(parts, "; "), fmt.Errorf("compose down %s: %v: %s", st.Project, err, tail(out))
			}
			parts = append(parts, "down "+st.Project)
		}
		killed, spared := 0, 0
		for _, p := range co.Processes {
			if p.Name == "pi" || p.PID == os.Getpid() {
				spared++
				continue
			}
			if err := kill(p.PID, syscall.SIGTERM); err == nil {
				killed++
			}
		}
		parts = append(parts, fmt.Sprintf("SIGTERM to %d processes", killed))
		if spared > 0 {
			parts = append(parts, fmt.Sprintf("%d pi agent(s) left to Herdr", spared))
		}
		return strings.Join(parts, "; "), nil
	case "docker.prune_build_cache":
		out, err := exec.CommandContext(ctx, "docker", "builder", "prune", "-f").CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("builder prune: %v: %s", err, tail(out))
		}
		s.c.RefreshDocker()
		return "docker builder prune: " + lastLine(out), nil
	case "docker.prune_volumes":
		projects := s.c.VolumeOnlyProjects()
		if len(projects) == 0 {
			return "", errors.New("no volume-only projects")
		}
		removed, kept := 0, 0
		for _, p := range projects {
			ids, err := exec.CommandContext(ctx, "docker", "volume", "ls", "-q", "--filter", "label=com.docker.compose.project="+p).Output()
			if err != nil {
				continue
			}
			for _, v := range strings.Fields(string(ids)) {
				if err := exec.CommandContext(ctx, "docker", "volume", "rm", v).Run(); err != nil {
					kept++ // in use or otherwise refused by docker
				} else {
					removed++
				}
			}
		}
		s.c.RefreshDocker()
		return fmt.Sprintf("removed %d volumes from %d projects (%d refused by docker)", removed, len(projects), kept), nil
	default:
		return "", fmt.Errorf("unknown action %q", req.Type)
	}
}

func lastLine(b []byte) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return lines[len(lines)-1]
}

func tail(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 400 {
		s = "…" + s[len(s)-400:]
	}
	return s
}

func (s *Server) audit(req actionRequest, detail string, err error) {
	if mkErr := os.MkdirAll(filepath.Dir(s.auditLog), 0o700); mkErr != nil {
		log.Printf("audit: %v", mkErr)
		return
	}
	f, openErr := os.OpenFile(s.auditLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if openErr != nil {
		log.Printf("audit: %v", openErr)
		return
	}
	defer f.Close()
	outcome := "ok " + detail
	if err != nil {
		outcome = "refused " + err.Error()
	}
	fmt.Fprintf(f, "%s %s %s volumes=%v signal=%s | %s\n", time.Now().UTC().Format(time.RFC3339),
		req.Type, req.Target, req.Volumes, req.Signal, outcome)
	log.Printf("action %s %s: %s", req.Type, req.Target, outcome)
}
