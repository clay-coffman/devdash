package collect

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// FormatSample renders a Sample in devbox-mem-sample's line format, so
// either tool's log feeds the same chart.
func FormatSample(s Sample, hapiCount, testProcs int) string {
	return fmt.Sprintf("%s mem_used=%dM avail=%dM swap=%d%% herdr_cg=%dM hapi=%d/%dM pi=%d test_procs=%d load1=%.2f",
		s.T.UTC().Format(time.RFC3339), s.Used>>20, s.Avail>>20, s.SwapPC, s.Herdr>>20, hapiCount, s.Hapi>>20, s.Pi, testProcs, s.Load1)
}

// CheckoutSample is one checkout's memory at one minute.
type CheckoutSample struct {
	T     time.Time `json:"t"`
	Bytes int64     `json:"bytes"`
}

// FormatCheckoutLine renders "time\tpath\tbytes".
func FormatCheckoutLine(t time.Time, path string, bytes int64) string {
	return t.UTC().Format(time.RFC3339) + "\t" + path + "\t" + strconv.FormatInt(bytes, 10)
}

// ParseCheckoutLog reads checkouts.log into per-path series since a time.
func ParseCheckoutLog(r io.Reader, since time.Time) map[string][]CheckoutSample {
	out := map[string][]CheckoutSample{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) != 3 {
			continue
		}
		t, err := time.Parse(time.RFC3339, f[0])
		if err != nil || t.Before(since) {
			continue
		}
		b, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil {
			continue
		}
		out[f[1]] = append(out[f[1]], CheckoutSample{T: t, Bytes: b})
	}
	return out
}

func ReadCheckoutLog(path string, since time.Time) (map[string][]CheckoutSample, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string][]CheckoutSample{}, nil
		}
		return nil, err
	}
	defer f.Close()
	return ParseCheckoutLog(f, since), nil
}

// MergeSamples unions sample series, deduplicated to the minute. Earlier
// arguments win on collisions.
func MergeSamples(series ...[]Sample) []Sample {
	seen := map[int64]bool{}
	var out []Sample
	for _, s := range series {
		for _, x := range s {
			k := x.T.Unix() / 60
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T.Before(out[j].T) })
	return out
}

// AppendLine appends one line to a log file, creating directories.
func AppendLine(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}

// TrimLog rewrites a log keeping only lines whose leading RFC3339 timestamp
// is newer than maxAge. Lines without a parseable timestamp are kept.
func TrimLog(path string, maxAge time.Duration) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cutoff := time.Now().Add(-maxAge)
	var keep []string
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if line == "" {
			continue
		}
		stamp := line
		if i := strings.IndexAny(line, " \t"); i > 0 {
			stamp = line[:i]
		}
		if t, err := time.Parse(time.RFC3339, stamp); err == nil && t.Before(cutoff) {
			continue
		}
		keep = append(keep, line)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(keep, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
