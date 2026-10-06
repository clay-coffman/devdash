package collect

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// Process is one process owned by the caller.
type Process struct {
	PID      int     `json:"pid"`
	PPID     int     `json:"ppid"`
	Name     string  `json:"name"` // argv[0] basename, falling back to comm
	Cmd      string  `json:"cmd"`  // argv joined, truncated
	Cwd      string  `json:"cwd"`
	RSS      int64   `json:"rss"`
	Threads  int     `json:"threads"`
	CPUTicks uint64  `json:"-"`   // utime+stime, for percent deltas
	CPU      float64 `json:"cpu"` // percent of one core since last sample
	Ports    []int   `json:"ports,omitempty"`
}

// parseStatus reads the fields we need from /proc/<pid>/status.
func parseStatus(r io.Reader) (name string, ppid int, uid int, rss int64, threads int) {
	uid = -1
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "Name":
			name = v
		case "PPid":
			ppid, _ = strconv.Atoi(v)
		case "Uid":
			if f := strings.Fields(v); len(f) > 0 {
				uid, _ = strconv.Atoi(f[0])
			}
		case "VmRSS":
			rss = ParseBytes(v)
		case "Threads":
			threads, _ = strconv.Atoi(v)
		}
	}
	return
}

// parseStatTicks returns utime+stime from /proc/<pid>/stat.
func parseStatTicks(b []byte) uint64 {
	// comm may contain spaces; it ends at the last ')'.
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(string(b[i+1:]))
	// After ')' the fields are: state(0) ppid(1) ... utime(11) stime(12)
	if len(f) < 13 {
		return 0
	}
	u, _ := strconv.ParseUint(f[11], 10, 64)
	s, _ := strconv.ParseUint(f[12], 10, 64)
	return u + s
}

// ListenSocket is a LISTEN entry of /proc/net/tcp{,6}: port and socket inode.
type ListenSocket struct {
	Port  int
	Inode uint64
	UID   int
}

func ParseNetTCP(r io.Reader) []ListenSocket {
	var out []ListenSocket
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || f[3] != "0A" {
			continue
		}
		_, hexPort, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		port, err := strconv.ParseUint(hexPort, 16, 32)
		if err != nil {
			continue
		}
		inode, _ := strconv.ParseUint(f[9], 10, 64)
		uid, _ := strconv.Atoi(f[7])
		out = append(out, ListenSocket{Port: int(port), Inode: inode, UID: uid})
	}
	return out
}

func readListenSockets() map[uint64]int {
	m := map[uint64]int{}
	for _, p := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		for _, s := range ParseNetTCP(f) {
			m[s.Inode] = s.Port
		}
		f.Close()
	}
	return m
}

// ReadProcesses lists processes owned by uid with cwd, RSS and listening
// ports. prevTicks (pid -> utime+stime) and dtSeconds let it compute CPU
// percent; pass nil on the first call. It returns the new tick map.
func ReadProcesses(uid int, prevTicks map[int]uint64, dtSeconds float64) ([]Process, map[int]uint64, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, nil, err
	}
	sockets := readListenSockets()
	hz := float64(clockTicks())
	ticks := make(map[int]uint64, len(prevTicks))
	var procs []Process
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		dir := filepath.Join("/proc", e.Name())
		sf, err := os.Open(filepath.Join(dir, "status"))
		if err != nil {
			continue
		}
		name, ppid, puid, rss, threads := parseStatus(sf)
		sf.Close()
		if puid != uid {
			continue
		}
		cmdline, _ := os.ReadFile(filepath.Join(dir, "cmdline"))
		if len(cmdline) == 0 {
			continue // kernel thread or zombie
		}
		args := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
		if b := filepath.Base(args[0]); b != "" && b != "." {
			name = b
		}
		cmd := strings.Join(args, " ")
		if len(cmd) > 200 {
			cmd = cmd[:200]
		}
		cwd, _ := os.Readlink(filepath.Join(dir, "cwd"))
		p := Process{PID: pid, PPID: ppid, Name: name, Cmd: cmd, Cwd: cwd, RSS: rss, Threads: threads}
		if st, err := os.ReadFile(filepath.Join(dir, "stat")); err == nil {
			p.CPUTicks = parseStatTicks(st)
			ticks[pid] = p.CPUTicks
			if prev, ok := prevTicks[pid]; ok && dtSeconds > 0 && hz > 0 {
				p.CPU = float64(p.CPUTicks-prev) / hz / dtSeconds * 100
			}
		}
		if len(sockets) > 0 {
			if fds, err := os.ReadDir(filepath.Join(dir, "fd")); err == nil {
				for _, fd := range fds {
					link, err := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
					if err != nil || !strings.HasPrefix(link, "socket:[") {
						continue
					}
					ino, _ := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]"), 10, 64)
					if port, ok := sockets[ino]; ok {
						p.Ports = append(p.Ports, port)
					}
				}
			}
		}
		if len(p.Ports) > 1 {
			sort.Ints(p.Ports)
			p.Ports = dedupeInts(p.Ports)
		}
		procs = append(procs, p)
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].RSS > procs[j].RSS })
	return procs, ticks, nil
}

func dedupeInts(a []int) []int {
	out := a[:1]
	for _, v := range a[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

func clockTicks() int64 {
	// sysconf(_SC_CLK_TCK) is 100 on every Linux we run; avoid cgo.
	return 100
}

// Kill sends sig to pid, refusing anything not owned by uid.
func Kill(pid, uid int, sig syscall.Signal) error {
	sf, err := os.Open(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return err
	}
	_, _, puid, _, _ := parseStatus(sf)
	sf.Close()
	if puid != uid {
		return os.ErrPermission
	}
	return syscall.Kill(pid, sig)
}
