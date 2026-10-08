package work

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T) Board {
	t.Helper()
	f, err := os.Open("testdata/board.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, err := ParseBoard(f)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestBoardProjection(t *testing.T) {
	b := fixture(t)
	if len(b.Streams) != 3 || len(b.Agents) != 1 {
		t.Fatalf("shape: %+v", b)
	}
	s := b.Streams[0]
	if len(s.Derived.Tasks[0].Checkouts) != 2 || s.PRs[0].Review != "REVIEW_REQUIRED" || !s.PRs[0].Draft || s.PRs[0].Checks.Running[0].URL != "" {
		t.Fatal("lost checkout or PR contract")
	}
	if b.Streams[1].Derived != nil || len(b.Streams[2].Derived.Tasks) != 0 || !strings.Contains(b.Streams[2].Warning, "Unsupported") {
		t.Fatal("legacy/unknown version")
	}
	if b.Agents[0].Assessment.WaitingOn != "you" {
		t.Fatal("personal label")
	}
	out, _ := json.Marshal(b)
	for _, forbidden := range []string{"PRIVATE", "cleanup", "session_path", "last_user_message", "recent_tool", "pr_history", "registered_brief", "input_tokens", "javascript:"} {
		if strings.Contains(string(out), forbidden) {
			t.Errorf("leaked %s", forbidden)
		}
	}
	if b.Sweep.At.IsZero() || b.Agents[0].Meta.At.IsZero() {
		t.Fatal("lost observation times")
	}
}
func TestBoardEmptyAndInvalid(t *testing.T) {
	for _, s := range []string{`{"workstreams":[],"agents":[],"sweep":{}}`, `{"workstreams":null,"agents":null,"sweep":{}}`} {
		b, err := ParseBoard(strings.NewReader(s))
		if err != nil || len(b.Streams) != 0 || len(b.Agents) != 0 {
			t.Fatalf("empty %v", err)
		}
	}
	for _, s := range []string{`{}`, `null`, `{"workstreams":[]}`, `{"workstreams":{},"agents":[],"sweep":{}}`, `{"workstreams":[],"agents":[],"sweep":null}`, `{"workstreams":[],"agents":[],"sweep":{}}{}`, `{"workstreams":[`, strings.Repeat(" ", MaxDocument+1)} {
		if _, err := ParseBoard(strings.NewReader(s)); err == nil {
			t.Errorf("accepted invalid %.80s", s)
		}
	}
}
func TestFutureDerivedShapeIsNotDecoded(t *testing.T) {
	b, err := ParseBoard(strings.NewReader(`{"workstreams":[{"id":"future","repo":"/projects/atlas","derived":{"version":99,"tasks":{"future_shape":true}}}],"agents":[],"sweep":{}}`))
	if err != nil || b.Streams[0].Derived.Version != 99 || len(b.Streams[0].Derived.Tasks) != 0 {
		t.Fatalf("future schema: %v", err)
	}
	for _, raw := range []string{`{"workstreams":[null],"agents":[],"sweep":{}}`, `{"workstreams":[{}],"agents":[],"sweep":{}}`, `{"workstreams":[],"agents":[null],"sweep":{}}`} {
		if _, err := ParseBoard(strings.NewReader(raw)); err == nil {
			t.Fatalf("invalid identity accepted: %s", raw)
		}
	}
}

func TestSafeURL(t *testing.T) {
	for _, s := range []string{"javascript:alert(1)", "data:text/html,bad", "//example.org", "file:///tmp/x", "https://user:pass@example.org/", "https://"} {
		if SafeURL(s) != "" {
			t.Fatal(s)
		}
	}
	if SafeURL("https://example.org/pr/1") == "" {
		t.Fatal("valid URL")
	}
}
