// Package state joins everything the collectors know into one snapshot
// attributed per git checkout.
package state

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/clay-coffman/devdash/internal/collect"
)

type Stack struct {
	Project    string              `json:"project"`
	WorkingDir string              `json:"working_dir"`
	Class      string              `json:"class"` // live, detached, orphaned, foreign, unknown
	Containers []collect.Container `json:"containers"`
	Bytes      int64               `json:"bytes"`
	Running    int                 `json:"running"`
	Volumes    int                 `json:"volumes"`
}

type Checkout struct {
	Path           string            `json:"path"`
	Display        string            `json:"display"`
	Branch         string            `json:"branch,omitempty"`
	Agents         []collect.Agent   `json:"agents"`
	Processes      []collect.Process `json:"processes"`
	Stacks         []Stack           `json:"stacks"`
	ProcBytes      int64             `json:"proc_bytes"`
	ContainerBytes int64             `json:"container_bytes"`
	TotalBytes     int64             `json:"total_bytes"`
	Working        int               `json:"working"` // agents with status working
	Reclaim        Reclaim           `json:"reclaim"`
}

// Reclaim is the deterministic "can I stop this" verdict for a checkout.
type Reclaim struct {
	Score   int      `json:"score"` // 0 = never; higher = safer to stop
	Reasons []string `json:"reasons"`
}

// Candidate is one row of the Reclaim strip.
type Candidate struct {
	Path    string   `json:"path"`
	Display string   `json:"display"`
	Branch  string   `json:"branch,omitempty"`
	Bytes   int64    `json:"bytes"`
	Score   int      `json:"score"`
	Reasons []string `json:"reasons"`
}

type Port struct {
	Port int    `json:"port"`
	PID  int    `json:"pid"`
	Name string `json:"name"`
	Cwd  string `json:"cwd"`
}

type LivePoint struct {
	T       int64   `json:"t"` // unix seconds
	Used    int64   `json:"used"`
	Avail   int64   `json:"avail"`
	PSISome float64 `json:"psi_some"`
	CPU     float64 `json:"cpu"`
}

type VolumeProject struct {
	Project string `json:"project"`
	Volumes int    `json:"volumes"`
}

type State struct {
	Host    string         `json:"host"`
	Now     time.Time      `json:"now"`
	Uptime  float64        `json:"uptime"`
	Cores   int            `json:"cores"`
	NoSwap  bool           `json:"no_swap"`
	Mem     Memory         `json:"mem"`
	CPU     CPU            `json:"cpu"`
	Disks   []collect.Disk `json:"disks"`
	Buckets Buckets        `json:"buckets"`

	Checkouts    []Checkout  `json:"checkouts"`
	Unattributed Checkout    `json:"unattributed"`
	Reclaim      []Candidate `json:"reclaim"`
	Docker       Docker      `json:"docker"`
	Ports        []Port      `json:"ports"`
	Live         []LivePoint `json:"live"`
	Counts       Counts      `json:"counts"`
	Errors       []string    `json:"errors,omitempty"`
	Token        string      `json:"token"`
}

type Memory struct {
	collect.Memory
	Used int64            `json:"used"`
	PSI  collect.Pressure `json:"psi"`
}

type CPU struct {
	Percent float64          `json:"percent"`
	Load    collect.Load     `json:"load"`
	PSI     collect.Pressure `json:"psi"`
}

type Buckets struct {
	HerdrCgroup  int64 `json:"herdr_cgroup"`
	Containers   int64 `json:"containers"`
	OwnProcesses int64 `json:"own_processes"`
}

type Docker struct {
	DF         []collect.DiskUsage `json:"df"`
	VolumeOnly []VolumeProject     `json:"volume_only"`
	Available  bool                `json:"available"`
}

type Counts struct {
	Agents     int `json:"agents"`
	Working    int `json:"working"`
	Stacks     int `json:"stacks"`
	Containers int `json:"containers"`
	Processes  int `json:"processes"`
}

