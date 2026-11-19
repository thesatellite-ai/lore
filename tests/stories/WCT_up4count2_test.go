//go:build stories && !windows

package stories

// WCT_up4count2_test.go — story WCT, the owner's exact D-684 shape: a
// client repo outside every setup's folders (setup none, found through its
// client link alone), an admin with unpushed store commits of mixed kinds
// (member add x2, org link, a set-url round trip, org config set, writes,
// policy set) while a teammate pushed 2 commits; `lore sync` in the client
// repo must count what git moved and print the pushed-store line.

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

func TestStoryWCTUp4CountOwnerShape(t *testing.T) {
	s := &Story{World: journeys.NewWorld(t), ID: "WCT"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	shop := s.Seed(s.Remote(wctClientSlug), func(dir string) { PlainRepo(t, dir) })
	portal := s.Seed(s.Remote("github.com/solo-dev/scratch"), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	m.LoreIn(pc, "org", "config", "set", "--org", wOrg, "--key", "repos", "--add", "github.com/client-co/*", "--yes").OK(t)
	m.Lore(m.Home, "sync", "--setup", wSetup).OK(t)

	// the client repo outside every setup's folders: its link is all that ties it to the org
	oc := m.Clone(shop, "shop-out")
	m.MoveClone(t, oc, filepath.Join(m.Home, "elsewhere", "shop"))
	m.LoreIn(oc, "org", "link", "--org", wOrg, "--project", "shop", "--yes").OK(t)
	// a repo outside the setup's folders that no store is attached to: its
	// sync sends the org store clone's waiting commits through the
	// waiting-clone push (sync_orgstores.go), the path that printed the
	// owner's "store <org>: pushed (sent 4, received 0, …)" with no push line
	uc := m.Clone(portal, "scratch")
	m.MoveClone(t, uc, filepath.Join(m.Home, "elsewhere", "scratch"))
	m.Lore(m.Home, "sync", "--setup", wSetup).OK(t)

	// the teammate
	_, line := journeys.Invite(t, m, pc.Dir, wqMember.Email)
	bm := s.Persona(wqMember.Name, wqMember.Email).Laptop()
	journeys.GitHubLogin(t, s.World, bm, wqMember.Login)
	bc := bm.Clone(app, "platform")
	bm.MoveClone(t, bc, filepath.Join(bm.Home, "code", "platform"))
	journeys.Join(t, bm, bc.Dir, wSetup, line, wqMember, []string{wFolders})
	m.Lore(m.Home, "sync", "--setup", wSetup).OK(t)

	clone := storeCloneDir(t, m)
	for i, p := range []struct{ name, dir string }{
		{"linked", oc.Dir},
		{"no-store-attached", uc.Dir},
	} {
		n := strconv.Itoa(i)
		s.Step("D-684/owner-shape/no-setup-client-repo-after-teammate-push/"+p.name, func() {
			// the admin's unpushed commits of mixed kinds
			m.Lore(m.Home, "member", "add", "--email", "pat.one"+n+"@example.test", "--name", "Pat One "+n, "--setup", wSetup, "--yes").OK(t)
			m.Lore(m.Home, "member", "add", "--email", "pat.two"+n+"@example.test", "--name", "Pat Two "+n, "--setup", wSetup, "--yes").OK(t)
			if i == 0 {
				m.LoreIn(pc, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", "github.com/acme-co/*", "--yes").OK(t)
				m.LoreIn(pc, "org", "link", "--org", wOrg, "--project", "platform", "--own", "--yes").OK(t)
			} else {
				m.LoreIn(pc, "org", "config", "set", "--org", wOrg, "--key", "repos", "--add", "github.com/client-two/*", "--yes").OK(t)
			}
			moved := s.MoveRemote(t, store, "acme-moved"+n, true)
			m.Lore(m.Home, "store", "set-url", "--name", wOrg, "--url", moved.SSHURL(), "--yes").OK(t)
			m.Lore(m.Home, "store", "set-url", "--name", wOrg, "--url", store.SSHURL(), "--yes").OK(t)
			m.Lore(oc.Dir, "memory", "add", "--body", "the shop deploys from a tag "+n).OK(t)
			m.Lore(oc.Dir, "memory", "add", "--body", "orders are idempotent "+n).OK(t)
			m.Lore(oc.Dir, "policy", "set", "--key", "context.budget.internal", "--value", "2200"+n, "--to", wOrg+"/shop", "--yes").OK(t)

			// the teammate pushes 2 commits meanwhile
			for _, body := range []string{"the teammate notes the release train", "the teammate keeps a runbook"} {
				bm.LoreIn(bc, "memory", "add", "--body", body+" "+n, "--to", wOrg+"/org").OK(t)
			}
			bm.LoreIn(bc, "sync").OK(t)

			head := wqGit(t, clone, "rev-parse", "HEAD")
			remoteBefore := wqGit(t, store.Dir, "rev-parse", "main")
			r := m.Lore(p.dir, "sync").OK(t)
			remoteAfter := wqGit(t, store.Dir, "rev-parse", "main")
			sent, received := wqCounts(t, r)
			went := wctCount(t, store.Dir, remoteBefore+".."+remoteAfter)
			arrived := wctCount(t, clone, head+".."+remoteBefore)
			if sent != went {
				t.Errorf("[WCT D-684 owner-shape %s] sync says sent %d, git rev-list on the store remote counts %d:\n%s", p.name, sent, went, r)
			}
			if received != arrived {
				t.Errorf("[WCT D-684 owner-shape %s] sync says received %d, git counts %d teammate commits that arrived:\n%s", p.name, received, arrived, r)
			}
			r.Says(t, "lore: pushed store")
		})
	}
}
