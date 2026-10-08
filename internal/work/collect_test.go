package work

import (
	"context"
	"errors"
	"github.com/clay-coffman/devdash/internal/collect"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBoardReadLimits(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	path := filepath.Join(dir, "herdr-board")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%08388609d' 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBoard(); !errors.Is(err, collect.ErrOutputLimit) {
		t.Fatalf("8 MiB command bound: %v", err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec /bin/sleep 11\n"), 0700); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := ReadBoard(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("10s command bound: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 11*time.Second {
		t.Fatalf("timeout took %v", elapsed)
	}
}

func TestBoardReadExactCommandAndMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if _, err := ReadBoard(); err == nil {
		t.Fatal("missing provider")
	}
	script := `#!/bin/sh
[ "$#" = 2 ] && [ "$1" = json ] && [ "$2" = --no-sweep ] || exit 7
printf '{"workstreams":[],"agents":[],"sweep":{}}'
`
	path := filepath.Join(dir, "herdr-board")
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	b, err := ReadBoard()
	if err != nil || len(b.Streams) != 0 {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '{truncated'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBoard(); err == nil || !strings.Contains(err.Error(), "invalid board") {
		t.Fatal("malformed", err)
	}
}