// Collector owns the caches and refresh loops.
type Collector struct {
	uid        int
	home       string
	host       string
	cores      int
	git        *collect.Git
	samplesLog string
	Token      string

	mu sync.Mutex
	// sampled every tick
	mem      collect.Memory
	memPSI   collect.Pressure
	cpuPSI   collect.Pressure
	cpuPct   float64
	load     collect.Load
	disks    []collect.Disk
	procs    []collect.Process
	herdrCg  int64
	uptime   float64
	live     []LivePoint
	prevCPU  collect.CPUTicks
	prevTick map[int]uint64
	prevAt   time.Time
	// slower caches
	containers []collect.Container
	dockerOK   bool
	agents     []collect.Agent
	classes    map[string]collect.StackClass
	volumeOnly []string
	df         []collect.DiskUsage
	volumes    map[string]int
	errs       map[string]string
}

func New(token string) *Collector {
	host, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	c := &Collector{
		uid:        os.Getuid(),
		home:       home,
		host:       host,
		cores:      cores(),
		git:        collect.NewGit(),
		samplesLog: filepath.Join(home, ".local", "state", "devbox-mem", "samples.log"),
		Token:      token,
		errs:       map[string]string{},
	}
	return c
}

func cores() int {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0
	}
	defer f.Close()
	b := make([]byte, 64*1024)
	n, _ := f.Read(b)
	count := 0
	for _, line := range strings.Split(string(b[:n]), "\n") {
		if strings.HasPrefix(line, "cpu") && len(line) > 3 && line[3] >= '0' && line[3] <= '9' {
			count++
		}
	}
	return count
}

// Run starts the refresh loops and blocks until stop is closed.
func (c *Collector) Run(stop <-chan struct{}) {
	go c.loop(stop, 5*time.Second, c.refreshDocker)
	go c.loop(stop, 5*time.Second, c.refreshHerdr)
	go c.loop(stop, 30*time.Second, c.refreshReaper)
	go c.loop(stop, 60*time.Second, c.refreshDF)
	go c.loop(stop, 30*time.Second, c.refreshDisks)
	go func() { // sampler starts after the first docker/herdr refresh has had a chance
		time.Sleep(15 * time.Second)
		c.loop(stop, 60*time.Second, c.sampleToLog)
	}()
	go c.loop(stop, time.Hour, c.trimLogs)
	c.loop(stop, 2*time.Second, c.sample)
}

func (c *Collector) loop(stop <-chan struct{}, every time.Duration, fn func()) {
	fn()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			fn()
		}
	}
}

func (c *Collector) setErr(key string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		delete(c.errs, key)
	} else {
		c.errs[key] = err.Error()
	}
}

func (c *Collector) sample() {
	now := time.Now()
	mem, err := collect.ReadMeminfo()
	c.setErr("meminfo", err)
	memPSI, _ := collect.ReadPressure("memory")
	cpuPSI, _ := collect.ReadPressure("cpu")
	load, _ := collect.ReadLoadavg()
	ticks, _ := collect.ReadStatCPU()
	var up float64
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			up, _ = strconv.ParseFloat(f[0], 64)
		}
	}
	herdr := collect.CgroupMemory(c.uid, "herdr.service")

	c.mu.Lock()
	dt := now.Sub(c.prevAt).Seconds()
	prevTick := c.prevTick
	prevCPU := c.prevCPU
	c.mu.Unlock()

	procs, newTicks, err := collect.ReadProcesses(c.uid, prevTick, dt)
	c.setErr("procs", err)

	c.mu.Lock()
	defer c.mu.Unlock()
	if prevCPU.Total != 0 {
		c.cpuPct = collect.CPUPercent(prevCPU, ticks)
	}
	c.prevCPU, c.prevTick, c.prevAt = ticks, newTicks, now
	c.mem, c.memPSI, c.cpuPSI, c.load, c.procs, c.herdrCg, c.uptime = mem, memPSI, cpuPSI, load, procs, herdr, up
	c.live = append(c.live, LivePoint{T: now.Unix(), Used: mem.Used(), Avail: mem.Available, PSISome: memPSI.Some10, CPU: c.cpuPct})
	if len(c.live) > 300 {
		c.live = c.live[len(c.live)-300:]
	}
}

