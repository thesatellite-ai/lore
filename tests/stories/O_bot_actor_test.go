//go:build stories && !windows

package stories

// O_bot_actor_test.go — devco:D-868 (owner org-setup item 5, bug hunt
// H-116) on the owner-shaped machine (story O): the team wanted a nightly
// job to write as itself. `lore actor register --kind cron` was refused
// ("--kind is human (default) or agent") though the schema defines the
// system, cron and plugin kinds, and in an org LORE_ACTOR=cron:<x> was
// dropped without a word: the write was made as the org identity or as
// the detected coding agent. Now the admin registers the bot with an
// owner; it writes as itself, its display name and whoami name the owner;
// an unregistered or stopped bot is refused with the register command the
// refusal prints (which then works where printed); any other LORE_ACTOR
// value lore does not use is said on stderr.

import (
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// oExitInvalidInput is the exit status of E_INVALID_INPUT.
const oExitInvalidInput = 20

func TestStoryOBotActor(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	var bot *teamharness.Clone

	s.Step("D-868/platform-is-in-the-org", func() {
		ada.LoreIn(pc, "whoami").OK(t).Says(t, "org        "+oOrg)
	})

	s.Step("D-868/unregistered-bot-refused-with-the-register-command", func() {
		bot = pc.With("LORE_HUMAN=", "LORE_ACTOR=cron:nightly-sync")
		r := bot.Lore("memory", "add", "--body", "nightly sync ran", "--to", oOrg+"/"+oFolder, "--yes")
		oCheck(t, "D-868/unregistered-refused", r.Code == oExitInvalidInput, "an unregistered cron bot wrote (exit %d):\n%s", r.Code, r.Out())
		r.Says(t, "E_INVALID_INPUT", "cron:nightly-sync", "is not on the roster of", "lore actor register --kind cron --label nightly-sync --owner @<person>")
		bot.Lore("whoami").OK(t).Says(t, "cron:nightly-sync", "owner      none:")
	})

	s.Step("D-868/admin-registers-the-bot-with-the-printed-command", func() {
		ada.LoreIn(pc, "actor", "register", "--kind", "cron", "--label", "nightly-sync", "--yes").OK(t).
			Says(t, "cron nightly-sync registered in the roster of", "owned by", "LORE_ACTOR=cron:nightly-sync").Never(t, "--kind is human")
		bot.Lore("memory", "add", "--body", "nightly sync ran", "--to", oOrg+"/"+oFolder, "--yes").OK(t)
		bot.Lore("whoami").OK(t).Says(t, "cron:nightly-sync", "owner      ").Never(t, "owner      none:")
		ada.LoreIn(pc, "actor", "list").OK(t).Says(t, "[cron] nightly-sync (cron, owner ")
	})

	s.Step("D-868/stopped-bot-refused", func() {
		ada.LoreIn(pc, "actor", "deactivate", "--handle", "@nightly-sync", "--yes").OK(t).Says(t, "deactivated")
		r := bot.Lore("memory", "add", "--body", "one more run", "--to", oOrg+"/"+oFolder, "--yes")
		oCheck(t, "D-868/stopped-refused", r.Code == oExitInvalidInput, "a deactivated cron bot wrote (exit %d):\n%s", r.Code, r.Out())
		r.Says(t, "was deactivated on the roster of")
	})

	s.Step("D-868/ignored-actor-said-on-every-command", func() {
		pc.With("LORE_HUMAN=", "LORE_ACTOR=human:eve@example.test").Lore("memory", "list").OK(t).
			Says(t, "LORE_ACTOR=human:eve@example.test ignored: in ")
	})
}
