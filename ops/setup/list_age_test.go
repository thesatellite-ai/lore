package setup

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// agedEnv is healthEnv whose git config file changed when changed is set.
type agedEnv struct {
	healthEnv
	file    string
	changed bool
}

func (a agedEnv) HealthInputChanged(_ context.Context, _ string) string {
	if a.changed {
		return a.file
	}
	return ""
}

// TestListHealthAgeD605: setup list showed a stored result from an
// earlier day as if current, and kept it after the git config its check
// read was fixed (devco D-605). A result from an earlier day carries its
// date; one whose git config changed after it reads "not checked since
// <date>" with the check that refreshes it, and no stale problem or fix.
func TestListHealthAgeD605(t *testing.T) {
	old := time.Now().Add(-72 * time.Hour)
	day := old.Local().Format(time.DateOnly)
	warn := []onboard.CheckResult{{Check: "identity.git_email", Status: onboard.StatusWarn, Level: onboard.LevelWarn, CheckedAt: old,
		Detail: "git config --global user.email ada@acme.example"}}
	for _, c := range []struct {
		name    string
		at      time.Time
		changed bool
		cell    string
		says    string
		never   string
	}{
		{"today, nothing changed", time.Now(), false, "⚠ 1", "⚠ 1\n", "checked"},
		{"an earlier day, nothing changed", old, false, "⚠ 1", "⚠ 1 (checked " + day + ")", "not checked since"},
		{"git config changed after it", old, true, "", "not checked since " + day + " (~/.gitconfig-acme changed since; check it again with lore setup check --setup acme --step identity)", "--global"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rs := append([]onboard.CheckResult{}, warn...)
			rs[0].CheckedAt = c.at
			f := newFakeEnv(onboard.SetupEntry{Name: "acme", Kind: onboard.KindSolo})
			env := agedEnv{healthEnv: healthEnv{fakeEnv: f, cached: map[string][]onboard.CheckResult{"acme": rs}}, file: "~/.gitconfig-acme", changed: c.changed}
			out, err := newEngine(env, onboardCatalog{}).List(bg, ListIn{})
			if err != nil {
				t.Fatal(err)
			}
			s := out.Setups[0]
			if s.Health != c.cell || s.CheckedAt == "" || (c.cell == "") != (s.Stale != "") || (c.cell == "" && len(s.Problems) != 0) {
				t.Fatalf("summary = health %q checked %q stale %q problems %d", s.Health, s.CheckedAt, s.Stale, len(s.Problems))
			}
			text := out.Text()
			if !strings.Contains(text, c.says) || strings.Contains(text, c.never) {
				t.Fatalf("text lacks %q or has %q:\n%s", c.says, c.never, text)
			}
		})
	}
}
