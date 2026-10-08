package server

import (
	"encoding/json"
	"github.com/clay-coffman/devdash/internal/state"
	"github.com/clay-coffman/devdash/internal/work"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkEndpointIsCachedAllowlist(t *testing.T) {
	// If the handler ran a command this PATH would fail. Pending is honest, not empty success.
	t.Setenv("PATH", t.TempDir())
	s := New(state.New("PRIVATE TOKEN"))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/work", nil))
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code)
	}
	var p work.Projection
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Version != 1 || p.Board.State != "pending" || p.Herdr.State != "pending" {
		t.Fatal("contract")
	}
	if strings.Contains(w.Body.String(), "PRIVATE") {
		t.Fatal("token leaked")
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/work", nil))
	if w.Code != 405 {
		t.Fatal("read only")
	}
}
