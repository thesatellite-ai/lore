//go:build stories && !windows

package stories

// O_agent_bound_session_test.go — devco:D-889 (bug hunt H-122, owner
// 2026-11-22) on the owner-shaped machine (story O): a coding tool runs
// each command in a new shell, so the export `lore agent start` printed
// never reached the agent's next command and every write in the org repo
// was "<person> via codex". Now agent start inside codex binds that codex
// session to the agent: the next command (no LORE_ACTOR) writes as the
// agent in the org store, whoami names the binding, another codex session
// and LORE_HUMAN=1 stay the person's, the same codex session in Ada's
// personal repo stays "Ada Home via codex" (devco:D-895: a binding never
// crosses to another identity), and the printed `lore agent stop` ends it
// where it is printed.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestStoryOAgentBoundToolSession(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})

	codexEnv := []string{"LORE_HUMAN=", "LORE_NO_DETECT=0", "LORE_ACTOR=", "LORE_SESSION="}
	for _, n := range agentdetect.AllowedNames(nil) {
		codexEnv = append(codexEnv, n+"=") // only the codex marker below
	}
	codex := pc.With(append(codexEnv, "CODEX_THREAD_ID=o-d889")...)
	other := pc.With(append(codexEnv, "CODEX_THREAD_ID=o-d889-other")...)
	const label = "frontend_d889"
	orgFolder := oOrg + "/org"

	type ident struct {
		Data struct {
			Identity struct {
				Name    string `json:"name"`
				Kind    string `json:"kind"`
				ActorID string `json:"actor_id"`
				Source  string `json:"source"`
			} `json:"identity"`
		} `json:"data"`
	}
	stampedBy := func(body string) string {
		t.Helper()
		out := teamharness.DecodeJSON[struct {
			Data []struct {
				Body      string `json:"body"`
				CreatedBy string `json:"created_by"`
			} `json:"data"`
		}](t, ada.LoreIn(pc, "memory", "list", "--store", orgFolder, "--json"))
		for _, r := range out.Data {
			if r.Body == body {
				return r.CreatedBy
			}
		}
		t.Fatalf("memory %q not in %s", body, orgFolder)
		return ""
	}
	person := teamharness.DecodeJSON[ident](t, ada.LoreIn(pc, "whoami", "--json")).Data.Identity.ActorID
	detected := teamharness.DecodeJSON[ident](t, codex.Lore("whoami", "--json")).Data.Identity.ActorID
	var agent string

	s.Step("D-889/agent-start-in-codex-binds-the-session", func() {
		r := codex.Lore("agent", "start", "--label", label).OK(t)
		r.Says(t, "every new shell of this codex session now writes as agent:"+label, "`lore agent stop` ends it")
		lines := strings.Split(strings.TrimSpace(r.Stdout), "\n")
		oCheck(t, "D-889/export-last", strings.HasPrefix(lines[len(lines)-1], "export LORE_ACTOR=agent:"+label+" LORE_SESSION=s-"), "the export is not the last line:\n%s", r)
	})

	s.Step("D-889/next-command-writes-as-the-agent-in-the-org-store", func() {
		got := teamharness.DecodeJSON[ident](t, codex.Lore("whoami", "--json")).Data.Identity
		oCheck(t, "D-889/whoami-agent", got.Kind == "agent" && got.Name == label && strings.Contains(got.Source, "lore agent start in this codex session"),
			"whoami in the bound codex session: %+v, want the agent %s from the binding", got, label)
		agent = got.ActorID
		codex.Lore("memory", "add", "--body", "the d889 bound note", "--to", orgFolder, "--yes").OK(t)
		by := stampedBy("the d889 bound note")
		oCheck(t, "D-889/stamped-agent", by == agent && by != person && by != detected, "the bound note is stamped %s, want the agent %s (person %s, detected %s)", by, agent, person, detected)
	})

	s.Step("D-889/other-session-and-human-stay-the-person", func() {
		other.Lore("whoami").Says(t, "via codex")
		codex.With("LORE_HUMAN=1").Lore("memory", "add", "--body", "the d889 human note", "--to", orgFolder, "--yes").OK(t)
		by := stampedBy("the d889 human note")
		oCheck(t, "D-889/human-wins", by == person, "LORE_HUMAN=1 in the bound session is stamped %s, want the person %s", by, person)
	})

	s.Step("D-895/same-codex-session-in-the-personal-repo-is-not-bound", func() {
		// devco:D-895 (bug hunt H-154): the binding is the person's. The
		// same codex session cd'ing into Ada's personal repo (her home
		// identity, no org) writes as "Ada Home via codex", never as the
		// org agent she started in the org repo.
		notes := s.Seed(s.Remote("github.com/"+oAdminPersonal.Login+"/notes"), func(dir string) { PlainRepo(t, dir) })
		nc := ada.CloneTo(notes, ada.Code("notes"))
		got := teamharness.DecodeJSON[ident](t, nc.With(append(codexEnv, "CODEX_THREAD_ID=o-d889")...).Lore("whoami", "--json")).Data.Identity
		oCheck(t, "D-895/personal-repo-not-bound", got.Kind == "agent" && got.Name == oAdminPersonal.Name+" via codex" && got.ActorID != agent,
			"whoami of the bound codex session in the personal repo: %+v, want %s via codex (not the org agent %s)", got, oAdminPersonal.Name, agent)
		// back in the org repo the session is still the agent
		codex.Lore("whoami").Says(t, "lore agent start in this codex session")
	})

	s.Step("D-889/printed-agent-stop-runs-where-printed", func() {
		codex.Lore("agent", "stop").OK(t).Says(t, "this codex session no longer writes as agent:"+label)
		codex.Lore("whoami").Says(t, "via codex", "from: agent detection")
		codex.Lore("agent", "stop").OK(t).Says(t, "nothing to stop")
	})
}
