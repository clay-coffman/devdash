package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/clay-coffman/devdash/internal/state"
)

func TestCheckoutStopIsNoLongerSupported(t *testing.T) {
	s := New(state.New("test-token"))
	s.auditLog = filepath.Join(t.TempDir(), "actions.log")
	r := httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(`{"type":"checkout.stop","target":"/example"}`))
	r.Header.Set("X-Devdash-Token", "test-token")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", w.Code)
	}
	var result actionResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Detail != `unknown action "checkout.stop"` {
		t.Fatalf("unexpected result: %+v", result)
	}
}
