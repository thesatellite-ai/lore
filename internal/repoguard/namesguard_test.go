package repoguard

// namesguard_test.go — devco D-981, D-982: names that must never appear in
// this public repository (clients, companies, people, private hosts) once
// reached tests and docs. The list of such words is private: it lives on each
// maintainer's machine ($NAMES_GUARD_FILE, else
// ~/.config/names-guard/terms.txt) and nothing derived from it (no hash, no
// length, no count) is committed, since a hash of a short name is reversed in
// seconds (D-982 removed the hash list D-981 had added). tests/lib/
// names-guard.sh is the one check: whole words, case-insensitive, in tracked
// text files and paths, in commit authors and messages, in added lines or on
// stdin; it names the place, never the word, and without a list it prints one
// line and passes. These tests run it on the real tree when a list exists and
// pin its behaviour with made-up words in scratch repositories.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// namesGuardScript is the guard every caller runs (this test, the pre-push
// hook, the landing and the throwaway CI script).
var namesGuardScript = filepath.Join(repoRoot, "tests", "lib", "names-guard.sh")

// namesGuardList is where the guard reads the private list on this machine.
func namesGuardList() string {
	if p := os.Getenv("NAMES_GUARD_FILE"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("~", ".config", "names-guard", "terms.txt")
	}
	return filepath.Join(home, ".config", "names-guard", "terms.txt")
}

