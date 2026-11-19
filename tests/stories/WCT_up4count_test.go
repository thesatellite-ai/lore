//go:build stories && !windows

package stories

// WCT_up4count_test.go — story WCT (tests/stories/WCT-up4count.md): what
// `lore sync` and a write say they moved is what git moved (devco D-684,
// D-691).
//
//	D-684: a teammate pushes to the store first; the admin then writes in a
//	       linked client repo, a linked own repo and a store folder, and runs
//	       `lore sync` there: the store line's "sent N" is the commits that
//	       reached the remote, "received M" the commits that arrived, and the
//	       "lore: pushed store …" line is printed in every placement
//	D-691: a write's "wrote N row file(s)" is the files its commit holds

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

// wctClientSlug is a client's repo the org works on (made-up names).
const wctClientSlug = "github.com/client-co/shop"

// wctWrote is a write's "wrote N row file(s)" line.
var wctWrote = regexp.MustCompile(`wrote (\d+) row file\(s\)`)

func TestStoryWCTUp4Count(t *testing.T) {
	s := &Story{World: journeys.NewWorld(t), ID: "WCT"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	shop := s.Seed(s.Remote(wctClientSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	inOrg := wOrg + "/org"
	m.LoreIn(pc, "org", "config", "set", "--org", wOrg, "--key", "repos", "--add", "github.com/client-co/*", "--yes").OK(t)
	m.LoreIn(pc, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", "github.com/acme-co/*", "--yes").OK(t)
	m.Lore(m.Home, "sync", "--setup", wSetup).OK(t)

	// the client repo, linked to its own folder of the org store
	cc := m.Clone(shop, "shop")
	m.MoveClone(t, cc, filepath.Join(m.Home, "code", "shop"))
	m.LoreIn(cc, "org", "link", "--org", wOrg, "--project", "shop", "--yes").OK(t)
	// a second clone of it outside the setup's folders (no setup applies there)
	oc := m.Clone(shop, "shop-out")
	m.MoveClone(t, oc, filepath.Join(m.Home, "elsewhere", "shop"))
	m.LoreIn(oc, "org", "link", "--org", wOrg, "--project", "shop", "--yes").OK(t)
	// a third clone not linked: only the org's repos: pattern ties it to the org
	nc := m.Clone(shop, "shop-unlinked")
	m.MoveClone(t, nc, filepath.Join(m.Home, "code", "shop-unlinked"))
	// the own repo, linked to its folder
	m.LoreIn(pc, "org", "link", "--org", wOrg, "--project", "platform", "--own", "--yes").OK(t)
	m.Lore(m.Home, "sync", "--setup", wSetup).OK(t)

	// the teammate who pushes first
	_, line := journeys.Invite(t, m, pc.Dir, wqMember.Email)
	bm := s.Persona(wqMember.Name, wqMember.Email).Laptop()
	journeys.GitHubLogin(t, s.World, bm, wqMember.Login)
	bc := bm.Clone(app, "platform")
	bm.MoveClone(t, bc, filepath.Join(bm.Home, "code", "platform"))
	journeys.Join(t, bm, bc.Dir, wSetup, line, wqMember, []string{wFolders})
	m.Lore(m.Home, "sync", "--setup", wSetup).OK(t)

	clone := storeCloneDir(t, m)
	places := []struct{ name, dir string }{
		{"client-repo", cc.Dir},
		{"client-outside-setup", oc.Dir},
		{"client-unlinked", nc.Dir},
		{"own-linked", pc.Dir},
		{"store-folder", filepath.Join(clone, "shop")},
	}
	for i, p := range places {
		s.Step("D-684/sync-counts-what-git-moved/"+p.name, func() {
			for j, body := range []string{"the teammate notes the release train", "the teammate keeps a runbook"} {
				bm.LoreIn(bc, "memory", "add", "--body", body+" "+strconv.Itoa(i)+strconv.Itoa(j), "--to", wOrg+"/platform").OK(t)
			}
			bm.LoreIn(bc, "sync").OK(t)
			for j, body := range []string{"the shop deploys from a tag", "the shop's staging is shared", "orders are idempotent"} {
				m.Lore(p.dir, "memory", "add", "--body", body+" "+strconv.Itoa(i)+strconv.Itoa(j)).OK(t)
			}
			// local commits of another folder, made from the home folder, ride the same push
			m.Lore(m.Home, "memory", "add", "--body", "the org rota changes monthly "+strconv.Itoa(i), "--to", inOrg).OK(t)
			m.Lore(m.Home, "memory", "add", "--body", "the platform pins go 1.22 "+strconv.Itoa(i), "--to", wOrg+"/platform").OK(t)
			m.Lore(m.Home, "memory", "add", "--body", "the platform has two regions "+strconv.Itoa(i), "--to", wOrg+"/platform").OK(t)
			head := wqGit(t, clone, "rev-parse", "HEAD")
			remoteBefore := wqGit(t, store.Dir, "rev-parse", "main")
			r := m.Lore(p.dir, "sync").OK(t)
			remoteAfter := wqGit(t, store.Dir, "rev-parse", "main")
			sent, received := wqCounts(t, r)
			went := wctCount(t, store.Dir, remoteBefore+".."+remoteAfter)
			// the commits that arrived: on the remote before the push, not here before the sync
			arrived := wctCount(t, clone, head+".."+remoteBefore)
			if sent != went {
				t.Errorf("[WCT D-684 %s] sync says sent %d, git rev-list %s..%s on the store remote counts %d:\n%s", p.name, sent, remoteBefore[:8], remoteAfter[:8], went, r)
			}
			if received != arrived {
				t.Errorf("[WCT D-684 %s] sync says received %d, git counts %d teammate commits that arrived:\n%s", p.name, received, arrived, r)
			}
			r.Says(t, "lore: pushed store")
		})
	}

	s.Step("D-691/wrote-n-is-the-files-the-commit-holds", func() {
		// the owner ran it from the home folder under a coding agent: the
		// agent's first write of the day registers its actors rows too
		s.AdvanceDays(1)
		home := pc.With(wqAgentEnv("wct-session-1")...)
		home.Dir = m.Home
		shopTo := []string{"--to", wOrg + "/shop"}
		for _, args := range [][]string{
			append([]string{"memory", "add", "--body", "the shop's cdn purges on deploy"}, shopTo...),
			append([]string{"decision", "add", "--title", "orders use uuids", "--body", "uuids, not serials"}, shopTo...),
			// the folder's first task list: its commit adds the folder's _meta.json
			append([]string{"task", "add", "--title", "rotate the shop's api key"}, shopTo...),
			append([]string{"memory", "add", "--body", "the org's vpn is wireguard"}, "--to", inOrg),
		} {
			head := wqGit(t, clone, "rev-parse", "HEAD")
			r := home.Lore(args...).OK(t)
			files := map[string]bool{}
			for _, f := range strings.Fields(wqGit(t, clone, "log", "--name-only", "--format=", head+"..HEAD")) {
				files[f] = true
			}
			if said := wctWroteCount(r.Out()); said != len(files) {
				t.Errorf("[WCT D-691] lore %s says it wrote %d file(s), its commits hold %d (%v):\n%s", strings.Join(args[:2], " "), said, len(files), files, r)
			}
			if args[1] == "task" {
				r.Says(t, "_meta.json")
			}
		}
	})
}

// wctWroteCount is how many row files a write's "lore: wrote …" lines name:
// one per "lore: wrote <file>" line, N per "wrote N row file(s)".
func wctWroteCount(out string) int {
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if !strings.HasPrefix(l, "lore: wrote ") && !strings.HasPrefix(l, "lore: removed ") {
			continue
		}
		if got := wctWrote.FindStringSubmatch(l); got != nil {
			k, _ := strconv.Atoi(got[1])
			n += k
			continue
		}
		n++
	}
	return n
}

// wctCount is git rev-list --count of rng in dir.
func wctCount(t *testing.T, dir, rng string) int {
	t.Helper()
	n, err := strconv.Atoi(wqGit(t, dir, "rev-list", "--count", rng))
	if err != nil {
		t.Fatal(err)
	}
	return n
}
