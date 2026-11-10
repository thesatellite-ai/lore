//go:build loretest

package identity

import (
	"context"
	"testing"
)

// TestNoDetectInTestBuild: in a loretest build LORE_NO_DETECT=1 (exactly
// "1") turns agent detection off, so a coding-agent session resolves the
// plain person with no Detection recorded; any other value keeps
// detection on (cl-gate).
func TestNoDetectInTestBuild(t *testing.T) {
	env := map[string]string{"CODEX_THREAD_ID": "t-1"}
	in := Inputs{Home: t.TempDir(), Getenv: func(k string) string { return env[k] },
		Config: Config{Identity: Entry{Key: "human:tomas@agency.example", Name: "tomas"}}, Detect: detectWith(env, true)}
	for _, c := range []struct {
		val   string
		agent bool
	}{{"1", false}, {"", true}, {"0", true}, {"true", true}, {" 1", true}} {
		t.Run("LORE_NO_DETECT="+c.val, func(t *testing.T) {
			t.Setenv(EnvNoDetect, c.val)
			r, err := Resolve(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			if got := r.Kind == KindAgent; got != c.agent {
				t.Fatalf("agent = %v (%+v), want %v", got, r, c.agent)
			}
			if !c.agent && (r.Detection != nil || r.StableKey != "human:tomas@agency.example") {
				t.Fatalf("detection off still recorded %+v", r)
			}
		})
	}
}
