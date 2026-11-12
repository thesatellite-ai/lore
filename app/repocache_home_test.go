package app

// repocache_home_test.go — owner item 16: the repo store's cache lives
// under the lore home (~/.lore/cache/repo/<folder>-<hash>.db), never in
// the work tree. A read in a repo whose .gitignore lacks lore's lines then
// creates nothing in the repo, so it has nothing to hide through
// .git/info/exclude: the repo, info/exclude and .git/config stay byte for
// byte as they were. The cache is reused by the next command, and two
// checkouts get two caches.

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire"
)

// rcSnapshot hashes every file under dir (path, mode, content), leaving
// out only git's stat cache and its object store and reflogs (git itself
// writes them; lore's reads add no objects): every other file, lore's
// cache files included, must stay the same.
func rcSnapshot(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		slash := filepath.ToSlash(rel)
		if d.IsDir() {
			if slash == ".git/objects" || slash == ".git/logs" {
				return filepath.SkipDir
			}
			lines = append(lines, "dir "+slash)
			return nil
		}
		if slash == ".git/index" {
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		info, _ := d.Info()
		sum := sha256.Sum256(raw)
		lines = append(lines, hex.EncodeToString(sum[:8])+" "+info.Mode().String()+" "+slash)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

// rcRepoCaches lists the repo cache files (not their sidecars) under home.
func rcRepoCaches(t *testing.T, home string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(home, ".lore", "cache", "repo", "*.db"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// rcPlainRepo is a code repo named name with committed lore rows (one
// memory) whose .gitignore holds none of lore's lines, with no lore
// local file in the work tree and git's default info/exclude.
func rcPlainRepo(t *testing.T, w *cliWorld, name string) string {
	t.Helper()
	repo := w.path(name)
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	w.git(t, repo, "init", "-q", "-b", "main")
	w.mustLore(t, repo, "init")
	w.mustLore(t, repo, "memory", "add", "--body", "the "+name+" widget warms at boot")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("node_modules/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".lore/lore-cache.db", ".lore/lore-cache.db-wal", ".lore/lore-cache.db-shm", ".lore/lock", ".lore/state", ".lore/backups"} {
		if err := os.RemoveAll(filepath.Join(repo, filepath.FromSlash(p))); err != nil {
			t.Fatal(err)
		}
	}
	w.git(t, repo, "add", "-A")
	w.git(t, repo, "commit", "-q", "-m", "rows")
	if err := os.WriteFile(filepath.Join(repo, ".git", "info", "exclude"), []byte("# git ls-files --others --exclude-from=.git/info/exclude\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := strings.TrimSpace(w.git(t, repo, "status", "--porcelain", "--untracked-files=all")); st != "" {
		t.Fatalf("the repo is not clean before the read:\n%s", st)
	}
	return repo
}

func TestReadCacheLivesUnderHome(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person().author().unscoped()
	home := w.env["HOME"]
	repo := rcPlainRepo(t, w, "widgets")
	// the writes above made a cache under the home; the read must make
	// (or reuse) it there too, so start from none
	if err := os.RemoveAll(filepath.Join(home, ".lore", "cache", "repo")); err != nil {
		t.Fatal(err)
	}
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	config := filepath.Join(repo, ".git", "config")
	exBefore, _ := os.ReadFile(exclude)
	cfgBefore, _ := os.ReadFile(config)
	before := rcSnapshot(t, repo)

	code, out, errOut := w.lore(t, repo, "memory", "list")
	if code != 0 || !strings.Contains(out+errOut, "widgets widget warms") {
		t.Fatalf("memory list: exit %d\n%s\n%s", code, out, errOut)
	}
	if after := rcSnapshot(t, repo); after != before {
		t.Fatalf("a read changed the repo:\n%s", pfDiff(before, after))
	}
	if exAfter, _ := os.ReadFile(exclude); string(exAfter) != string(exBefore) {
		t.Fatalf("a read wrote .git/info/exclude:\n%s", exAfter)
	}
	if cfgAfter, _ := os.ReadFile(config); string(cfgAfter) != string(cfgBefore) {
		t.Fatalf("a read wrote .git/config:\n%s", cfgAfter)
	}
	if strings.Contains(errOut, "info/exclude") {
		t.Fatalf("a read announced an exclude write:\n%s", errOut)
	}
	caches := rcRepoCaches(t, home)
	if len(caches) != 1 || !strings.HasPrefix(filepath.Base(caches[0]), "widgets-") {
		t.Fatalf("want one repo cache named widgets-<hash>.db under the home, got %v", caches)
	}
	first, err := os.Stat(caches[0])
	if err != nil {
		t.Fatal(err)
	}

	// the next command reuses the same file (no second cache, same file)
	code, out, errOut = w.lore(t, repo, "memory", "search", "widget")
	if code != 0 || !strings.Contains(out+errOut, "widgets widget warms") {
		t.Fatalf("memory search: exit %d\n%s\n%s", code, out, errOut)
	}
	again := rcRepoCaches(t, home)
	if len(again) != 1 || again[0] != caches[0] {
		t.Fatalf("the second command did not reuse the cache: %v then %v", caches, again)
	}
	if second, serr := os.Stat(again[0]); serr != nil || !os.SameFile(first, second) {
		t.Fatalf("the cache file was replaced between commands (%v)", serr)
	}
	if after := rcSnapshot(t, repo); after != before {
		t.Fatalf("the second read changed the repo:\n%s", pfDiff(before, after))
	}

	// lore where names the cache under the home
	if code, out, errOut = w.lore(t, repo, "where"); code != 0 || !strings.Contains(out, caches[0]) {
		t.Fatalf("lore where does not list %s: exit %d\n%s\n%s", caches[0], code, out, errOut)
	}
}

func TestTwoReposGetTwoCaches(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person().author().unscoped()
	home := w.env["HOME"]
	a := rcPlainRepo(t, w, "alpha")
	b := rcPlainRepo(t, w, filepath.Join("second", "alpha")) // same folder name, another path
	if err := os.RemoveAll(filepath.Join(home, ".lore", "cache", "repo")); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{a, b} {
		if code, out, errOut := w.lore(t, r, "memory", "list"); code != 0 {
			t.Fatalf("memory list in %s: exit %d\n%s\n%s", r, code, out, errOut)
		}
	}
	caches := rcRepoCaches(t, home)
	if len(caches) != 2 || caches[0] == caches[1] {
		t.Fatalf("want two repo caches, got %v", caches)
	}
	for _, c := range caches {
		if !strings.HasPrefix(filepath.Base(c), "alpha-") {
			t.Fatalf("a repo cache is not named by its repo folder: %s", c)
		}
	}
	// each cache holds only its own repo's rows
	_, outA, _ := w.lore(t, a, "memory", "list")
	if strings.Contains(outA, "second") {
		t.Fatalf("repo %s reads another repo's rows:\n%s", a, outA)
	}
}

// An earlier build's .lore/lore-cache.db is left alone by every command:
// doctor names it as safe to delete and doctor --fix removes it.
func TestOldRepoCacheDoctorFix(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person().author().unscoped()
	repo := rcPlainRepo(t, w, "legacy")
	old := filepath.Join(repo, ".lore", "lore-cache.db")
	if err := os.WriteFile(old, []byte("old cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := w.lore(t, repo, "memory", "list"); code != 0 {
		t.Fatalf("memory list: exit %d\n%s\n%s", code, out, errOut)
	}
	if b, err := os.ReadFile(old); err != nil || string(b) != "old cache" {
		t.Fatalf("a read touched the old cache (%v): %q", err, b)
	}
	_, out, errOut := w.lore(t, repo, "doctor")
	if !strings.Contains(out+errOut, "old_repo_cache") || !strings.Contains(out+errOut, old) || !strings.Contains(out+errOut, "safe to delete") {
		t.Fatalf("doctor does not name the old cache as safe to delete:\n%s\n%s", out, errOut)
	}
	_, out, errOut = w.lore(t, repo, "doctor", "--fix")
	if _, err := os.Lstat(old); err == nil {
		t.Fatalf("doctor --fix kept %s:\n%s\n%s", old, out, errOut)
	}
}

// forgetLocalState removes what a fresh clone of the checkout at dir does
// not carry on this machine: the localStateFiles under dir/.lore and the
// repo cache under the world's lore home.
func forgetLocalState(t *testing.T, w *cliWorld, dir string) {
	t.Helper()
	for _, f := range localStateFiles {
		if err := os.RemoveAll(filepath.Join(dir, ".lore", f)); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := filepath.Glob(wire.RepoCachePath(filepath.Join(w.env["HOME"], ".lore"), dir) + "*")
	for _, f := range files {
		if err := os.Remove(f); err != nil {
			t.Fatal(err)
		}
	}
}
