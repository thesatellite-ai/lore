package cli

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/sessionmask"
)

// TestSessionIDsMaskedInOutput (PASS3-A-7, supersedes FB-SESS-2): a
// session-shaped token in any output form (--json, human, error) is
// masked with no lore.db at all (this test has none: the known-id list is
// unreadable by construction), and so is the current session variable's
// value whatever its shape. Not parallel: it sets the environment.
func TestSessionIDsMaskedInOutput(t *testing.T) {
	uuid := strings.Join([]string{"0f8b2c1e", "7d3a", "4b6c", "9e10", "a1b2c3d4e5f6"}, "-")
	envID := "thr_" + strings.Repeat("Q", 12)
	t.Setenv("CODEX_SESSION_ID", envID)
	f := newFake(t)
	for _, args := range [][]string{
		{"memory", "add", "s " + uuid + " and " + envID, "--json"},
		{"memory", "add", "s " + uuid + " and " + envID},
	} {
		r := execute(Build(f.reg, f), "", args...)
		if r.exit != 0 {
			t.Fatalf("%v: exit %d stderr %q", args, r.exit, r.stderr)
		}
		if strings.Contains(r.stdout, uuid) || strings.Contains(r.stdout, envID) || !strings.Contains(r.stdout, sessionmask.Redacted) {
			t.Errorf("%v: session id not masked: %q", args, r.stdout)
		}
	}
	// Error output: the fake's error message carries no body, so check the
	// one place an error prints via the exported mask path directly.
	if got := masked("ERROR: [E_X] " + uuid); strings.Contains(got, uuid) {
		t.Errorf("error line not masked: %q", got)
	}
}
