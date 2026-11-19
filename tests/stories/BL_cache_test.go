//go:build stories && !windows

package stories

// BL_cache_test.go — BL-25 (devco:D-411, devco:V-17): a code branch's
// cache follows its mode on every path of the branch rule. A shared branch
// reads main's cache and keeps no layer cache; an isolated one reads its
// own; an isolated branch's notes never reach main's cache file; switching
// a branch share <-> isolate (by hand or by a teammate's pushed choice)
// moves it on the next command (a teammate's own share is refused: the
// mode of a company folder's branch is the admin's, devco D-694); landed
// and dropped branches leave no
// cache; doctor lists a stray layer cache and doctor --fix removes it. Every
// name, email and url is made up.

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// layerCacheOf lists the layer cache folders of branch on the admin's
// machine.
func (w *blWorld) layerCacheOf(branch string) []string {
	key := strings.ReplaceAll(strings.ReplaceAll(branch, "%", "%25"), "/", "%2F")
	got, _ := filepath.Glob(w.admin.LorePath("cache", "stores", "*", "*", "_layers", "*", key))
	return got
}

// mainCacheHolds reports the main (non-layer) cache files of the store
// folders on the admin's machine that hold text (the sqlite file and its
// write-ahead log, byte for byte): an isolated branch's note found there
// leaked into the cache every shared branch reads.
func (w *blWorld) mainCacheHolds(t *testing.T, text string) []string {
	t.Helper()
	var hits []string
	root := w.admin.LorePath("cache", "stores")
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "_layers" {
				return filepath.SkipDir
			}
			return nil
		}
		if b, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(b), text) {
			hits = append(hits, p)
		}
		return nil
	})
	return hits
}

// noLeak fails when any of texts sits in a main cache file.
func (w *blWorld) noLeak(t *testing.T, texts ...string) {
	t.Helper()
	// a read on main first, so main's cache is current
	cur := strings.TrimSpace(w.s.Git(w.aapp.Dir, "symbolic-ref", "--short", "HEAD"))
	w.aapp.Git("switch", "-q", "main")
	w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Never(t, texts...)
	w.aapp.Git("switch", "-q", cur)
	for _, text := range texts {
		if hits := w.mainCacheHolds(t, text); len(hits) != 0 {
			t.Fatalf("an isolated branch's note %q is in main's cache: %v", text, hits)
		}
	}
}

