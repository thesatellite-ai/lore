//go:build stories && !windows

package stories

// WX_uppaths_test.go — owner use pass 1 (devco D-534, D-535): a lore home
// made by copying another one (a preview home made from ~/.lore) reaches
// only its own clones and its own render.
//
//	day1: StartTeam in platform, web linked as an own repo (--own), sync
//	copy: ~/.lore copied to ~/.lore-dev, LORE_HOME=~/.lore-dev from here on;
//	      its state/store-clones.json names the old home's clone (`why:
//	      moved`, what an older set-url wrote for a default-place clone)
//	D-534: store show warns and uses the copy's own clone (the old home's
//	       clone is never fetched); the record keeps no absolute path; the
//	       store clone's pre-commit block names neither home nor needs the
//	       build that wrote it; a clone lore makes gets its hook at once;
//	       doctor --fix rewrites a block pinned to the old home
//	D-535: sync in the linked own repo says which local files it wrote and
//	       their imports name the render under ~/.lore-dev, not ~/.lore

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

func TestStoryWXUpPaths(t *testing.T) {
	s := &Story{World: journeys.NewWorld(t), ID: "WX"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote("github.com/acme-co/web"), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	m.Lore(pc.Dir, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", "github.com/acme-co/*", "--yes").OK(t)
	wc := m.Clone(web, "web")
	m.MoveClone(t, wc, filepath.Join(m.Home, "code", "web"))
	m.Lore(wc.Dir, "org", "link", "--org", wOrg, "--project", "web", "--own", "--yes").OK(t)
	m.Lore(wc.Dir, "rule", "add", "--body", "always run the linter before each commit", "--severity", "must", "--to", wOrg+"/web", "--yes").OK(t)
	m.Lore(wc.Dir, "sync").OK(t)

	oldHome := m.LorePath()
	devHome := filepath.Join(m.Home, ".lore-dev")
	oldClone := filepath.Join(oldHome, "stores", "acme-co", "lore-team")
	devClone := filepath.Join(devHome, "stores", "acme-co", "lore-team")
	if out, err := exec.Command("cp", "-a", oldHome, devHome).CombinedOutput(); err != nil {
		t.Fatalf("copy the home: %v\n%s", err, out)
	}
	m.SetEnv("LORE_HOME", devHome)
	record := filepath.Join(devHome, "state", "store-clones.json")
	if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, record, `{"clones":{"github.com/acme-co/lore-team":{"path":"`+oldClone+`","why":"moved","set_at":"2026-11-17T00:00:00Z"}}}`+"\n")
	fetchHead := func() time.Time {
		st, err := os.Stat(filepath.Join(oldClone, ".git", "FETCH_HEAD"))
		if err != nil {
			return time.Time{}
		}
		return st.ModTime()
	}

	s.Step("D-534/copied-home-uses-its-own-clone", func() {
		before := fetchHead()
		time.Sleep(1100 * time.Millisecond) // a fetch in the old clone would move FETCH_HEAD's mtime
		r := m.Lore(m.Home, "store", "show").OK(t).Says(t, "another lore home", "ignored")
		if strings.Contains(r.Stdout, oldClone) || !strings.Contains(r.Stdout, devClone) {
			t.Fatalf("store show under the copied home must show its own clone %s:\n%s", devClone, r.Stdout)
		}
		if after := fetchHead(); !after.Equal(before) {
			t.Fatalf("a read under the copied home fetched in the old home's clone %s (FETCH_HEAD %v → %v)", oldClone, before, after)
		}
		b, _ := os.ReadFile(record)
		if strings.Contains(string(b), oldHome+string(filepath.Separator)) {
			t.Fatalf("the copied home's record still names the old home:\n%s", b)
		}
		m.Lore(m.Home, "store", "show").OK(t).Never(t, "another lore home")
	})

	hook := func(clone string) string {
		b, _ := os.ReadFile(filepath.Join(clone, ".git", "hooks", "pre-commit"))
		return string(b)
	}
	s.Step("D-534/store-hook-follows-its-home", func() {
		// the copied clone still carries the hook the old home wrote (as the
		// owner's did); doctor --fix rewrites it
		writeFile(t, filepath.Join(devClone, ".git", "hooks", "pre-commit"), "#!/bin/sh\n# >>> lore store check (lore writes this block; lines outside it are yours)\nLORE_HOME='"+oldHome+"'; export LORE_HOME\nif command -v '/gone/build/lore' >/dev/null 2>&1; then\n  '/gone/build/lore' sync hook pre-commit || exit $?\nfi\n# <<< lore store check\n")
		m.Lore(pc.Dir, "doctor", "--fix")
		h := hook(devClone)
		if strings.Contains(h, oldHome) || strings.Contains(h, devHome) || strings.Contains(h, "/gone/build/lore") || !strings.Contains(h, `LORE_HOME="$(cd ../../.. && pwd -P)"`) {
			t.Fatalf("after doctor --fix the store clone's hook must find its home from its place and lore on PATH:\n%s", h)
		}
		// a clone lore makes now gets its hook with it, not at the first write
		if err := os.RemoveAll(devClone); err != nil {
			t.Fatal(err)
		}
		m.Lore(m.Home, "memory", "list", "--store", wOrg).OK(t)
		if h := hook(devClone); !strings.Contains(h, "sync hook pre-commit") {
			t.Fatalf("a new store clone has no pre-commit hook until its first write:\n%q", h)
		}
	})

	s.Step("D-535/sync-says-what-it-wrote-and-points-at-this-home", func() {
		for _, f := range []string{"CLAUDE.local.md", filepath.Join(".cursor", "rules", "lore-private.mdc")} {
			_ = os.Remove(filepath.Join(wc.Dir, f))
		}
		r := m.Lore(wc.Dir, "sync").OK(t).Says(t, "wrote CLAUDE.local.md")
		_ = r
		b, err := os.ReadFile(filepath.Join(wc.Dir, "CLAUDE.local.md"))
		if err != nil {
			t.Fatalf("no CLAUDE.local.md after sync: %v", err)
		}
		if strings.Contains(string(b), "~/.lore/render/") || !strings.Contains(string(b), "@~/.lore-dev/render/") {
			t.Fatalf("CLAUDE.local.md must import the render under the lore home in use (~/.lore-dev):\n%s", b)
		}
		if out := wc.Git("status", "--porcelain"); strings.TrimSpace(out) != "" {
			t.Fatalf("the linked own repo's tracked files changed (D-494): %s", out)
		}
	})
}
