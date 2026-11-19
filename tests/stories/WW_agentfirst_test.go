//go:build stories && !windows

package stories

// WW_agentfirst_test.go — story WW, devco D-694 (the MemberAgentWrites
// shape): a member's coding agent writes the first note into a team folder
// the admin just made, a folder with no project row yet. The agent's write
// makes that folder's project row; before the fix the project row was
// committed alone, stating the agent as its writer before the agent's own
// actor row existed, so every reader's authority replay held it back and
// the note failed E_NOT_FOUND on its project_id. Now the note goes
// through, nothing is held back on either machine, and the admin reads it
// after a sync. Every name, email and url is made up.

import (
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

// The fresh team folder of the cell (made up).
const wafFolder = "docs"

func TestStoryWWAgentFirstNoteInFreshTeamFolder(t *testing.T) {
	t.Parallel()
	s := &Story{World: journeys.NewWorld(t), ID: "WW"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote(wWebSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	m.Lore(pc.Dir, "member", "add", "--email", wubMember.Email, "--name", wubMember.Name, "--handle", "mo", "--role", "member", "--yes").OK(t)
	m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", "github.com/acme-co/*", "--yes").OK(t)
	m.Lore(pc.Dir, "sync").OK(t)
	mo := s.Persona(wubMember.Name, wubMember.Email).Laptop()
	journeys.GitHubLogin(t, s.World, mo, wubMember.Login)
	mc := fCodeClone(s, mo, web, "web")
	journeys.Join(t, mo, mc.Dir, wubJoin, store.SSHURL(), wubMember, []string{wFolders})
	mo.Lore(mc.Dir, "sync").OK(t)
	place := wOrg + "/" + wafFolder

	s.Step("D-694/admin-makes-a-fresh-team-folder", func() {
		// folder add writes no project row: the member's agent's first
		// note is the folder's first write
		m.Lore(pc.Dir, "store", "folder", "add", "team", wafFolder).OK(t)
		m.Lore(pc.Dir, "team", "add", "--handle", "@"+wafFolder, "--members", "mo").OK(t)
		m.Lore(pc.Dir, "sync").OK(t)
		mo.Lore(mc.Dir, "sync").OK(t)
	})

	s.Step("D-694/members-agent-writes-the-first-note", func() {
		agent := mc.With(wqAgentEnv("ww-agent-first")...)
		agent.Lore("memory", "add", "--body", "quokka docs build with the site generator", "--to", place, "--yes").OK(t).Never(t, "E_NOT_FOUND")
		agent.Lore("sync").OK(t).Never(t, "held back")
		agent.Lore("memory", "add", "--body", "quokka docs deploy on merge", "--to", place, "--yes").OK(t).Never(t, "E_NOT_FOUND", "E_STALE")
		agent.Lore("sync").OK(t).Never(t, "held back")
	})

	s.Step("D-694/admin-reads-it-after-a-sync", func() {
		m.Lore(pc.Dir, "sync").OK(t).Never(t, "held back")
		m.Lore(pc.Dir, "search", "quokka", "--store", place).OK(t).Says(t, "quokka docs build with the site generator", "quokka docs deploy on merge")
	})
}
