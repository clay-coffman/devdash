package state

import (
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/clay-coffman/devdash/internal/collect"
)

var testProcRe = regexp.MustCompile(`vitest|tsc --noEmit|playwright`)

// StateDir is where devdash keeps its own logs.
func StateDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "devdash")
}

func (c *Collector) ownSamplesLog() string { return filepath.Join(StateDir(), "samples.log") }
func (c *Collector) checkoutsLog() string  { return filepath.Join(StateDir(), "checkouts.log") }

// sampleToLog writes one host line and one line per checkout. Runs every
// minute; the same format as devbox-mem-sample so either log feeds the chart.
func (c *Collector) sampleToLog() {
	s := c.Snapshot()
	if s.Mem.Total == 0 {
		return
	}
	now := time.Now()
	var hapiCount int
	var hapiBytes int64
	var pi, tests int
	for _, co := range append(s.Checkouts, s.Unattributed) {
		for _, st := range co.Stacks {
			for _, ct := range st.Containers {
				if strings.Contains(ct.Name, "-fhir-") && ct.State == "running" {
					hapiCount++
					hapiBytes += ct.MemUsage
				}
			}
		}
		for _, p := range co.Processes {
			if p.Name == "pi" {
				pi++
			}
			if testProcRe.MatchString(p.Cmd) {
				tests++
			}
		}
	}
	swapPC := 0
	if s.Mem.SwapTotal > 0 {
		swapPC = int((s.Mem.SwapTotal - s.Mem.SwapFree) * 100 / s.Mem.SwapTotal)
	}
	line := collect.FormatSample(collect.Sample{T: now, Used: s.Mem.Used, Avail: s.Mem.Available, SwapPC: swapPC,
		Herdr: s.Buckets.HerdrCgroup, Hapi: hapiBytes, Pi: pi, Load1: s.CPU.Load.One}, hapiCount, tests)
	if err := collect.AppendLine(c.ownSamplesLog(), line); err != nil {
		log.Printf("sampler: %v", err)
	}
	for _, co := range s.Checkouts {
		if co.TotalBytes == 0 {
			continue
		}
		if err := collect.AppendLine(c.checkoutsLog(), collect.FormatCheckoutLine(now, co.Path, co.TotalBytes)); err != nil {
			log.Printf("sampler: %v", err)
			break
		}
	}
}

func (c *Collector) trimLogs() {
	for _, p := range []string{c.ownSamplesLog(), c.checkoutsLog()} {
		if err := collect.TrimLog(p, 7*24*time.Hour); err != nil {
			log.Printf("trim %s: %v", p, err)
		}
	}
}

// History returns host samples since the given time, merging devdash's own
// log with devbox-mem-sample's when it exists.
func (c *Collector) History(since time.Time) ([]collect.Sample, error) {
	own, err := collect.ReadSamples(c.ownSamplesLog(), since)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	other, _ := collect.ReadSamples(c.samplesLog, since)
	return collect.MergeSamples(own, other), nil
}

// CheckoutHistory returns per-checkout series since the given time.
func (c *Collector) CheckoutHistory(since time.Time) (map[string][]collect.CheckoutSample, error) {
	return collect.ReadCheckoutLog(c.checkoutsLog(), since)
}
