package setup

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// TestListNamesHealthWarnings (devco D-483): a setup whose Health cell
// reads "⚠ 1" has that warning named under the table, with its fix and
// the command that lists it again; a healthy setup adds no line, and the
// JSON row carries the same problems.
func TestListNamesHealthWarnings(t *testing.T) {
	f := newFakeEnv(onboard.SetupEntry{Name: "acme-ops", Kind: onboard.KindSolo}, onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo})
	warn := onboard.CheckResult{Check: "store.unpushed", Status: onboard.StatusFail, Level: onboard.LevelWarn, Target: "acme/ops",
		Detail: "branch backup-test is not on the store", Fix: onboard.Fix{Text: "push or delete the branch"}}
	env := healthEnv{fakeEnv: f, cached: map[string][]onboard.CheckResult{
		"acme-ops": {{Status: onboard.StatusOK}, warn},
		"personal": {{Status: onboard.StatusOK}},
	}}
	out, err := newEngine(env, onboardCatalog{}).List(bg, ListIn{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Setups[0].Health != "⚠ 1" || len(out.Setups[0].Problems) != 1 || len(out.Setups[1].Problems) != 0 {
		t.Fatalf("rows = %+v", out.Setups)
	}
	txt := out.Text()
	for _, want := range []string{
		"acme-ops ⚠ 1:\n",
		"  warn store.unpushed acme/ops: branch backup-test is not on the store (fix: push or delete the branch)\n",
		"  (lore setup show --name acme-ops lists them with their fixes; lore doctor --all checks again)\n",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("setup list text lacks %q:\n%s", want, txt)
		}
	}
	if strings.Contains(txt, "personal ✓") {
		t.Errorf("a healthy setup got a problem block:\n%s", txt)
	}
}