func (c *Collector) refreshDocker() {
	ps, err := collect.DockerPS()
	c.setErr("docker", err)
	if err != nil {
		c.mu.Lock()
		c.dockerOK = false
		c.mu.Unlock()
		return
	}
	if stats, err := collect.DockerStats(); err == nil {
		collect.MergeStats(ps, stats)
	} else {
		c.setErr("docker stats", err)
	}
	c.mu.Lock()
	c.containers, c.dockerOK = ps, true
	c.mu.Unlock()
}

func (c *Collector) refreshHerdr() {
	ags, err := collect.HerdrAgents()
	c.setErr("herdr", err)
	if err != nil {
		return
	}
	c.mu.Lock()
	c.agents = ags
	c.mu.Unlock()
}

func (c *Collector) refreshReaper() {
	classes, vols, err := collect.Reaper()
	c.setErr("reaper", err)
	if err != nil {
		return
	}
	c.mu.Lock()
	c.classes, c.volumeOnly = classes, vols
	c.mu.Unlock()
}

func (c *Collector) refreshDF() {
	df, vols, err := collect.DockerDF()
	c.setErr("docker df", err)
	if err != nil {
		return
	}
	c.mu.Lock()
	c.df, c.volumes = df, vols
	c.mu.Unlock()
}

func (c *Collector) refreshDisks() {
	var disks []collect.Disk
	seen := map[string]bool{}
	for _, m := range []string{"/", c.home, "/var/lib/docker", "/tmp"} {
		d, err := collect.ReadDisk(m)
		if err != nil {
			continue
		}
		key := strconv.FormatInt(d.Total, 10) + "/" + strconv.FormatInt(d.Avail, 10)
		if seen[key] { // same filesystem mounted at both
			continue
		}
		seen[key] = true
		disks = append(disks, d)
	}
	c.mu.Lock()
	c.disks = disks
	c.mu.Unlock()
}

func (c *Collector) tilde(p string) string {
	if c.home != "" && strings.HasPrefix(p, c.home) {
		return "~" + strings.TrimPrefix(p, c.home)
	}
	return p
}

// Class returns the reaper's verdict for a project, or a conservative
// fallback based on the roots rule (anything outside $HOME is foreign).
func (c *Collector) Class(project, workingDir string) string {
	c.mu.Lock()
	cls, ok := c.classes[project]
	c.mu.Unlock()
	if ok {
		return cls.Class
	}
	if workingDir == "" || c.home == "" || !strings.HasPrefix(workingDir, c.home+"/") {
		return "foreign"
	}
	return "unknown"
}

