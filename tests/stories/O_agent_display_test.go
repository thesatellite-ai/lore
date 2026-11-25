//go:build stories && !windows

package stories

// O_agent_display_test.go — devco:D-900 (issue #113) on the owner-shaped
// machine (story O): a detected agent's coding tool reached committed
// files ("<person> via codex" on the actor row, the Lore-Agent trailer of
// the store's commits) with no way to turn it off. Now the org's own
// policy `identity.agent_display: person` (set once in the org store, read
// by everyone who writes there) makes the agent's next write rename its
// row to the person's name; the actor id never changes, so the agent's
// history stays one actor. devco:D-983 (H-248): no git commit names the
// agent in any mode, on any ref of the store or the code repo: author and
// committer are the person, and there is no Lore-Agent trailer. The
// row's stable_key and label keep the tool slug: they are ids (the actor id
// is hashed from the key), not prose, and a client repo's copy never
// carries them.

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestStoryOAgentDisplayHidesTool(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})

	codexEnv := []string{"LORE_HUMAN=", "LORE_NO_DETECT=0", "LORE_ACTOR=", "LORE_SESSION=", "LORE_AGENT_DISPLAY="}
	for _, n := range agentdetect.AllowedNames(nil) {
		codexEnv = append(codexEnv, n+"=") // only the codex marker below
	}
	codex := pc.With(append(codexEnv, "CODEX_THREAD_ID=o-d900")...)
	orgFolder := oOrg + "/org"

	type ident struct {
		Data struct {
			Identity struct {
				Name    string `json:"name"`
				ActorID string `json:"actor_id"`
			} `json:"identity"`
		} `json:"data"`
	}
	type shown struct {
		Data struct {
			Name string `json:"display_name"`
		} `json:"data"`
	}
	personName := teamharness.DecodeJSON[ident](t, ada.LoreIn(pc, "whoami", "--json")).Data.Identity.Name
	agent := teamharness.DecodeJSON[ident](t, codex.Lore("whoami", "--json")).Data.Identity.ActorID
	agentName := func() string {
		t.Helper()
		r := ada.LoreIn(pc, "actor", "show", "--id", agent, "--store", orgFolder, "--json").OK(t)
		return teamharness.DecodeJSON[shown](t, r).Data.Name
	}
	// agentCommits lists every commit line on any ref of the git dir that
	// names an agent: an author or committer "<person> via <tool>" or at
	// @lore.invalid, or a Lore-Agent trailer (devco:D-983), after a sync.
	agentCommits := func(gitDir string) []string {
		t.Helper()
		ada.LoreIn(pc, "sync").OK(t) // the writes' commits reach the team store
		out, err := exec.Command("git", "--git-dir", gitDir, "log", "--all", "--format=@@%an <%ae> | %cn <%ce>%n%B").CombinedOutput()
		if err != nil {
			t.Fatalf("git log of %s: %v\n%s", gitDir, err, out)
		}
		var bad []string
		for _, l := range strings.Split(string(out), "\n") {
			who, header := strings.CutPrefix(l, "@@")
			if header && (strings.Contains(who, " via ") || strings.Contains(strings.ToLower(who), "@lore.invalid")) || strings.HasPrefix(l, "Lore-Agent:") {
				bad = append(bad, l)
			}
		}
		return bad
	}
	noAgentCommits := func(key string) {
		t.Helper()
		for _, dir := range []string{team.Dir, pc.Dir + "/.git"} {
			bad := agentCommits(dir)
			oCheck(t, key, len(bad) == 0, "commits on %s name the agent (author, committer or trailer), want the person only: %q", dir, bad)
		}
	}

	s.Step("D-900/default-names-the-tool", func() {
		codex.Lore("memory", "add", "--body", "the d900 first note", "--to", orgFolder, "--yes").OK(t)
		oCheck(t, "D-900/default-row", agentName() == personName+" via codex", "the agent row reads %q, want %q (the default is unchanged)", agentName(), personName+" via codex")
		noAgentCommits("D-983/default-no-agent-commit")
	})

	s.Step("D-900/org-policy-person-hides-the-tool", func() {
		ada.LoreIn(pc, "policy", "set", "--key", "identity.agent_display", "--value", "person", "--to", orgFolder, "--yes").OK(t)
		codex.Lore("memory", "add", "--body", "the d900 second note", "--to", orgFolder, "--yes").OK(t)
		got := teamharness.DecodeJSON[ident](t, codex.Lore("whoami", "--json")).Data.Identity
		oCheck(t, "D-900/same-actor", got.ActorID == agent, "the agent's id changed with the setting: %s, was %s", got.ActorID, agent)
		oCheck(t, "D-900/row-renamed", agentName() == personName, "the agent row reads %q under the org's policy person, want %q", agentName(), personName)
		noAgentCommits("D-983/person-no-agent-commit")
		files := team.FilesMatching("main", agent[4:])
		oCheck(t, "D-900/agent-row-pushed", len(files) > 0, "the agent row is not in the team store")
		for _, f := range files {
			body, _ := team.Read("main", f)
			var row struct {
				Name string `json:"display_name"`
			}
			if err := json.Unmarshal([]byte(body), &row); err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			// stable_key and label keep the slug: ids, not prose (the actor id
			// is hashed from the key; the issue's own scope)
			oCheck(t, "D-900/no-tool-in-store-name", row.Name == personName, "%s in the team store reads display_name %q, want %q", f, row.Name, personName)
		}
	})

	s.Step("D-900/env-cannot-loosen-the-org-policy", func() {
		codex.With("LORE_AGENT_DISPLAY=tool").Lore("memory", "add", "--body", "the d900 third note", "--to", orgFolder, "--yes").OK(t)
		oCheck(t, "D-900/policy-wins", agentName() == personName, "LORE_AGENT_DISPLAY=tool loosened the org's policy: %q", agentName())
	})
}
