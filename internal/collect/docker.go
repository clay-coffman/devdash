package collect

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"time"
)

// Container is one `docker ps -a` row joined with `docker stats`.
type Container struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	State      string  `json:"state"`  // running, exited, ...
	Status     string  `json:"status"` // "Up 7 minutes (healthy)"
	Health     string  `json:"health,omitempty"`
	Image      string  `json:"image"`
	Project    string  `json:"project,omitempty"`
	WorkingDir string  `json:"working_dir,omitempty"`
	Service    string  `json:"service,omitempty"`
	Ports      []int   `json:"ports,omitempty"` // host-published ports
	MemUsage   int64   `json:"mem_usage"`
	MemLimit   int64   `json:"mem_limit"`
	CPU        float64 `json:"cpu"`
	PIDs       int     `json:"pids"`
}

const psFormat = `{{.ID}}\t{{.Names}}\t{{.State}}\t{{.Status}}\t{{.Image}}\t{{.Label "com.docker.compose.project"}}\t{{.Label "com.docker.compose.project.working_dir"}}\t{{.Label "com.docker.compose.service"}}\t{{.Ports}}`

func ParseDockerPS(r io.Reader) []Container {
	var out []Container
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 9 {
			continue
		}
		c := Container{ID: f[0], Name: f[1], State: f[2], Status: f[3], Image: f[4],
			Project: f[5], WorkingDir: f[6], Service: f[7]}
		if i := strings.Index(c.Status, "("); i >= 0 {
			c.Health = strings.Trim(c.Status[i:], "()")
		}
		c.Ports = parsePublishedPorts(f[8])
		out = append(out, c)
	}
	return out
}

// parsePublishedPorts extracts host ports from "127.0.0.1:19091->8080/tcp,
// 4510-4559/tcp, 5678/tcp". Unpublished container ports are skipped.
func parsePublishedPorts(s string) []int {
	var ports []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		host, _, ok := strings.Cut(part, "->")
		if !ok {
			continue
		}
		i := strings.LastIndexByte(host, ':')
		if i < 0 {
			continue
		}
		var p int
		for _, ch := range host[i+1:] {
			if ch < '0' || ch > '9' {
				p = -1
				break
			}
			p = p*10 + int(ch-'0')
		}
		if p > 0 {
			ports = append(ports, p)
		}
	}
	if len(ports) > 1 {
		ports = dedupeInts(ports)
	}
	return ports
}

type statsRow struct {
	ID       string `json:"ID"`
	Name     string `json:"Name"`
	MemUsage string `json:"MemUsage"`
	CPUPerc  string `json:"CPUPerc"`
	PIDs     string `json:"PIDs"`
}

// ParseDockerStats reads `docker stats --no-stream --format '{{json .}}'`.
func ParseDockerStats(r io.Reader) map[string]Container {
	out := map[string]Container{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var row statsRow
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			continue
		}
		c := Container{ID: row.ID, Name: row.Name, CPU: ParsePercent(row.CPUPerc)}
		use, lim, _ := strings.Cut(row.MemUsage, "/")
		c.MemUsage = ParseBytes(use)
		c.MemLimit = ParseBytes(lim)
		for _, ch := range row.PIDs {
			if ch >= '0' && ch <= '9' {
				c.PIDs = c.PIDs*10 + int(ch-'0')
			}
		}
		out[row.Name] = c
	}
	return out
}

// MergeStats copies usage numbers into the ps rows by name.
func MergeStats(ps []Container, stats map[string]Container) {
	for i := range ps {
		if s, ok := stats[ps[i].Name]; ok {
			ps[i].MemUsage, ps[i].MemLimit, ps[i].CPU, ps[i].PIDs = s.MemUsage, s.MemLimit, s.CPU, s.PIDs
		}
	}
}

// DiskUsage is one row of `docker system df`.
type DiskUsage struct {
	Type        string `json:"type"`
	Total       int    `json:"total"`
	Active      int    `json:"active"`
	Size        int64  `json:"size"`
	Reclaimable int64  `json:"reclaimable"`
}

func ParseDockerDF(r io.Reader) []DiskUsage {
	var out []DiskUsage
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		var row struct {
			Type, TotalCount, Active, Size, Reclaimable string
		}
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			continue
		}
		d := DiskUsage{Type: row.Type, Size: ParseBytes(row.Size), Reclaimable: ParseBytes(row.Reclaimable)}
		d.Total = atoi(row.TotalCount)
		d.Active = atoi(row.Active)
		out = append(out, d)
	}
	return out
}

// ParseVolumeProjects reads `docker volume ls --format '{{.Name}}\t{{.Label
// "com.docker.compose.project"}}'` and counts volumes per project.
func ParseVolumeProjects(r io.Reader) map[string]int {
	out := map[string]int{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		_, project, ok := strings.Cut(sc.Text(), "\t")
		if !ok || project == "" {
			continue
		}
		out[project]++
	}
	return out
}

func atoi(s string) int {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			break
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

// run executes a command with a timeout and returns stdout.
func run(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

func DockerPS() ([]Container, error) {
	b, err := run(10*time.Second, "docker", "ps", "-a", "--format", psFormat)
	if err != nil {
		return nil, err
	}
	return ParseDockerPS(strings.NewReader(string(b))), nil
}

func DockerStats() (map[string]Container, error) {
	b, err := run(20*time.Second, "docker", "stats", "--no-stream", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	return ParseDockerStats(strings.NewReader(string(b))), nil
}

func DockerDF() ([]DiskUsage, map[string]int, error) {
	b, err := run(30*time.Second, "docker", "system", "df", "--format", "{{json .}}")
	if err != nil {
		return nil, nil, err
	}
	df := ParseDockerDF(strings.NewReader(string(b)))
	v, err := run(10*time.Second, "docker", "volume", "ls", "--format", `{{.Name}}\t{{.Label "com.docker.compose.project"}}`)
	if err != nil {
		return df, nil, err
	}
	return df, ParseVolumeProjects(strings.NewReader(string(v))), nil
}