// runNamesGuard runs the guard in dir with extra environment and stdin and
// returns its combined output and exit code.
func runNamesGuard(t *testing.T, dir string, env []string, stdin string, args ...string) (string, int) {
	t.Helper()
	script, err := filepath.Abs(namesGuardScript)
	if err != nil {
		t.Fatal(err)
	}
	cmd := testCmd(t, "bash", append([]string{script}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("names-guard.sh %v: %v", args, err)
	}
	return string(out), code
}

// writeNamesList writes a temp list (or allow file) of made-up terms with a
// comment line and a blank line, as a maintainer's list would have.
func writeNamesList(t *testing.T, terms ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "terms.txt")
	body := "# made-up test terms\n\n" + strings.Join(terms, "\n") + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// guardRepo is a scratch repository whose tracked files the guard scans.
func guardRepo(t *testing.T, files map[string]string) *hookRepo {
	t.Helper()
	for _, tool := range []string{"git", "bash"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	r := &hookRepo{t: t, dir: gitInitRepo(t)}
	r.git("config", "user.email", "t@example.com")
	r.git("config", "user.name", "t")
	for rel, body := range files {
		p := filepath.Join(r.dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r.git("add", ".")
	r.git("commit", "-qm", "files")
	return r
}

// mentionsTerm reports output that prints a made-up listed word.
func mentionsTerm(out string) bool {
	low := strings.ToLower(out)
	return strings.Contains(low, "zorblax") || strings.Contains(low, "acme-secret")
}

// TestNoListedNamesInTrackedFiles is the guard on this repository: with the
// maintainer's private list on the machine, no tracked text file or path
// holds a listed word. Without the list (GitHub Actions, other contributors)
// it skips with the guard's one line. On a failure, replace the word on each
// named line with a made-up name (acme, alice, example.com).
func TestNoListedNamesInTrackedFiles(t *testing.T) {
	t.Parallel()
	list := namesGuardList()
	if _, err := os.Stat(list); err != nil {
		t.Skipf("names-guard: no list at %s, skipped", list)
	}
	out, code := runNamesGuard(t, repoRoot, nil, "", "--files")
	if code != 0 {
		t.Fatalf("tracked files hold words of the private names list (exit %d):\n%s", code, out)
	}
}

// TestNamesGuardFiles: a listed word in a tracked text file fails naming
// file:line, a listed word in a path fails naming its place in git ls-files,
// and neither prints the word; a longer word holding a term, a term joined by
// _ and a binary file are not hits.
func TestNamesGuardFiles(t *testing.T) {
	t.Parallel()
	env := []string{"NAMES_GUARD_FILE=" + writeNamesList(t, "Zorblax", "acme-secret")}
	r := guardRepo(t, map[string]string{
		"docs/bad.md":   "line one\nthe Zorblax repo\n",
		"mail.txt":      "mail x@acme-secret.example\n",
		"fine.md":       "zorblaxing and unzorblax and zorblax_app are other words\nacme or secret apart\n",
		"model.bin":     "zorblax\x00acme-secret\n",
		"zorblax/a.txt": "nothing here\n",
	})
	out, code := runNamesGuard(t, r.dir, env, "", "--files")
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	for _, want := range []string{"docs/bad.md:2:", "mail.txt:1:", "tracked path number"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	for _, not := range []string{"fine.md", "model.bin"} {
		if strings.Contains(out, not) {
			t.Errorf("output names %s, which holds no listed word as text:\n%s", not, out)
		}
	}
	if mentionsTerm(out) {
		t.Errorf("the report prints a listed word:\n%s", out)
	}
	clean := guardRepo(t, map[string]string{"fine.md": "zorblaxing is not the word\n"})
	if out, code := runNamesGuard(t, clean.dir, env, "", "--files"); code != 0 || out != "" {
		t.Errorf("clean tree: exit %d, out %q; want 0 and silence", code, out)
	}
}

// TestNamesGuardCommits: --messages names a commit whose message or author
// holds a listed word, --added one whose added lines do; neither prints the
// word, and clean commits pass.
func TestNamesGuardCommits(t *testing.T) {
	t.Parallel()
	env := []string{"NAMES_GUARD_FILE=" + writeNamesList(t, "zorblax", "acme-secret")}
	r := guardRepo(t, map[string]string{"a.txt": "one\n"})
	base := r.git("rev-parse", "HEAD")
	r.write("a.txt", "two\n")
	r.git("commit", "-qam", "fix: the Zorblax repo")
	bad := r.git("rev-parse", "HEAD")
	r.write("a.txt", "three\n")
	r.git("-c", "user.email=dev@acme-secret.example", "commit", "-qam", "fix: plain")
	badAuthor := r.git("rev-parse", "HEAD")
	r.write("a.txt", "four\nhost www.acme-secret.example\n")
	r.git("commit", "-qam", "fix: zorblaxing is not the word")
	badLine := r.git("rev-parse", "HEAD")

	out, code := runNamesGuard(t, r.dir, env, "", "--messages", base+"..HEAD")
	if code != 1 || !strings.Contains(out, bad) || !strings.Contains(out, badAuthor) || strings.Contains(out, badLine) || mentionsTerm(out) {
		t.Errorf("--messages: exit %d, want 1 naming %s and %s only, never the word:\n%s", code, bad, badAuthor, out)
	}
	out, code = runNamesGuard(t, r.dir, env, "", "--added", base+"..HEAD")
	if code != 1 || !strings.Contains(out, badLine) || strings.Contains(out, bad) || mentionsTerm(out) {
		t.Errorf("--added: exit %d, want 1 naming %s only, never the word:\n%s", code, badLine, out)
	}
	if out, code := runNamesGuard(t, r.dir, env, "", "--messages", base); code != 0 {
		t.Errorf("--messages on a clean commit: exit %d:\n%s", code, out)
	}
	if out, code := runNamesGuard(t, r.dir, env, "", "--messages"); code != 2 || !strings.Contains(out, "needs revisions") {
		t.Errorf("--messages without revisions: exit %d, want 2:\n%s", code, out)
	}
}

// TestNamesGuardStdin: --stdin names the lines holding a listed word; the
// allow file drops a term from the list.
func TestNamesGuardStdin(t *testing.T) {
	t.Parallel()
	list := writeNamesList(t, "zorblax", "acme-secret")
	env := []string{"NAMES_GUARD_FILE=" + list}
	in := "one\nthe ZORBLAX repo\nunzorblax\nmail bob@acme-secret.example\n"
	out, code := runNamesGuard(t, t.TempDir(), env, in, "--stdin")
	if code != 1 || !strings.Contains(out, "stdin line 2:") || !strings.Contains(out, "stdin line 4:") || strings.Contains(out, "line 3") || mentionsTerm(out) {
		t.Errorf("--stdin: exit %d, want 1 naming lines 2 and 4, never the word:\n%s", code, out)
	}
	allowed := []string{"NAMES_GUARD_FILE=" + list, "NAMES_GUARD_ALLOW=" + writeNamesList(t, "Zorblax")}
	out, code = runNamesGuard(t, t.TempDir(), allowed, in, "--stdin")
	if code != 1 || strings.Contains(out, "line 2") || !strings.Contains(out, "stdin line 4:") {
		t.Errorf("allowed term: exit %d, want only line 4 (acme-secret is still listed):\n%s", code, out)
	}
	if out, code := runNamesGuard(t, t.TempDir(), allowed, "the zorblax repo\n", "--stdin"); code != 0 {
		t.Errorf("only allowed terms: exit %d, want 0:\n%s", code, out)
	}
}

// TestNamesGuardWithoutList: where no list exists (GitHub Actions, another
// contributor) every mode prints exactly one skip line and passes.
func TestNamesGuardWithoutList(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "absent", "terms.txt")
	env := []string{"NAMES_GUARD_FILE=" + missing}
	r := guardRepo(t, map[string]string{"a.md": "the zorblax repo\n"})
	want := "names-guard: no list at " + missing + ", skipped\n"
	for _, args := range [][]string{{"--files"}, {"--messages", "HEAD"}, {"--added", "HEAD"}, {"--stdin"}} {
		out, code := runNamesGuard(t, r.dir, env, "the zorblax repo\n", args...)
		if code != 0 || out != want {
			t.Errorf("%v without a list: exit %d, out %q; want 0 and %q", args, code, out, want)
		}
	}
}

// TestNamesGuardAllowFileIsGeneric: the committed allow file holds comments
// and plain words only, so it can never carry a list in disguise (a hash, a
// path to another list).
func TestNamesGuardAllowFileIsGeneric(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(filepath.Join(repoRoot, "tests", "lib", "names-guard-allow.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(line) > 40 || strings.ContainsAny(line, "/\\") {
			t.Errorf("names-guard-allow.txt:%d is not a plain generic word", i+1)
		}
	}
}

// TestPrePushRefusesListedWord: the pre-push hook refuses a commit whose
// message, author or added lines hold a listed (made-up) word, naming the
// commit and never the word, before any gate runs; a clean commit, and any
// commit on a machine without a list, goes on to the gate.
func TestPrePushRefusesListedWord(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		commit func(r *hookRepo)
	}{
		{"message", func(r *hookRepo) {
			r.write("a.txt", "two\n")
			r.git("commit", "-qam", "fix: the Zorblax repo")
		}},
		{"author", func(r *hookRepo) {
			r.write("a.txt", "two\n")
			r.git("-c", "user.email=dev@zorblax.example", "commit", "-qam", "fix: plain")
		}},
		{"added line", func(r *hookRepo) {
			r.write("a.txt", "two\nthe zorblax-app repo\n")
			r.git("commit", "-qam", "fix: plain")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newHookRepo(t)
			r.env = []string{"NAMES_GUARD_FILE=" + writeNamesList(t, "zorblax")}
			r.standInTask(standInPass)
			tc.commit(r)
			head := r.git("rev-parse", "HEAD")
			code, out, runs := r.push(head, r.pathWithTask())
			if code == 0 || runs != 0 || !strings.Contains(out, head) || !strings.Contains(out, "private names list") || mentionsTerm(out) {
				t.Fatalf("want refused naming %s and never the word, no gate run: %d runs=%d %s", head, code, runs, out)
			}
		})
	}
	r := newHookRepo(t)
	r.env = []string{"NAMES_GUARD_FILE=" + writeNamesList(t, "zorblax")}
	r.standInTask(standInPass)
	r.write("a.txt", "two\nunzorblax is fine\n")
	r.git("commit", "-qam", "fix: zorblaxing is not the word")
	if code, out, runs := r.push(r.git("rev-parse", "HEAD"), r.pathWithTask()); code != 0 || runs != 1 {
		t.Fatalf("a clean commit must go on to the gate: %d runs=%d %s", code, runs, out)
	}
	r = newHookRepo(t) // env keeps newHookRepo's absent list
	r.standInTask(standInPass)
	r.write("a.txt", "two\nthe zorblax repo\n")
	r.git("commit", "-qam", "fix: the zorblax repo")
	code, out, runs := r.push(r.git("rev-parse", "HEAD"), r.pathWithTask())
	if code != 0 || runs != 1 || strings.Count(out, "names-guard: no list at ") != 1 {
		t.Fatalf("without a list the hook says so once and goes on to the gate: %d runs=%d %s", code, runs, out)
	}
}
