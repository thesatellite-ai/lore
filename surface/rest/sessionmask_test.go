package rest

import (
	"net/http"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/sessionmask"
)

// TestSessionIDsMaskedInResponses (PASS3-A-7): `lore api` masks a
// session-shaped token and the current session variable's value in every
// JSON body, as the CLI does. Not parallel: it sets the environment.
func TestSessionIDsMaskedInResponses(t *testing.T) {
	uuid := strings.Join([]string{"0f8b2c1e", "7d3a", "4b6c", "9e10", "a1b2c3d4e5f6"}, "-")
	envID := "thr_" + strings.Repeat("Q", 12)
	t.Setenv("CODEX_SESSION_ID", envID)
	h := newServer(t, newFake(t))
	r := call(h, http.MethodPost, "/v1/memories", `{"body":"s `+uuid+` and `+envID+`"}`, testToken)
	if r.status/100 != 2 {
		t.Fatalf("status %d body %q", r.status, r.body)
	}
	if strings.Contains(r.body, uuid) || strings.Contains(r.body, envID) || !strings.Contains(r.body, sessionmask.Redacted) {
		t.Errorf("session id not masked: %q", r.body)
	}
}
