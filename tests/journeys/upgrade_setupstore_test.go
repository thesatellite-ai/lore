//go:build journeys && !windows

package journeys

// upgrade_setupstore_test.go — B4-22 and D10 (B4-23): a lore 0.1.17 user
// saves the setup page's personal store step after upgrading. With 0.1.17's
// `personal.store: local`, choosing "local" reuses that folder (its note
// moves to its root) and no command fails "store me attached twice". With
// a git personal store holding a note, choosing "local" says the old store
// holds 1 note, keeps it readable as me-before, and `lore move <id> --to me
// --yes` brings it over.

import (
	"strings"
	"testing"
)

const (
	ssLocalNote = "a note in the 0.1.17 local store"
	ssGitNote   = "a note in the 0.1.17 git personal store"
)

// ssSetup saves the setup steps before personal_store on m.
func ssSetup(t testing.TB, m *Machine, name, email string) {
	t.Helper()
	m.Lore(m.Home, "setup", "apply", "side", "--step", "kind", "--answers", `{"kind":"solo","name":"side"}`).OK(t)
	m.Lore(m.Home, "setup", "apply", "side", "--step", "identity", "--answers", `{"name":"`+name+`","email":"`+email+`"}`).OK(t)
	m.Lore(m.Home, "setup", "apply", "side", "--step", "github", "--answers", `{}`).OK(t)
}

func TestJourneyUpgraderSetupPersonalStore(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)

	lou := w.Persona("Lou Local", "lou@example.test")
	old := UseOldLore(t, lou.Laptop())
	notes := old.Clone(w.Remote("github.com/lou/notes"), "notes")
	w.Step("day1/local-store-setup-local", func() {
		upWriteConfig(t, old.Machine, "identity:\n  key: human:lou@example.test\n  name: Lou Local\npersonal:\n  store: local\n")
		old.Old(notes, "init", "--non-interactive", "--name=notes").OK(t)
		old.Old(notes, "memory", "add", "--body", ssLocalNote, "--to", "me").OK(t)
		m := old.Upgrade()
		ssSetup(t, m, "Lou Local", "lou@example.test")
		m.Lore(m.Home, "setup", "apply", "side", "--step", "personal_store", "--answers", `{"personal_store":"local"}`).OK(t).
			Says(t, "reused lore 0.1.17's personal store")
		r := m.LoreIn(notes, "memory", "list").OK(t)
		r.Never(t, "attached twice")
		upCount(t, "lou", r.Stdout, ssLocalNote)
		m.Lore(m.Home, "config", "check").OK(t).Never(t, "warning")
	})

	pia := w.Persona("Pia Git", "pia@example.test")
	gold := UseOldLore(t, pia.Laptop())
	me := w.Remote("github.com/pia/lore-me")
	work := gold.Clone(w.Remote("github.com/pia/work"), "work")
	w.Step("day1/git-store-setup-local", func() {
		upWriteConfig(t, gold.Machine, "identity:\n  key: human:pia@example.test\n  name: Pia Git\npersonal:\n  store: "+me.SSHURL()+"\n")
		gold.Old(nil, "store", "init", me.SSHURL()).OK(t)
		gold.Old(work, "init", "--non-interactive", "--name=work").OK(t)
		gold.Old(work, "memory", "add", "--body", ssGitNote, "--to", "me").OK(t)
		m := gold.Upgrade()
		m.LoreIn(work, "memory", "list").OK(t) // the first command moves the 0.1.17 clone
		ssSetup(t, m, "Pia Git", "pia@example.test")
		m.Lore(m.Home, "setup", "apply", "side", "--step", "personal_store", "--answers", `{"personal_store":"local"}`).OK(t).
			Says(t, "holds 1 note(s)", "me-before", "lore move <id> --to me --yes")
		r := m.LoreIn(work, "memory", "list").OK(t)
		upCount(t, "pia", r.Stdout, ssGitNote)
		r.Says(t, "[me-before]")
		id := upMemID.FindString(r.Stdout)
		m.LoreIn(work, "move", id, "--to", "me", "--yes").OK(t)
		after := m.LoreIn(work, "memory", "list").OK(t)
		upCount(t, "pia after move", after.Stdout, ssGitNote)
		if !strings.Contains(after.Stdout, "[me]") || strings.Contains(after.Stdout, "[me-before]") {
			t.Errorf("after the move the note is not in me:\n%s", after.Stdout)
		}
	})
}
