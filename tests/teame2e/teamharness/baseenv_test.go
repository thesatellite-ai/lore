package teamharness

import (
	"strings"
	"testing"
)

// TestBaseEnvDropsDeveloperCredentials: a World's processes never get the
// developer's ssh agent or GitHub tokens, so a git or ssh call that
// reaches a real host (a journey machine with no fake GitHub account)
// cannot act as the developer there (journeys solo, 2026-11-09).
func TestBaseEnvDropsDeveloperCredentials(t *testing.T) {
	for _, k := range developerCredentials {
		t.Setenv(k, "placeholder-"+strings.ToLower(k))
	}
	t.Setenv("TZ", "UTC") // an ordinary variable still passes through
	env := (&World{}).baseEnv(t.TempDir(), "alice")
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		for _, c := range developerCredentials {
			if k == c {
				t.Errorf("a World inherits the developer's %s", k)
			}
		}
	}
	if !strings.Contains(strings.Join(env, "\n"), "\nTZ=UTC") && env[0] != "TZ=UTC" {
		t.Errorf("an ordinary variable (TZ) was dropped too")
	}
}
