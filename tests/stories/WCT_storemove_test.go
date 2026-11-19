//go:build stories && !windows

package stories

// WCT_storemove_test.go — story WCT, the set-url move matrix (devco D-699,
// owner use pass 5 weak 5): after an admin's `lore store set-url` to a NEW
// repo while the old one still exists, every explicit push path sends the
// move to the old address (its push line names the old address), nothing
// else does, and the teammate whose lore still uses the old address follows
// at their next sync. One cell per explicit push path; each cell moves the
// store again, so the old address of a cell is the new one of the cell
// before it (a teammate who followed once follows again).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestStoryWCTStoreMoveForward(t *testing.T) {
	s := &Story{World: journeys.NewWorld(t), ID: "WCT"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	m.Lore(m.Home, "sync", "--setup", wSetup).OK(t)

	_, line := journeys.Invite(t, m, pc.Dir, wqMember.Email)
	bm := s.Persona(wqMember.Name, wqMember.Email).Laptop()
	journeys.GitHubLogin(t, s.World, bm, wqMember.Login)
	bc := bm.Clone(app, "platform")
	bm.MoveClone(t, bc, filepath.Join(bm.Home, "code", "platform"))
	journeys.Join(t, bm, bc.Dir, wSetup, line, wqMember, []string{wFolders})
	m.Lore(m.Home, "sync", "--setup", wSetup).OK(t)
	bm.LoreIn(bc, "sync").OK(t)

	cur := store
	for _, p := range []struct {
		name string
		push func() teamharness.Result
	}{
		{"repo-sync", func() teamharness.Result { return m.LoreIn(pc, "sync") }},
		{"home-sync-setup", func() teamharness.Result { return m.Lore(m.Home, "sync", "--setup", wSetup) }},
		{"store-push", func() teamharness.Result { return m.Lore(m.Home, "store", "push", "--store", wOrg) }},
	} {
		s.Step("D-699/store-moves-to-a-new-repo/"+p.name, func() {
			old := cur
			fresh := s.Remote("github.com/acme-co/lore-team-" + p.name)
			wqGit(t, old.Dir, "push", "-q", "--mirror", fresh.Dir) // the history copied over; the old repo stays
			oldBefore := wqGit(t, old.Dir, "rev-parse", "main")

			m.Lore(m.Home, "store", "set-url", "--name", wOrg, "--url", fresh.SSHURL(), "--yes").OK(t).
				Says(t, "also sends the move to the old address "+old.SSHURL())
			// never without an explicit push: set-url and a read leave the old repo as it was
			m.LoreIn(pc, "memory", "list").OK(t)
			if got := wqGit(t, old.Dir, "rev-parse", "main"); got != oldBefore {
				t.Errorf("[WCT D-699 %s] the old repo moved (%s -> %s) before any explicit push", p.name, oldBefore, got)
			}

			r := p.push().OK(t)
			r.Says(t, "at its old address "+old.SSHURL()+" too")
			oldHead, newHead := wqGit(t, old.Dir, "rev-parse", "main"), wqGit(t, fresh.Dir, "rev-parse", "main")
			if oldHead != newHead {
				t.Fatalf("[WCT D-699 %s] old repo main %s, new repo main %s after the explicit push:\n%s", p.name, oldHead, newHead, r)
			}
			// said once: the next push owes the old address nothing
			p.push().OK(t).Never(t, "old address")

			// the teammate on the old address follows at their next sync
			bm.LoreIn(bc, "sync").OK(t)
			cfg, err := os.ReadFile(bm.LorePath("config.yaml"))
			if err != nil || !strings.Contains(string(cfg), fresh.SSHURL()) {
				t.Fatalf("[WCT D-699 %s] the teammate's config does not follow the move to %s (%v):\n%s", p.name, fresh.SSHURL(), err, cfg)
			}
			bm.LoreIn(bc, "sync").OK(t)
			bclone := wctStoreClone(t, bm, "lore-team-"+p.name)
			if got := wqGit(t, bclone, "rev-parse", "HEAD"); got != newHead && got != wqGit(t, fresh.Dir, "rev-parse", "main") {
				t.Fatalf("[WCT D-699 %s] the teammate's clone %s is at %s, the new repo at %s", p.name, bclone, got, newHead)
			}
			cur = fresh
		})
	}
}

// wctStoreClone is the machine's clone of the acme-co store repo named repo.
func wctStoreClone(t *testing.T, m *journeys.Machine, repo string) string {
	t.Helper()
	got, _ := filepath.Glob(filepath.Join(m.Home, ".lore", "stores", "acme-co", repo+"*"))
	if len(got) != 1 {
		t.Fatalf("want one clone of acme-co/%s, got %v", repo, got)
	}
	return got[0]
}