// Snapshot joins the caches into a State.
func (c *Collector) Snapshot() State {
	c.mu.Lock()
	s := State{
		Host: c.host, Now: time.Now(), Uptime: c.uptime, Cores: c.cores,
		NoSwap: c.mem.SwapTotal == 0,
		Mem:    Memory{Memory: c.mem, Used: c.mem.Used(), PSI: c.memPSI},
		CPU:    CPU{Percent: c.cpuPct, Load: c.load, PSI: c.cpuPSI},
		Disks:  append([]collect.Disk(nil), c.disks...),
		Live:   append([]LivePoint(nil), c.live...),
		Token:  c.Token,
	}
	s.Buckets.HerdrCgroup = c.herdrCg
	procs := append([]collect.Process(nil), c.procs...)
	containers := append([]collect.Container(nil), c.containers...)
	agents := append([]collect.Agent(nil), c.agents...)
	classes := c.classes
	volumeOnly := c.volumeOnly
	volumes := c.volumes
	s.Docker.DF = append([]collect.DiskUsage(nil), c.df...)
	s.Docker.Available = c.dockerOK
	for k, v := range c.errs {
		s.Errors = append(s.Errors, k+": "+v)
	}
	sort.Strings(s.Errors)
	c.mu.Unlock()

	byPath := map[string]*Checkout{}
	get := func(top string) *Checkout {
		if top == "" {
			return &s.Unattributed
		}
		co, ok := byPath[top]
		if !ok {
			co = &Checkout{Path: top, Display: c.tilde(top), Branch: c.git.Branch(top)}
			byPath[top] = co
		}
		return co
	}
	s.Unattributed.Display = "Unattributed"

	for _, p := range procs {
		co := get(c.git.Toplevel(p.Cwd))
		co.Processes = append(co.Processes, p)
		co.ProcBytes += p.RSS
		s.Buckets.OwnProcesses += p.RSS
		for _, port := range p.Ports {
			s.Ports = append(s.Ports, Port{Port: port, PID: p.PID, Name: p.Name, Cwd: c.tilde(p.Cwd)})
		}
	}
	for _, a := range agents {
		co := get(c.git.Toplevel(a.Cwd))
		co.Agents = append(co.Agents, a)
		if a.Status == "working" {
			co.Working++
			s.Counts.Working++
		}
	}
	s.Counts.Agents = len(agents)
	s.Counts.Processes = len(procs)

	stacks := map[string]*Stack{}
	var order []string
	for _, ct := range containers {
		if ct.Project == "" {
			continue
		}
		st, ok := stacks[ct.Project]
		if !ok {
			st = &Stack{Project: ct.Project, WorkingDir: ct.WorkingDir, Volumes: volumes[ct.Project]}
			stacks[ct.Project] = st
			order = append(order, ct.Project)
		}
		st.Containers = append(st.Containers, ct)
		st.Bytes += ct.MemUsage
		if ct.State == "running" {
			st.Running++
		}
		s.Buckets.Containers += ct.MemUsage
		s.Counts.Containers++
	}
	for _, name := range order {
		st := stacks[name]
		st.Class = c.Class(name, st.WorkingDir)
		if cls, ok := classes[name]; ok && cls.WorkingDir != "" {
			st.WorkingDir = cls.WorkingDir
		}
		sort.Slice(st.Containers, func(i, j int) bool { return st.Containers[i].MemUsage > st.Containers[j].MemUsage })
		top := ""
		if st.Class != "orphaned" {
			top = c.git.Toplevel(st.WorkingDir)
		}
		co := get(top)
		co.Stacks = append(co.Stacks, *st)
		co.ContainerBytes += st.Bytes
	}
	s.Counts.Stacks = len(order)
	for _, name := range volumeOnly {
		s.Docker.VolumeOnly = append(s.Docker.VolumeOnly, VolumeProject{Project: name, Volumes: volumes[name]})
	}

	for _, co := range byPath {
		finish(co)
		co.Reclaim = scoreReclaim(co)
		s.Checkouts = append(s.Checkouts, *co)
	}
	finish(&s.Unattributed)
	s.Reclaim = candidates(s.Checkouts)
	sort.Slice(s.Checkouts, func(i, j int) bool {
		if s.Checkouts[i].TotalBytes != s.Checkouts[j].TotalBytes {
			return s.Checkouts[i].TotalBytes > s.Checkouts[j].TotalBytes
		}
		return s.Checkouts[i].Path < s.Checkouts[j].Path
	})
	sort.Slice(s.Ports, func(i, j int) bool { return s.Ports[i].Port < s.Ports[j].Port })
	return s
}

func finish(co *Checkout) {
	co.TotalBytes = co.ProcBytes + co.ContainerBytes
	sort.Slice(co.Processes, func(i, j int) bool { return co.Processes[i].RSS > co.Processes[j].RSS })
	sort.Slice(co.Stacks, func(i, j int) bool { return co.Stacks[i].Bytes > co.Stacks[j].Bytes })
	sort.Slice(co.Agents, func(i, j int) bool { return co.Agents[i].Name < co.Agents[j].Name })
	if co.Agents == nil {
		co.Agents = []collect.Agent{}
	}
	if co.Processes == nil {
		co.Processes = []collect.Process{}
	}
	if co.Stacks == nil {
		co.Stacks = []Stack{}
	}
}

