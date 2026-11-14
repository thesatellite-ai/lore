package app

// hookindex_test.go — docs bugs B1/B2 (plain files round, 2026-11-12).
//
// B1: `git commit -a`, `git commit <path>` and `git commit --amend -a` run
// pre-commit with GIT_INDEX_FILE naming a temporary index that holds what
// is being committed. `lore check leaks --staged` dropped that variable
// with the other repo-locating ones and read the default .git/index, so a
// secret written into a row and committed with -a went through, while
// `git add` + `git commit` was refused. Every form must be refused, in a
// store clone and in a code repo.
//
// B2: the refusal hint in a store clone named .lore/data/secret_allows,
// which a store does not have; it names the store's own place.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/scan"
)

// pfStoreWorld is a person's code repo linked to a git store (a bare repo
// on disk reached through url.insteadOf) with one memory written to the
// store; it returns the world, the repo, lore's store clone, the bare
// remote and the memory's id.
func pfStoreWorld(t *testing.T) (w *cliWorld, repo, clone, bare, memID string) {
	t.Helper()
	w = newCLIWorld(t).person().author().unscoped()
	home := w.env["HOME"]
	hosts := w.path("hosts")
	bare = filepath.Join(hosts, "acme", "notes.git")
	if err := os.MkdirAll(bare, 0o700); err != nil {
		t.Fatal(err)
	}
	w.git(t, bare, "init", "-q", "--bare", "-b", "main")
	gitcfg := "[user]\n\tname = Pat Example\n\temail = pat@example.com\n[init]\n\tdefaultBranch = main\n[url \"" + filepath.ToSlash(hosts) + "/\"]\n\tinsteadOf = https://github.com/\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(gitcfg), 0o600); err != nil {
		t.Fatal(err)
	}
	repo = w.path("repo")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	w.git(t, repo, "init", "-q", "-b", "main")
	w.mustLore(t, repo, "init")
	w.mustLore(t, repo, "store", "init", "notes", "https://github.com/acme/notes.git", "--yes")
	code, out, errOut := w.lore(t, repo, "memory", "add", "--body", "the widget cache warms at boot", "--to", "notes")
	if code != 0 {
		t.Fatalf("memory add: exit %d\n%s\n%s", code, out, errOut)
	}
	memID = pfMemoryID.FindString(out + errOut)
	if memID == "" {
		t.Fatalf("no memory id in:\n%s\n%s", out, errOut)
	}
	w.mustLore(t, repo, "sync") // C5: the note reaches the store's remote with the person's sync
	return w, repo, filepath.Join(home, ".lore", "stores", "acme", "notes"), bare, memID
}

// rowFileOf is the path of the row file id.json under dir.
func rowFileOf(t *testing.T, dir, id string) string {
	t.Helper()
	var found string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == id+".json" && !strings.Contains(filepath.ToSlash(p), "/.git/") {
			found = p
		}
		return nil
	})
	if found == "" {
		t.Fatalf("no row file %s.json under %s", id, dir)
	}
	return found
}

// leakHook writes a pre-commit hook into dir's .git/hooks that runs this
// test binary as lore (`lore check leaks --staged`), as lore's block does
// with the release binary (a test binary is never wired in by lore).
func leakHook(t *testing.T, dir string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(dir, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o750); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\n" + envCLIChild + "=1 LORE_GIT_HOOK=pre-commit '" + exe + "' check leaks --staged || exit $?\n"
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(body), 0o755); err != nil { // #nosec G306 -- a hook git must run
		t.Fatal(err)
	}
}

// hookedGit runs git in dir with the clone's own hooks; it returns the
// exit status and the combined output.
func hookedGit(t *testing.T, w *cliWorld, dir string, args ...string) (int, string) {
	t.Helper()
	cmd := &exec.Cmd{Path: w.gitBin, Args: append([]string{w.gitBin, "-C", dir}, args...), Env: w.environ(map[string]string{"GIT_CONFIG_NOSYSTEM": "1"})}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), string(out)
		}
		t.Fatalf("git %v: %v", args, err)
	}
	return 0, string(out)
}

