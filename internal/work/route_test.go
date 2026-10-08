package work

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyRouteChildContextAndFailures(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "herdr.sock")
	for i, body := range []string{
		`printf '{"running":true,"endpoint_compatible":true,"socket":"%s"}' "$HERDR_SOCKET_PATH"`,
		`printf '{"running":true,"endpoint_compatible":true,"socket":"/foreign.sock"}'`,
		`printf '{"running":false,"socket":"%s"}' "$HERDR_SOCKET_PATH"`,
		`printf 'not json'`,
		`exit 1`,
	} {
		script := "#!/bin/sh\n" + body + "\n"
		if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		r := VerifyRoute(socket)
		if r.Verified != (i == 0) || r.Server != Fingerprint(socket) {
			t.Fatalf("%s: %+v", body, r)
		}
	}
	if err := os.Remove(filepath.Join(dir, "herdr")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if r := VerifyRoute(socket); r.Verified || r.Reason == "" {
		t.Fatalf("missing CLI accepted: %+v", r)
	}
}
