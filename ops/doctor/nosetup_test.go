package doctor

import (
	"strings"
	"testing"
)

// TestAllNoSetupSaysNothingChecked: C-12, `lore doctor --all` on a
// machine with no setup checks no repo; the report says so and names the
// next steps (doctor in a repo, fix --all --dirs --dry-run, lore setup),
// instead of a bare "0 setups … healthy".
func TestAllNoSetupSaysNothingChecked(t *testing.T) {
	env := newEnv(t)
	env.store.cfg.Setups = nil
	r := runAllT(t, env, RunIn{})
	text := r.Text()
	for _, want := range []string{"no setup on this machine, so --all checked no repo", "lore fix --all --dirs <folder> --dry-run", "make a setup: lore setup", "0 setups"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text lacks %q:\n%s", want, text)
		}
	}
	found := false
	for _, n := range r.Notes {
		found = found || n == NoteNoSetups
	}
	if !found {
		t.Fatalf("--json notes lack the no-setup note: %q", r.Notes)
	}
	env.store.cfg.Setups = append(env.store.cfg.Setups, personal(env.home))
	if strings.Contains(runAllT(t, env, RunIn{}).Text(), "checked no repo") {
		t.Fatal("a machine with a setup got the no-setup note")
	}
}