// FindProcess reports whether pid is one of the caller's processes in the
// last sample, and its name.
func (c *Collector) FindProcess(pid int) (collect.Process, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.procs {
		if p.PID == pid {
			return p, true
		}
	}
	return collect.Process{}, false
}

// FindContainer returns the container and its project's class.
func (c *Collector) FindContainer(name string) (collect.Container, string, bool) {
	c.mu.Lock()
	var found collect.Container
	ok := false
	for _, ct := range c.containers {
		if ct.Name == name {
			found, ok = ct, true
			break
		}
	}
	c.mu.Unlock()
	if !ok {
		return found, "", false
	}
	return found, c.Class(found.Project, found.WorkingDir), true
}

// ProjectWorkingDir returns the recorded working dir for a project.
func (c *Collector) ProjectWorkingDir(project string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cls, ok := c.classes[project]; ok {
		return cls.WorkingDir, true
	}
	for _, ct := range c.containers {
		if ct.Project == project {
			return ct.WorkingDir, true
		}
	}
	return "", false
}

// scoreReclaim applies the table from PLAN.md. Working agents veto; stack
// classification can only raise the score.
func scoreReclaim(co *Checkout) Reclaim {
	r := Reclaim{Reasons: []string{}}
	if co.Working > 0 {
		r.Reasons = append(r.Reasons, "agent working")
		return r
	}
	var blocked, done, idle, other int
	for _, a := range co.Agents {
		switch a.Status {
		case "blocked":
			blocked++
		case "done":
			done++
		case "idle":
			idle++
		default:
			other++
		}
	}
	switch {
	case len(co.Agents) == 0:
		r.Score = 70
		r.Reasons = append(r.Reasons, "no agent here")
	case other > 0:
		r.Reasons = append(r.Reasons, "agent in an unknown state")
		return r
	case blocked > 0:
		r.Score = 30
		r.Reasons = append(r.Reasons, "agent blocked (waiting on you)")
	case idle == 0:
		r.Score = 60
		r.Reasons = append(r.Reasons, "all agents done")
	default:
		r.Score = 40
		r.Reasons = append(r.Reasons, "agents idle")
	}
	for _, a := range co.Agents {
		if a.PR != "" {
			r.Reasons = append(r.Reasons, "PR "+a.PR)
			break
		}
	}
	for _, st := range co.Stacks {
		switch st.Class {
		case "orphaned":
			r.Score = 100
			r.Reasons = append(r.Reasons, "orphaned stack: checkout is gone")
		case "detached":
			if r.Score < 80 {
				r.Score = 80
			}
			r.Reasons = append(r.Reasons, "detached stack: nothing in the checkout uses it")
		}
	}
	return r
}

// candidates ranks checkouts worth stopping: score, then bytes.
func candidates(cos []Checkout) []Candidate {
	out := []Candidate{}
	for _, co := range cos {
		if co.Reclaim.Score == 0 || co.TotalBytes < 512<<20 {
			continue
		}
		out = append(out, Candidate{Path: co.Path, Display: co.Display, Branch: co.Branch, Bytes: co.TotalBytes, Score: co.Reclaim.Score, Reasons: co.Reclaim.Reasons})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Bytes > out[j].Bytes
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// FindCheckout returns the current checkout for a path.
func (c *Collector) FindCheckout(path string) (Checkout, bool) {
	for _, co := range c.Snapshot().Checkouts {
		if co.Path == path {
			return co, true
		}
	}
	return Checkout{}, false
}

// VolumeOnlyProjects returns the reaper's list of projects that exist only
// as volumes.
func (c *Collector) VolumeOnlyProjects() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.volumeOnly...)
}

// RefreshDocker forces the docker caches to refresh now (after an action).
func (c *Collector) RefreshDocker() {
	c.refreshDocker()
	c.refreshReaper()
	c.refreshDF()
}
