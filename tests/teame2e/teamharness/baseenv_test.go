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

// TestBaseEnvSetsTheOSUser (H-173, devco:D-915): every simulated machine
// runs lore with USER and USERNAME set to OSUser, whatever the parent has.
// A Go test binary drops the USER it inherited (devco:D-908) and a bare
// shell has none; a World that passed the parent's on made the journey of
// the OS-user identity fallback depend on the shell that ran it.
func TestBaseEnvSetsTheOSUser(t *testing.T) {
	for _, parent := range []string{"", "someone-else"} {
		for _, k := range osUserEnv {
			t.Setenv(k, parent)
		}
		for _, env := range [][]string{(&World{}).baseEnv(t.TempDir(), "alice"), probeEnv(t.TempDir())} {
			for _, k := range osUserEnv {
				var got []string
				for _, kv := range env {
					if name, v, _ := strings.Cut(kv, "="); name == k {
						got = append(got, v)
					}
				}
				if len(got) != 1 || got[0] != OSUser {
					t.Errorf("parent %s=%q: the World's %s values are %q, want exactly [%q]", k, parent, k, got, OSUser)
				}
			}
		}
	}
}
