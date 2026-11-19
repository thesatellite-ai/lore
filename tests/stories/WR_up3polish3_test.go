//go:build stories && !windows

package stories

// WR_up3polish3_test.go — story WR (tests/stories/WR-up3polish3.md): the
// leftovers of story WQ (devco D-667, D-668).
//
//	D-667: a repo linked to the org folder itself (`org link --project
//	       org`), then a coding agent's `memory add --to acme/org` in
//	       another repo saves, never E_INTERNAL from the cache refresh
//	D-668: every success line that names a roster says "in the roster of
//	       acme (acme/org)", never "in acme org/"; an unregistered agent
//	       actor is "not on the roster of acme (acme/org)"

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

func TestStoryWRUp3Polish3(t *testing.T) {
	s := &Story{World: journeys.NewWorld(t), ID: "WR"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	inOrg := wOrg + "/org"

	s.Step("D-667/agent-write-after-org-link-project-org", func() {
		// a resource kind in the org folder: its res_server table is the
		// one a stale handle named after another handle rebuilt it
		setup := []string{"--setup", wSetup, "--yes"}
		lore := func(args ...string) { m.Lore(m.Home, append(args, setup...)...).OK(t) }
		lore("resource", "kind", "add", "--kind", "server", "--to", inOrg)
		lore("resource", "field", "add", "--kind", "server", "--field", "url", "--type", "text", "--to", inOrg)
		lore("resource", "add", "--kind", "server", "--name", "web1", "--set", "url=https://web1.example.test", "--to", inOrg)
		lc := m.Clone(app, "linked")
		m.MoveClone(t, lc, filepath.Join(m.Home, "code", "linked"))
		m.LoreIn(lc, "org", "link", "--org", wOrg, "--project", "org", "--yes").OK(t)
		ag := pc.With(wqAgentEnv("wr-session-1")...)
		ag.Lore("memory", "add", "--body", "the agent notes the release checklist", "--to", inOrg).OK(t).Never(t, "E_INTERNAL")
		lc.With(wqAgentEnv("wr-session-2")...).Lore("memory", "add", "--body", "the agent keeps the changelog short", "--to", inOrg).OK(t).Never(t, "E_INTERNAL")
		m.LoreIn(pc, "memory", "add", "--body", "a person writes too", "--to", inOrg).OK(t)
		m.LoreIn(lc, "memory", "list", "--store", wOrg).OK(t).Says(t, "the agent notes the release checklist", "a person writes too")
	})

	s.Step("D-668/success-lines-name-the-roster", func() {
		in := "in " + wqRosterLabel
		never := []string{"in " + wOrg + " org/", "org/'s roster", wOrg + "'s roster"}
		m.Lore(m.Home, "actor", "register", "--setup", wSetup).OK(t).Says(t, in).Never(t, never...)
		me := wAdminHandle(t, m, pc)
		m.LoreIn(pc, "actor", "contact", "add", "--actor", me, "--type", "github", "--value", "ada-second", "--yes").OK(t).
			Says(t, in).Never(t, never...)
		m.LoreIn(pc, "actor", "edit", "--actor", me, "--timezone", "Europe/Berlin", "--yes").OK(t).
			Says(t, in).Never(t, never...)
		m.LoreIn(pc, "role", "add", "--name", "design_lead", "--yes").OK(t).Says(t, in).Never(t, never...)
		m.LoreIn(pc, "contact-type", "add", "--name", "codeberg", "--kind", "handle", "--yes").OK(t).
			Says(t, in).Never(t, never...)
		// an agent actor no roster row names: the refusal names the roster
		task := regexp.MustCompile(`tsk_[0-9a-f]+`).FindString(m.LoreIn(pc, "task", "add", "--title", "write the release notes", "--json").OK(t).Out())
		r := pc.With("LORE_ACTOR=agent:wr-nobody").Lore("task", "claim", "--id", task, "--push")
		if r.Code == 0 || !strings.Contains(r.Out(), "not on "+wqRosterLabel) {
			t.Errorf("[WR D-668] an unregistered agent actor's claim must be refused naming %s:\n%s", wqRosterLabel, r)
		}
		for _, n := range never {
			if strings.Contains(r.Out(), n) {
				t.Errorf("[WR D-668] the refusal says %q:\n%s", n, r)
			}
		}
	})
}
