//go:build journeys && !windows

package journeys

// botactor_test.go — devco:D-868 (H-116): an org that runs a nightly job
// wants it to write as itself, not as a coding agent or a person.
// `lore actor register` refused the system/cron/plugin kinds the schema
// defines, and inside an org LORE_ACTOR=system:<x> was dropped without a
// word (the write was made as the org identity, or as the detected agent).
// Now an admin registers the bot with an owner, it writes as itself with
// the owner on its actor row and in its display name, whoami names the
// owner, and an unregistered or stopped bot is refused with the register
// command; any other ignored LORE_ACTOR value is said on every command.

import (
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// exitInvalidInput is the exit status of E_INVALID_INPUT.
const exitInvalidInput = 20

func TestJourneyBotActorInAnOrg(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	mira := w.Persona("Mira Castell", "mira@agency.example").Laptop()
	store := w.Remote("github.com/agency-co/lore-store")
	app := jPlainRepo(t, mira, w.Remote("github.com/agency-co/nightlyapp"), filepath.Join(mira.Home, "work", "nightlyapp"))
	var bot *teamharness.Clone

	w.Step("day1/an-org-and-a-linked-repo", func() {
		mira.LoreIn(app, "store", "init", "--name", "agencystore", "--url", store.SSHURL()).OK(t)
		mira.LoreIn(app, "org", "create", "--org", lgOrg, "--store", store.SSHURL(), "--own", lgOwn,
			"--key", "human:mira@agency.example", "--name", "Mira", "--yes").OK(t)
		mira.LoreIn(app, "store", "folder", "add", "agencystore", "nightly").OK(t)
		mira.LoreIn(app, "org", "link", "--org", lgOrg, "--project", "nightly", "--own", "--yes").OK(t)
		mira.LoreIn(app, "actor", "register", "--yes").OK(t)
		bot = app.With("LORE_HUMAN=", "LORE_ACTOR=cron:nightly-sync")
	})

	w.Step("day1/an-unregistered-bot-is-refused-loudly", func() {
		bot.Lore("memory", "add", "--body", "nightly sync ran", "--yes").
			Fails(t, exitInvalidInput, "E_INVALID_INPUT", "cron:nightly-sync", "is not on the roster of "+lgOrg,
				"lore actor register --kind cron --label nightly-sync --owner @<person>")
		bot.Lore("whoami").OK(t).Says(t, "cron:nightly-sync", "owner      none:", "lore actor register --kind cron --label nightly-sync")
	})

	w.Step("day1/mira-registers-the-bot-it-writes-as-itself", func() {
		mira.LoreIn(app, "actor", "register", "--kind", "cron", "--label", "nightly-sync", "--yes").OK(t).
			Says(t, "cron nightly-sync registered in the roster of", "owned by Mira (@mira)", "LORE_ACTOR=cron:nightly-sync").
			Never(t, "--kind is human")
		bot.Lore("memory", "add", "--body", "nightly sync ran", "--yes").OK(t)
		bot.Lore("whoami").OK(t).Says(t, "cron:nightly-sync", "owner      Mira (@mira)")
		mira.LoreIn(app, "actor", "list").OK(t).Says(t, "[cron] nightly-sync (cron, owner Mira)")
		mira.LoreIn(app, "memory", "list", "--json").OK(t).Says(t, "nightly sync ran")
	})

	w.Step("day1/a-label-names-one-agent-or-bot", func() {
		mira.LoreIn(app, "actor", "register", "--kind", "system", "--label", "nightly-sync", "--yes").
			Fails(t, exitInvalidInput, "the label nightly-sync is taken", "cron:nightly-sync", "lore actor register --kind system --label nightly-sync-system")
		mira.LoreIn(app, "actor", "register", "--kind", "system", "--label", "nightly-sync-system", "--yes").OK(t).
			Says(t, "system nightly-sync-system registered in the roster of")
	})

	w.Step("day1/a-stopped-bot-cannot-write", func() {
		mira.LoreIn(app, "actor", "deactivate", "--handle", "@nightly-sync", "--yes").OK(t).Says(t, "deactivated")
		bot.Lore("memory", "add", "--body", "one more run", "--yes").
			Fails(t, exitInvalidInput, "was deactivated on the roster of "+lgOrg, "lore actor register --kind cron --label nightly-sync")
	})

	w.Step("day1/an-ignored-actor-is-said-on-every-command", func() {
		app.With("LORE_HUMAN=", "LORE_ACTOR=human:eve@agency.example").Lore("memory", "list").OK(t).
			Says(t, "LORE_ACTOR=human:eve@agency.example ignored: in "+lgOrg)
	})
}
