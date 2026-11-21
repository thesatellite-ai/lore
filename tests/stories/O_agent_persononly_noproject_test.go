//go:build stories && !windows

package stories

// O_agent_persononly_noproject_test.go — devco:D-872 (bug hunt H-134) on
// the owner-shaped machine (story O): agents.person_only lists a command
// for people only, yet a detected agent ran it from a git repo with no lore
// project (a plain clone under the setup's folders): that path invoked the
// operation with a caller whose agent was never filled in, and the engine
// checked person_only only for a caller that named its agent. The agent is
// now refused there as in a repo with a project, with the key and the
// needs_person hint; the person still runs the command there.

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
)

func TestStoryOAgentPersonOnlyNoProject(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})

	codexEnv := []string{"LORE_HUMAN=", "LORE_NO_DETECT=0", "LORE_ACTOR="}
	for _, n := range agentdetect.AllowedNames(nil) {
		codexEnv = append(codexEnv, n+"=") // only the codex marker below
	}
	codex := pc.With(append(codexEnv, "CODEX_THREAD_ID=o-d872")...)
	org := oOrg + "/org"
	const body = "Deploys need two approvals"

	s.Step("D-872/admin-lists-rule-add-for-people", func() {
		ada.LoreIn(pc, "policy", "set", "--key", "agents.person_only", "--value", "rule.add", "--to", org, "--yes").OK(t)
	})

	s.Step("D-872/agent-in-a-repo-with-no-project-is-refused", func() {
		codex.Lore("whoami").Says(t, "codex")
		r := codex.Lore("rule", "add", "--body", body, "--to", org, "--yes").
			Refused(t, "E_POLICY_DENIED", "agents.person_only lists rule.add", `codex`, "a person runs it in their own terminal")
		oCheck(t, "D-872/refused", r.Code == 23, "agent rule add from a repo with no lore project: want exit 23 (E_POLICY_DENIED): %s", r)
		ada.LoreIn(pc, "rule", "list", "--store", org).Never(t, body)
		// a command the list does not name still runs for the agent there
		codex.Lore("memory", "add", "--body", "the d872 agent note", "--to", org, "--yes").OK(t)
	})

	s.Step("D-872/the-person-runs-it-there", func() {
		ada.LoreIn(pc, "rule", "add", "--body", body, "--to", org, "--yes").OK(t)
		ada.LoreIn(pc, "rule", "list", "--store", org).Says(t, body)
	})
}