func TestHandCommitSecretRefusedWithEveryCommitForm(t *testing.T) {
	t.Parallel()
	secret := "ghp_" + "a1B2c3D4e5F6g7H8" + "i9J0k1L2m3N4o5P6q7R8" // token-shaped, built at run time
	w, repo, clone, _, memID := pfStoreWorld(t)
	leakHook(t, clone)
	file := rowFileOf(t, clone, memID)
	clean, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(clean), "the widget cache warms at boot") {
		t.Fatalf("setup: row file does not hold the body:\n%s", clean)
	}
	dirty := strings.Replace(string(clean), "the widget cache warms at boot", "the deploy token is "+secret, 1)
	rel, _ := filepath.Rel(clone, file)
	rel = filepath.ToSlash(rel)

	forms := map[string][]string{
		"commit -a":         {"commit", "-q", "-a", "-m", "note the key"},
		"commit <path>":     {"commit", "-q", "-m", "note the key", "--", rel},
		"commit --amend -a": {"commit", "-q", "--amend", "-a", "-m", "note the key"},
		"add then commit":   nil, // git add, then a plain git commit
	}
	head := strings.TrimSpace(w.git(t, clone, "rev-parse", "HEAD"))
	for name, args := range forms {
		if err := os.WriteFile(file, []byte(dirty), 0o600); err != nil {
			t.Fatal(err)
		}
		if args == nil {
			w.git(t, clone, "add", "--", rel)
			args = []string{"commit", "-q", "-m", "note the key"}
		}
		code, out := hookedGit(t, w, clone, args...)
		if code == 0 {
			t.Fatalf("store clone, %s: the commit carrying a secret went through:\n%s", name, out)
		}
		if !strings.Contains(out, "E_LEAK_BLOCKED") && !strings.Contains(out, "carries a secret") {
			t.Fatalf("store clone, %s: refused, but not by the secret check:\n%s", name, out)
		}
		if strings.Contains(out, secret) {
			t.Fatalf("store clone, %s: the refusal printed the secret", name)
		}
		// B2: a store has no .lore/data; the hint names the store's place
		if strings.Contains(out, ".lore/data/secret_allows") || !strings.Contains(out, "<folder>/secret_allows/") {
			t.Fatalf("store clone, %s: the hint does not name the store's secret_allows place:\n%s", name, out)
		}
		if now := strings.TrimSpace(w.git(t, clone, "rev-parse", "HEAD")); now != head {
			t.Fatalf("store clone, %s: HEAD moved %s → %s", name, head, now)
		}
		// put the row back (index and file) for the next form
		w.git(t, clone, "reset", "-q", "--", rel)
		if err := os.WriteFile(file, clean, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// an allow entry for that value, written by lore into the store (no
	// .lore/data there), lets the same hand commit through
	hash := strconv.FormatUint(uint64(scan.HashMatch(secret)), 10)
	for _, rule := range []string{"github_token", "github-pat"} { // both rules match a ghp_ token
		if code, out, errOut := w.lore(t, repo, "secrets", "allow", memID, "--rule", rule, "--field", "body", "--match-hash", hash, "--reason", "a test fixture"); code != 0 {
			t.Fatalf("secrets allow: exit %d\n%s\n%s", code, out, errOut)
		}
	}
	if err := os.WriteFile(file, []byte(dirty), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out := hookedGit(t, w, clone, "commit", "-q", "-a", "-m", "note the fixture"); code != 0 {
		t.Fatalf("store clone, commit -a with a committed allow entry: refused:\n%s", out)
	}

	// a code repo's own .lore/data row, committed with -a: refused too,
	// and its hint still names .lore/data/secret_allows
	w.mustLore(t, repo, "memory", "add", "--body", "the queue drains at night")
	w.git(t, repo, "add", "-A")
	w.git(t, repo, "commit", "-q", "-m", "lore")
	leakHook(t, repo)
	var row string
	_ = filepath.WalkDir(filepath.Join(repo, ".lore", "data", "memories"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".json") {
			if b, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(b), "the queue drains at night") {
				row = p
			}
		}
		return nil
	})
	if row == "" {
		t.Fatal("setup: no code repo row holding the body")
	}
	b, err := os.ReadFile(row)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(row, []byte(strings.Replace(string(b), "the queue drains at night", "the deploy token is "+secret, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out := hookedGit(t, w, repo, "commit", "-q", "-a", "-m", "note the key")
	if code == 0 {
		t.Fatalf("code repo, commit -a: the commit carrying a secret went through:\n%s", out)
	}
	if !strings.Contains(out, "carries a secret") || !strings.Contains(out, ".lore/data/secret_allows") {
		t.Fatalf("code repo, commit -a: not refused by the secret check with the repo hint:\n%s", out)
	}
}
