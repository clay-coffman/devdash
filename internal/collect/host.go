package collect

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Memory is a snapshot of /proc/meminfo in bytes.
type Memory struct {
	Total     int64 `json:"total"`
	Free      int64 `json:"free"`
	Available int64 `json:"available"`
	Buffers   int64 `json:"buffers"`
	Cached    int64 `json:"cached"`
	Shmem     int64 `json:"shmem"`
	SwapTotal int64 `json:"swap_total"`
	SwapFree  int64 `json:"swap_free"`
}

// Used mirrors `free`: total minus free minus buffers minus cache.
func (m Memory) Used() int64 {
	u := m.Total - m.Free - m.Buffers - m.Cached
	if u < 0 {
		return 0
	}
	return u
}

func ParseMeminfo(r io.Reader) (Memory, error) {
	var m Memory
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		key, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		v := ParseBytes(strings.TrimSpace(rest))
		switch key {
		case "MemTotal":
			m.Total = v
		case "MemFree":
			m.Free = v
		case "MemAvailable":
			m.Available = v
		case "Buffers":
			m.Buffers = v
		case "Cached":
			m.Cached = v
		case "Shmem":
			m.Shmem = v
		case "SwapTotal":
			m.SwapTotal = v
		case "SwapFree":
			m.SwapFree = v
		}
	}
	if m.Total == 0 {
		return m, fmt.Errorf("meminfo: no MemTotal")
	}
	return m, sc.Err()
}

func ReadMeminfo() (Memory, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return Memory{}, err
	}
	defer f.Close()
	return ParseMeminfo(f)
}

// CPUTicks is the aggregate "cpu" line of /proc/stat.
type CPUTicks struct {
	Idle, Total uint64
}

func ParseStatCPU(r io.Reader) (CPUTicks, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 || f[0] != "cpu" {
			continue
		}
		var t CPUTicks
		for i, s := range f[1:] {
			v, err := strconv.ParseUint(s, 10, 64)
			if err != nil {
				return t, err
			}
			t.Total += v
			if i == 3 || i == 4 { // idle, iowait
				t.Idle += v
			}
		}
		return t, nil
	}
	return CPUTicks{}, fmt.Errorf("stat: no cpu line")
}

func ReadStatCPU() (CPUTicks, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return CPUTicks{}, err
	}
	defer f.Close()
	return ParseStatCPU(f)
}

// CPUPercent is busy time between two tick samples, 0..100.
func CPUPercent(prev, cur CPUTicks) float64 {
	dt := float64(cur.Total - prev.Total)
	if dt <= 0 {
		return 0
	}
	di := float64(cur.Idle - prev.Idle)
	return 100 * (1 - di/dt)
}

type Load struct {
	One, Five, Fifteen float64
	Running, Threads   int
}

func ParseLoadavg(s string) (Load, error) {
	f := strings.Fields(s)
	if len(f) < 4 {
		return Load{}, fmt.Errorf("loadavg: short line")
	}
	var l Load
	var err error
	if l.One, err = strconv.ParseFloat(f[0], 64); err != nil {
		return l, err
	}
	if l.Five, err = strconv.ParseFloat(f[1], 64); err != nil {
		return l, err
	}
	if l.Fifteen, err = strconv.ParseFloat(f[2], 64); err != nil {
		return l, err
	}
	if a, b, ok := strings.Cut(f[3], "/"); ok {
		l.Running, _ = strconv.Atoi(a)
		l.Threads, _ = strconv.Atoi(b)
	}
	return l, nil
}

func ReadLoadavg() (Load, error) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return Load{}, err
	}
	return ParseLoadavg(string(b))
}

// Pressure is one PSI file (/proc/pressure/{memory,cpu,io}). "some" means at
// least one task stalled on the resource; "full" means every non-idle task did.
// Memory "some" above a few percent is the earliest warning that the box is
// reclaiming hard and an OOM kill is becoming likely.
type Pressure struct {
	Some10, Some60, Some300 float64
	Full10, Full60, Full300 float64
}

func ParsePressure(r io.Reader) (Pressure, error) {
	var p Pressure
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 {
			continue
		}
		get := func(i int) float64 {
			_, v, _ := strings.Cut(f[i], "=")
			x, _ := strconv.ParseFloat(v, 64)
			return x
		}
		switch f[0] {
		case "some":
			p.Some10, p.Some60, p.Some300 = get(1), get(2), get(3)
		case "full":
			p.Full10, p.Full60, p.Full300 = get(1), get(2), get(3)
		}
	}
	return p, sc.Err()
}

func ReadPressure(resource string) (Pressure, error) {
	f, err := os.Open("/proc/pressure/" + resource)
	if err != nil {
		return Pressure{}, err
	}
	defer f.Close()
	return ParsePressure(f)
}

type Disk struct {
	Mount string `json:"mount"`
	Total int64  `json:"total"`
	Used  int64  `json:"used"`
	Avail int64  `json:"avail"`
}

func ReadDisk(mount string) (Disk, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(mount, &st); err != nil {
		return Disk{}, err
	}
	bs := int64(st.Bsize)
	d := Disk{Mount: mount, Total: int64(st.Blocks) * bs, Avail: int64(st.Bavail) * bs}
	d.Used = d.Total - int64(st.Bfree)*bs
	return d, nil
}

// CgroupMemory returns memory.current of the first cgroup directory named
// `name` under the caller's user slice (e.g. "herdr.service"), or 0 when
// there is none. This is how the whole Herdr tree (agents and everything they
// spawned) is measured as one number.
func CgroupMemory(uid int, name string) int64 {
	base := fmt.Sprintf("/sys/fs/cgroup/user.slice/user-%d.slice/user@%d.service", uid, uid)
	var found string
	_ = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		if d.IsDir() && d.Name() == name {
			found = p
			return filepath.SkipAll
		}
		// Only descend two levels: user@.service/<slice>/<unit>.
		if d.IsDir() && strings.Count(strings.TrimPrefix(p, base), "/") >= 2 {
			return filepath.SkipDir
		}
		return nil
	})
	if found == "" {
		return 0
	}
	b, err := os.ReadFile(filepath.Join(found, "memory.current"))
	if err != nil {
		return 0
	}
	v, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return v
}
