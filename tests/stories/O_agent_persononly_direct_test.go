//go:build stories && !windows

package stories

// O_agent_persononly_direct_test.go — devco:D-877 (bug hunt H-136) on the
// owner-shaped machine (story O): agents.person_only lists commands for
// people only, yet a detected agent ran every command App.run runs on its
// direct path (the operation invoked with no engine around it): hold claim
// and task next on an org store from a folder with no code repo, actor
// register (also from a repo), lore sync from the setup. The policy was
// never read there. The agent is now refused with the key and the
// needs_person hint on each; the person still runs them from the same
// folder.

import (
	"os"
	"testing"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
)

func TestStoryOAgentPersonOnlyDirect(t *testing.T) {
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
	codexRepo := pc.With(append(codexEnv, "CODEX_THREAD_ID=o-d877")...)
	// a plain folder (no git) under the setup's folders
	plainDir := ada.OrgDir("scratch-d877")
	if err := os.MkdirAll(plainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	inPlain := *codexRepo
	inPlain.Dir = plainDir
	codex := &inPlain

	const listed = "hold.claim,hold.release,task.next,actor.register,sync.run"
	s.Step("D-877/admin-lists-direct-commands-for-people", func() {
		ada.LoreIn(pc, "policy", "set", "--key", "agents.person_only", "--value", listed, "--to", oOrg+"/org", "--yes").OK(t)
	})

	refused := func(step, cmd string, r interface{ String() string }, code int) {
		oCheck(t, step, code == 23, "agent %s from a folder with no code repo: want exit 23 (E_POLICY_DENIED): %s", cmd, r)
	}
	s.Step("D-877/agent-refused-on-every-direct-path", func() {
		codex.Lore("whoami").Says(t, "codex")
		r := codex.Lore("hold", "claim", "--name", "staging", "--store", oOrg, "--push").
			Refused(t, "E_POLICY_DENIED", "agents.person_only lists hold.claim", "codex", "a person runs it in their own terminal")
		refused("D-877/hold-claim", "hold claim", r, r.Code)
		r = codex.Lore("task", "next", "--store", oOrg).
			Refused(t, "E_POLICY_DENIED", "agents.person_only lists task.next", "codex")
		refused("D-877/task-next", "task next", r, r.Code)
		r = codex.Lore("actor", "register", "--kind", "cron", "--label", "nightly").
			Refused(t, "E_POLICY_DENIED", "agents.person_only lists actor.register", "codex")
		refused("D-877/actor-register", "actor register", r, r.Code)
		r = codex.Lore("sync").
			Refused(t, "E_POLICY_DENIED", "agents.person_only lists sync.run", "codex")
		refused("D-877/sync", "sync", r, r.Code)
		// the roster verb in a repo (its direct path there too)
		codexRepo.Lore("actor", "register", "--kind", "cron", "--label", "nightly").
			Refused(t, "E_POLICY_DENIED", "agents.person_only lists actor.register", "codex")
		// a command the list does not name still runs for the agent there
		codex.Lore("sync", "status").OK(t)
	})

	s.Step("D-877/the-person-runs-them-there", func() {
		ada.Lore(plainDir, "hold", "claim", "--name", "staging", "--store", oOrg, "--push").OK(t)
		ada.Lore(plainDir, "hold", "release", "--name", "staging", "--store", oOrg).OK(t)
		ada.Lore(plainDir, "task", "next", "--store", oOrg).OK(t)
		ada.Lore(plainDir, "sync").OK(t)
	})
}
