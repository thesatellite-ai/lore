//go:build stories && !windows

package stories

// O_agent_nowrite_test.go — devco:D-869 (owner org-setup item 6, bug hunt
// H-117) on the owner-shaped machine (story O): agents.person_only names
// commands only, so a folder only people should change (acme/legal) was
// open to every agent through any command the list did not name. The
// folder's own `agents.no_write` now keeps the agents it lists (a tool
// such as codex, or * for every agent) out of every write into it: an
// add, a move into it, the agent taking the policy off; the refusal names
// the key, the folder and the line a person runs, and that line works
// where printed. The person still writes there, and the agent still
// writes everywhere else.
//
// devco:D-991 (bug hunt H-259, security): a bot on the roster (lore actor
// register --kind plugin, writing as LORE_ACTOR=plugin:<label>) passed
// agents.no_write whatever it listed (*, plugin:<label>, <label>) and its
// rules kept trust 0.50. A bot is now kept out like an agent, and its rule
// waits for a person's approve, marked agent-written.

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
)

func TestStoryOAgentNoWriteFolder(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})

	const folder = "legal"
	legal := oOrg + "/" + folder
	codexEnv := []string{"LORE_HUMAN=", "LORE_NO_DETECT=0", "LORE_ACTOR="}
	for _, n := range agentdetect.AllowedNames(nil) {
		codexEnv = append(codexEnv, n+"=") // only the codex marker below
	}
	codex := pc.With(append(codexEnv, "CODEX_THREAD_ID=o-d869")...)
	unset := []string{"policy", "unset", "--key", "agents.no_write", "--to", legal, "--yes"}

	s.Step("D-869/admin-keeps-codex-out-of-legal", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", oOrg, folder).OK(t)
		ada.LoreIn(pc, "policy", "set", "--key", "agents.no_write", "--value", "codex", "--to", legal, "--yes").OK(t)
		ada.LoreIn(pc, "policy", "set", "--key", "agents.no_write", "--value", "rule.add rule", "--to", legal, "--yes").
			Refused(t, "E_INVALID_INPUT", "is not an agent name")
	})

	s.Step("D-869/every-agent-write-into-legal-is-refused", func() {
		codex.Lore("whoami").Says(t, "codex")
		for _, args := range [][]string{
			{"rule", "add", "--body", "Contracts need two signatures", "--to", legal, "--yes"},
			{"memory", "add", "--body", "the legal inbox is checked on Mondays", "--to", legal, "--yes"},
			{"task", "add", "--title", "renew the NDA template", "--to", legal, "--yes"},
			unset, // the agent cannot take itself off the list
		} {
			r := codex.Lore(args...).Refused(t, "E_POLICY_DENIED", "agents.no_write of "+legal, `"codex"`,
				"lore policy unset --key agents.no_write --to "+legal+" --yes")
			oCheck(t, "D-869/refused-"+args[0], r.Code == 23, "agent %v into %s: want exit 23 (E_POLICY_DENIED): %s", args, legal, r)
		}
		ada.LoreIn(pc, "rule", "list", "--store", legal).Never(t, "Contracts need two signatures")
	})

	s.Step("D-869/agent-move-into-legal-is-refused", func() {
		id := oAddedID(t, codex.Lore("memory", "add", "--body", "the d869 note to move", "--to", "me", "--json").OK(t))
		if id == "" {
			t.Fatal("memory add --json printed no id")
		}
		codex.Lore("move", "--id", id, "--to", legal, "--yes").Refused(t, "E_POLICY_DENIED", "agents.no_write of "+legal)
	})

	s.Step("D-869/the-person-and-other-places-pass", func() {
		ada.LoreIn(pc, "rule", "add", "--body", "Contracts need two signatures", "--to", legal, "--yes").OK(t)
		codex.Lore("memory", "add", "--body", "the d869 agent note in the org folder", "--to", oOrg+"/org", "--yes").OK(t)
	})

	s.Step("D-869/printed-unset-runs-for-the-person", func() {
		ada.LoreIn(pc, unset...).OK(t)
		codex.Lore("memory", "add", "--body", "the legal inbox is checked on Mondays", "--to", legal, "--yes").OK(t)
	})

	bot := pc.With(append(codexEnv, "LORE_ACTOR=plugin:helper")...)
	s.Step("D-991/a-bot-is-kept-out-like-an-agent", func() {
		ada.LoreIn(pc, "actor", "register", "--kind", "plugin", "--label", "helper", "--yes").OK(t).
			Says(t, "plugin helper registered in the roster of", "LORE_ACTOR=plugin:helper")
		for _, item := range []string{"*", "plugin:helper", "helper"} {
			ada.LoreIn(pc, "policy", "set", "--key", "agents.no_write", "--value", item, "--to", legal, "--yes").OK(t)
			r := bot.Lore("memory", "add", "--body", "the d991 bot note", "--to", legal, "--yes").
				Refused(t, "E_POLICY_DENIED", "agents.no_write of "+legal, `"plugin:helper"`,
					"lore policy unset --key agents.no_write --to "+legal+" --yes")
			oCheck(t, "D-991/bot-refused-"+item, r.Code == 23, "plugin bot into %s with agents.no_write=%s: want exit 23 (E_POLICY_DENIED): %s", legal, item, r)
			ada.LoreIn(pc, unset...).OK(t) // the printed line works for the person
		}
		ada.LoreIn(pc, "memory", "list", "--store", legal).Never(t, "the d991 bot note")
	})

	s.Step("D-991/a-bot-rule-waits-for-a-person", func() {
		id := oAddedID(t, bot.Lore("rule", "add", "--body", "Bots file the weekly legal digest", "--to", legal, "--yes", "--json").OK(t))
		if id == "" {
			t.Fatal("rule add --json printed no id")
		}
		ada.LoreIn(pc, "rule", "show", "--id", id).OK(t).Says(t, "agent-written, not approved")
		bot.Lore("rule", "approve", "--id", id, "--yes").Refused(t, "E_POLICY_DENIED")
		ada.LoreIn(pc, "rule", "approve", "--id", id, "--yes").OK(t)
		ada.LoreIn(pc, "rule", "show", "--id", id).OK(t).Never(t, "agent-written, not approved")
	})
}
