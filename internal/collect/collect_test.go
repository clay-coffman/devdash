package collect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func section(t *testing.T, name string, n int) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(b), "\n---\n")
	if n >= len(parts) {
		t.Fatalf("%s has %d sections, want %d", name, len(parts), n+1)
	}
	return parts[n]
}

func TestParseBytes(t *testing.T) {
	cases := map[string]int64{
		"1.274GiB":     1367947083,
		"40.78MiB":     42760929,
		"3.779GB":      3779000000,
		"98.97MB (2%)": 98970000,
		"0B":           0,
		"451692 kB":    451692 * 1000, // /proc says kB but means KiB; close enough for display
		"32259M":       32259 * 1024 * 1024,
		" 61.44GiB":    65970697666,
		"junk":         0,
	}
	for in, want := range cases {
		if got := ParseBytes(in); got != want {
			t.Errorf("ParseBytes(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestMeminfo(t *testing.T) {
	m, err := ParseMeminfo(fixture(t, "meminfo"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Total < 60e9 || m.Total > 70e9 {
		t.Errorf("Total = %d, want ~61 GiB", m.Total)
	}
	if m.Available == 0 || m.Used() == 0 {
		t.Errorf("Available=%d Used=%d", m.Available, m.Used())
	}
	if m.SwapTotal != 0 {
		t.Errorf("devbox has no swap, got %d", m.SwapTotal)
	}
}

func TestStatAndLoad(t *testing.T) {
	c, err := ParseStatCPU(fixture(t, "stat"))
	if err != nil || c.Total == 0 || c.Idle == 0 {
		t.Fatalf("stat: %v %+v", err, c)
	}
	if p := CPUPercent(CPUTicks{Idle: 50, Total: 100}, CPUTicks{Idle: 75, Total: 200}); p != 75 {
		t.Errorf("CPUPercent = %v, want 75", p)
	}
	l, err := ParseLoadavg(section(t, "misc.txt", 0))
	if err != nil || l.One < 30 || l.Threads == 0 {
		t.Errorf("loadavg: %v %+v", err, l)
	}
}

func TestPressure(t *testing.T) {
	p, err := ParsePressure(strings.NewReader(section(t, "misc.txt", 2)))
	if err != nil {
		t.Fatal(err)
	}
	if p.Some10 < 60 || p.Some300 < 40 || p.Full10 != 0 {
		t.Errorf("cpu pressure = %+v", p)
	}
}

func TestNetTCP(t *testing.T) {
	socks := ParseNetTCP(strings.NewReader(section(t, "misc.txt", 3)))
	if len(socks) != 19 {
		t.Fatalf("got %d listen sockets, want 19", len(socks))
	}
	if socks[0].Port != 0x4BF2 || socks[0].Inode != 433863717 || socks[0].UID != 0 {
		t.Errorf("first socket = %+v", socks[0])
	}
	if socks[7].UID != 1000 || socks[7].Port != 0x4AE7 {
		t.Errorf("own socket = %+v", socks[7])
	}
	v6 := ParseNetTCP(strings.NewReader(section(t, "misc.txt", 4)))
	if len(v6) != 3 || v6[1].Port != 22 {
		t.Errorf("tcp6 = %+v", v6)
	}
}

func TestProcStatus(t *testing.T) {
	name, ppid, uid, rss, threads := parseStatus(strings.NewReader(section(t, "proc-pi.txt", 0)))
	if name != "pi" || ppid != 438126 || uid != 1000 || rss == 0 || threads != 11 {
		t.Errorf("status = %q %d %d %d %d", name, ppid, uid, rss, threads)
	}
	ticks := parseStatTicks([]byte(section(t, "proc-pi.txt", 2)))
	if ticks == 0 {
		t.Error("ticks = 0")
	}
}

func TestDockerPS(t *testing.T) {
	cs := ParseDockerPS(fixture(t, "docker-ps.tsv"))
	if len(cs) != 65 {
		t.Fatalf("got %d containers, want 65", len(cs))
	}
	c := cs[0]
	if c.Name != "carepilot-dev-feat-iam-staff-invitations-t02b-postgres-1" || c.State != "running" ||
		c.Health != "healthy" || c.Project != "carepilot-dev-feat-iam-staff-invitations-t02b" ||
		!strings.HasSuffix(c.WorkingDir, "/tools/dev-env") || c.Service != "postgres" ||
		len(c.Ports) != 1 || c.Ports[0] != 6142 {
		t.Errorf("first container = %+v", c)
	}
	if got := parsePublishedPorts("4510-4559/tcp, 5678/tcp, 127.0.0.1:19566->4566/tcp"); len(got) != 1 || got[0] != 19566 {
		t.Errorf("ports = %v", got)
	}
}

func TestDockerStats(t *testing.T) {
	st := ParseDockerStats(fixture(t, "docker-stats.jsonl"))
	fhir := st["carepilot-dev-feat-iam-staff-invitations-t02a-fhir-1"]
	if fhir.MemUsage != 1000*1024*1024 || fhir.MemLimit < 1.8e9 || fhir.MemLimit > 1.9e9 || fhir.PIDs != 75 {
		t.Errorf("fhir stats = %+v", fhir)
	}
}

func TestDockerDF(t *testing.T) {
	df := ParseDockerDF(strings.NewReader(section(t, "docker-df.txt", 0)))
	if len(df) != 4 || df[2].Type != "Local Volumes" || df[2].Total != 153 || df[2].Size < 5e9 || df[2].Reclaimable < 3e9 {
		t.Errorf("df = %+v", df)
	}
	vols := ParseVolumeProjects(strings.NewReader(section(t, "docker-df.txt", 1)))
	if len(vols) == 0 {
		t.Error("no volume projects")
	}
}

func TestHerdrAgents(t *testing.T) {
	ags, err := ParseHerdrAgents(fixture(t, "herdr-agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ags) < 10 {
		t.Fatalf("got %d agents", len(ags))
	}
	var working, named int
	for _, a := range ags {
		if a.Status == "working" {
			working++
		}
		if a.Name != "" {
			named++
		}
		if a.Cwd == "" {
			t.Errorf("agent without cwd: %+v", a)
		}
	}
	if named == 0 {
		t.Error("no named agents")
	}
}

func TestReaper(t *testing.T) {
	classes, vols := ParseReaper(fixture(t, "reaper.txt"))
	if len(classes) != 9 {
		t.Errorf("got %d classes", len(classes))
	}
	if c := classes["carepilot-dev-ibx-e2e"]; c.Class != "live" || !strings.HasSuffix(c.WorkingDir, "/ibx-e2e-clay") {
		t.Errorf("ibx-e2e = %+v", c)
	}
	if len(vols) != 11 || vols[0] != "carepilot-dev-fix-lum-1837-redact-exception-log" {
		t.Errorf("volume-only = %v", vols)
	}
}

func TestSamples(t *testing.T) {
	all := ParseSamples(fixture(t, "samples.log"), time.Time{})
	if len(all) < 1000 {
		t.Fatalf("got %d samples", len(all))
	}
	s := all[len(all)-1]
	if s.Used == 0 || s.Avail == 0 || s.Herdr == 0 || s.Pi == 0 || s.T.IsZero() {
		t.Errorf("last sample = %+v", s)
	}
	recent := ParseSamples(fixture(t, "samples.log"), s.T.Add(-time.Hour))
	if len(recent) < 30 || len(recent) > 70 {
		t.Errorf("last hour: %d samples", len(recent))
	}
}

func TestSampleRoundTrip(t *testing.T) {
	now := time.Date(2026, 10, 6, 14, 0, 33, 0, time.UTC)
	in := Sample{T: now, Used: 32259 << 20, Avail: 30655 << 20, SwapPC: 0, Herdr: 28555 << 20, Hapi: 6691 << 20, Pi: 17, Load1: 3.67}
	line := FormatSample(in, 5, 0)
	if line != "2026-10-06T14:00:33Z mem_used=32259M avail=30655M swap=0% herdr_cg=28555M hapi=5/6691M pi=17 test_procs=0 load1=3.67" {
		t.Fatalf("line = %q", line)
	}
	got := ParseSamples(strings.NewReader(line+"\n"), time.Time{})
	if len(got) != 1 || got[0] != in {
		t.Errorf("round trip = %+v, want %+v", got, in)
	}
	a := []Sample{{T: now, Used: 1}, {T: now.Add(time.Minute), Used: 2}}
	b := []Sample{{T: now.Add(10 * time.Second), Used: 99}, {T: now.Add(2 * time.Minute), Used: 3}}
	m := MergeSamples(a, b)
	if len(m) != 3 || m[0].Used != 1 || m[2].Used != 3 {
		t.Errorf("merge = %+v", m)
	}
	cl := FormatCheckoutLine(now, "/home/x/repo", 12345) + "\n" + FormatCheckoutLine(now.Add(time.Minute), "/home/x/repo", 23456) + "\n"
	co := ParseCheckoutLog(strings.NewReader(cl), now.Add(30*time.Second))
	if len(co["/home/x/repo"]) != 1 || co["/home/x/repo"][0].Bytes != 23456 {
		t.Errorf("checkout log = %+v", co)
	}
}

func TestTrimLog(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.log")
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	fresh := time.Now().UTC().Format(time.RFC3339)
	os.WriteFile(p, []byte(old+" a=1\n"+fresh+" a=2\nnot a timestamp\n"), 0o600)
	if err := TrimLog(p, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if got := string(b); got != fresh+" a=2\nnot a timestamp\n" {
		t.Errorf("trimmed = %q", got)
	}
}
