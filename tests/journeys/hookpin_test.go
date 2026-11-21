//go:build journeys && !windows

package journeys

// hookpin_test.go — devco D-795 (bug hunt H-60): a person who runs a
// second lore build with its own LORE_HOME (a preview through a wrapper,
// a scratch build of a source tree) in a repo found every git hook block
// there pinned to that build: every later commit ran it against its home,
// also after the build was replaced or gone from use. Owner rule: hooks in
// a repo call `lore` from PATH; a build is pinned only when the person
// asks for it there (--pin-hooks), `lore doctor` warns while a pin is in
// place, and the lore on PATH removes it and says so.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyHookPinOnlyOnRequest(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(soloName, soloEmail)
	lap := sam.Laptop()
	devHome := filepath.Join(w.Root, "elsewhere", "preview-home")
	app := jPlainRepo(t, lap, w.Remote("github.com/sam-dev/app"), filepath.Join(lap.Home, "work", "app"))
	hooks := filepath.Join(app.Dir, ".git", "hooks")
	hookText := func() string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(hooks, "pre-commit"))
		if err != nil {
			t.Fatalf("read the pre-commit hook: %v", err)
		}
		return string(b)
	}
	const pinTag, oldPinTag = "# lore-pin:", "# lore-bin:"

	w.Step("day1/a-build-with-its-own-home-sets-up-bare-hooks", func() {
		lap.SetEnv(loreHomeEnv, devHome) // what a preview wrapper does
		lap.LoreIn(app, "init").OK(t)
		if h := hookText(); strings.Contains(h, pinTag) || strings.Contains(h, oldPinTag) || strings.Contains(h, devHome) {
			t.Fatalf("a run with LORE_HOME alone pinned the repo's hooks to its build:\n%s", h)
		}
	})

	w.Step("day1/pin-hooks-pins-on-request", func() {
		lap.LoreIn(app, "fix", "--pin-hooks").OK(t)
		if h := hookText(); !strings.Contains(h, pinTag) || !strings.Contains(h, "LORE_HOME='"+devHome+"'") {
			t.Fatalf("lore fix --pin-hooks did not pin the hooks to this build with its home:\n%s", h)
		}
	})

	w.Step("day2/doctor-on-path-warns-and-names-the-fix", func() {
		lap.SetEnv(loreHomeEnv, "") // the person's own shell: the lore on PATH
		lap.LoreIn(app, "doctor").Says(t, "are pinned to", "(LORE_HOME="+devHome+")", "`lore fix` run by the lore on PATH removes the pin")
	})

	w.Step("day2/the-lore-on-path-removes-the-pin-and-says-so", func() {
		lap.LoreIn(app, "fix").Says(t, "hooks: removed the pin to", "they run lore from PATH")
		if h := hookText(); strings.Contains(h, pinTag) || strings.Contains(h, devHome) {
			t.Fatalf("lore fix by the lore on PATH left the pin:\n%s", h)
		}
		lap.LoreIn(app, "doctor").Never(t, "are pinned to")
	})
}