// TestStoryBL25CacheFollowsTheMode (BL-25): every path of the branch rule
// picks the matching cache, and no isolated note reaches main's cache.
func TestStoryBL25CacheFollowsTheMode(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	dev, devApp := w.blJoin(blDev)
	s.Step("day1/default-rule-isolates-into-its-own-cache", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/a")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "note of feat-a only").OK(t).Says(t, "feat/a notes")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "note of feat-a only")
		if len(w.layerCacheOf("feat/a")) == 0 {
			t.Fatal("isolated feat/a reads through no layer cache of its own")
		}
		w.noLeak(t, "note of feat-a only")
	})
	s.Step("day1/main-like-pattern-reads-mains-cache", func() {
		w.aapp.Git("switch", "-q", "main")
		w.aapp.Git("switch", "-q", "-c", "dev")
		w.admin.LoreIn(w.aapp, "branch", "mode").OK(t).Says(t, "shared", "step 3")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "dev writes for everyone").OK(t).Never(t, "dev notes")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "dev writes for everyone").Never(t, "note of feat-a only")
		if got := w.layerCacheOf("dev"); len(got) != 0 {
			t.Fatalf("main-like dev keeps a layer cache: %v", got)
		}
		w.aapp.Git("switch", "-q", "-c", "release/1.0")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "dev writes for everyone")
		if got := w.layerCacheOf("release/1.0"); len(got) != 0 {
			t.Fatalf("main-like release/1.0 keeps a layer cache: %v", got)
		}
	})
	s.Step("day1/per-branch-isolate-moves-dev-to-its-own-cache", func() {
		w.aapp.Git("switch", "-q", "dev")
		w.admin.LoreIn(w.aapp, "branch", "isolate", "--branch", "dev", "--yes").OK(t)
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "dev keeps this one").OK(t).Says(t, "dev notes")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "dev keeps this one", "dev writes for everyone")
		if len(w.layerCacheOf("dev")) == 0 {
			t.Fatal("policy-isolated dev reads through main's cache")
		}
		w.noLeak(t, "dev keeps this one")
	})
	s.Step("day1/per-branch-share-moves-feat-a-to-mains-cache", func() {
		w.aapp.Git("switch", "-q", "feat/a")
		w.admin.LoreIn(w.aapp, "branch", "share", "--branch", "feat/a", "--yes").OK(t).Says(t, "shared notes")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "the checkout runs on port 8080").Never(t, "note of feat-a only")
		if got := w.layerCacheOf("feat/a"); len(got) != 0 {
			t.Fatalf("shared feat/a kept its layer cache: %v", got)
		}
		w.noLeak(t, "note of feat-a only", "dev keeps this one")
		// back to isolated: its own cache again, its notes back
		w.admin.LoreIn(w.aapp, "branch", "share", "--branch", "feat/a", "--undo", "--yes").OK(t)
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "note of feat-a only")
		if len(w.layerCacheOf("feat/a")) == 0 {
			t.Fatal("feat/a isolated again reads through no layer cache")
		}
		w.admin.LoreIn(w.aapp, "sync").OK(t)
	})
	s.Step("day1/a-teammates-share-is-refused-the-admins-reaches-the-cache-on-the-next-command", func() {
		// devco D-694 (owner use pass 5, the role rule): a branch's mode in
		// a company folder is a policy of that folder, for everyone in it:
		// a member's share is refused with the ask-an-admin hint and
		// nothing is written; the admin's share reaches the teammate's
		// cache after a sync, and so does the admin's undo.
		dev.LoreIn(devApp, "sync").OK(t)
		before := strings.TrimSpace(w.s.Git(w.notes.Dir, "rev-parse", "HEAD"))
		dev.LoreIn(devApp, "branch", "share", "--branch", "feat/a", "--yes").Refused(t, "E_REFUSED", "only an admin of the org", "ask an admin of "+blOrg, blAdmin.Name, "nothing was written")
		dev.LoreIn(devApp, "branch", "isolate", "--branch", "feat/a", "--yes").Refused(t, "E_REFUSED", "only an admin of the org", blAdmin.Name)
		dev.LoreIn(devApp, "sync").OK(t)
		if after := strings.TrimSpace(w.s.Git(w.notes.Dir, "rev-parse", "HEAD")); after != before {
			t.Fatalf("devco:D-694: a member's refused branch share reached the store (%s -> %s)", before, after)
		}
		w.admin.LoreIn(w.aapp, "branch", "share", "--branch", "feat/a", "--yes").OK(t).Says(t, "shared notes")
		w.admin.LoreIn(w.aapp, "sync").OK(t)
		dev.LoreIn(devApp, "sync").OK(t)
		devApp.Git("switch", "-q", "-c", "feat/a")
		dev.LoreIn(devApp, "branch", "mode").OK(t).Says(t, "shared", "step 1")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Never(t, "note of feat-a only")
		if got := w.layerCacheOf("feat/a"); len(got) != 0 {
			t.Fatalf("feat/a shared by the admin kept its layer cache: %v", got)
		}
		w.noLeak(t, "note of feat-a only")
		w.admin.LoreIn(w.aapp, "branch", "share", "--branch", "feat/a", "--undo", "--yes").OK(t)
		w.admin.LoreIn(w.aapp, "sync").OK(t)
		dev.LoreIn(devApp, "sync").OK(t)
		dev.LoreIn(devApp, "branch", "mode").OK(t).Never(t, "step 1")
		devApp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "note of feat-a only")
	})
	s.Step("day1/offline-and-stacked-keep-their-own-caches", func() {
		// a commit of its own, so feat/a-part2 stacks on feat/a (BL-23)
		w.aapp.WriteFile("a.txt", "a\n")
		w.aapp.CommitAll("feat: a")
		away := w.notes.Dir + ".away"
		if err := os.Rename(w.notes.Dir, away); err != nil {
			t.Fatal(err)
		}
		w.aapp.Git("switch", "-q", "-c", "feat/a-part2")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "part two written offline").OK(t).Says(t, "feat/a-part2 notes")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "part two written offline", "note of feat-a only")
		if len(w.layerCacheOf("feat/a-part2")) == 0 {
			t.Fatal("the stacked branch reads through no layer cache of its own while offline")
		}
		w.noLeak(t, "part two written offline", "note of feat-a only")
		if err := os.Rename(away, w.notes.Dir); err != nil {
			t.Fatal(err)
		}
	})
	s.Step("day2/landed-and-dropped-leave-no-cache", func() {
		s.AdvanceDays(1)
		w.aapp.Git("switch", "-q", "feat/a")
		w.aapp.Git("push", "-q", "origin", "feat/a")
		w.admin.LoreIn(w.aapp, "sync").OK(t)
		w.aapp.Git("switch", "-q", "main")
		w.aapp.Git("merge", "-q", "--no-ff", "-m", "Merge feat/a", "feat/a")
		w.aapp.Git("push", "-q", "origin", "main")
		w.aapp.Git("fetch", "-q", "origin")
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/a").OK(t).Says(t, "landed")
		if got := w.layerCacheOf("feat/a"); len(got) != 0 {
			t.Fatalf("landed feat/a left its layer cache: %v", got)
		}
		w.admin.LoreIn(w.aapp, "branch", "drop", "--branch", "feat/a-part2").OK(t).Says(t, "dropped")
		if got := w.layerCacheOf("feat/a-part2"); len(got) != 0 {
			t.Fatalf("dropped feat/a-part2 left its layer cache: %v", got)
		}
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "note of feat-a only").Never(t, "part two written offline")
	})
	s.Step("day2/doctor-lists-a-stray-layer-cache-and-fix-removes-it", func() {
		dirs := w.layerCacheOf("dev")
		if len(dirs) == 0 {
			t.Fatal("no dev layer cache to copy")
		}
		ghost := filepath.Join(filepath.Dir(dirs[0]), "ghost%2Fgone")
		if err := os.CopyFS(ghost, os.DirFS(dirs[0])); err != nil {
			t.Fatal(err)
		}
		w.admin.LoreIn(w.aapp, "doctor").Says(t, "stray layer cache of ghost/gone", "lore doctor --fix")
		// a client repo has no .lore of its own, so lore fix refuses there;
		// doctor --fix runs the same repairs (and lore fix does in a repo
		// with its own notes)
		w.admin.LoreIn(w.aapp, "doctor", "--fix").Says(t, "stray layer cache of ghost/gone", "fixed")
		if _, err := os.Stat(ghost); !os.IsNotExist(err) {
			t.Fatalf("lore doctor --fix left the stray layer cache %s (%v)", ghost, err)
		}
		w.admin.LoreIn(w.aapp, "doctor").Never(t, "stray layer cache")
		if len(w.layerCacheOf("dev")) == 0 {
			t.Fatal("lore doctor --fix removed the layer cache of isolated dev, which still has notes")
		}
	})
}
