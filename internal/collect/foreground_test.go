package collect

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHerdrEffectiveForegroundCWD(t *testing.T) {
	for _, tt := range []struct {
		name       string
		foreground *string
		want       string
	}{
		{"different checkout", stringPointer("/projects/actual-checkout"), "/projects/actual-checkout"},
		{"non-Git foreground", stringPointer("/scratch/notes"), "/scratch/notes"},
		{"empty foreground fallback", stringPointer(""), "/projects/shell-checkout"},
		{"missing foreground fallback", nil, "/projects/shell-checkout"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			row := map[string]any{"pane_id": "w1:p1", "cwd": "/projects/shell-checkout", "agent_status": "working"}
			if tt.foreground != nil {
				row["foreground_cwd"] = *tt.foreground
			}
			b, err := json.Marshal(map[string]any{"result": map[string]any{"agents": []any{row}}})
			if err != nil {
				t.Fatal(err)
			}
			agents, err := ParseHerdrAgents(strings.NewReader(string(b)))
			if err != nil {
				t.Fatal(err)
			}
			if len(agents) != 1 || agents[0].Cwd != tt.want {
				t.Fatalf("effective location: %+v, want %q", agents, tt.want)
			}
		})
	}
}

func stringPointer(s string) *string { return &s }
