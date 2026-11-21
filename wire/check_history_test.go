package wire

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestHistoryScanNamesRemovedSecrets (devco D-870, owner org-setup item
// 11): `lore check leaks --history` reads every file version and commit
// message reachable from HEAD once. A key committed and removed later,
// which no other check reports, is named with the commit that added it
// and the commit-pinned .gitleaksignore line; a value kept through several
// versions is one finding; a key in a commit message is named; --since
// reads only the commits after it; a pinned line accepts its finding; no
// value is printed. Break-verified: with scanHistory returning
// historyClean before the scan, every refusal case passes.
func TestHistoryScanNamesRemovedSecrets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	removed := "AKIA" + strings.Repeat("Q", 16) // built at run time, never a literal key
	kept := "AKIA" + strings.Repeat("W", 16)
	inMsg := "AKIA" + strings.Repeat("M", 16)
	dir := t.TempDir()
	gitInitIn(t, dir)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.test"}, args...)...)
		cmd.Dir = dir
		cmd.Env = gitenv.Clean(os.Environ())
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(msg string) {
		t.Helper()
		git("add", "-A")
		git("commit", "-q", "--no-verify", "-m", msg)
	}
	var progress bytes.Buffer
	historyProgress = &progress // the tests of this package never print it
	check := func(since string) (string, error) {
		t.Helper()
		revs := []string{"HEAD"}
		if since != "" {
			revs = append(revs, "^"+since)
		}
		sc, err := newScanner(ctx, nil, withRepoGitleaks(dir))
		if err != nil {
			t.Fatal(err)
		}
		allows, err := committedAllows(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		pins, err := historyPins(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		return scanHistory(ctx, dir, revs, sc, allows, pins, historyPlace{})
	}

	write("README.md", "# widgets\n")
	commit("init")
	msg, err := check("")
	if err != nil || !strings.Contains(msg, "✓ lore check leaks --history: no secret in 1 commit(s)") {
		t.Fatalf("a clean history: %q, %v", msg, err)
	}
	write("deploy.env", "AWS_ACCESS_KEY_ID="+removed+"\n")
	write("testdata/fixture.txt", "id="+kept+"\n")
	commit("deploy settings")
	added := gitHead(t, dir)
	for i := range 3 {
		write("testdata/fixture.txt", "id="+kept+"\n"+strings.Repeat("# more\n", i+1))
		commit("fixture")
	}
	git("rm", "-q", "deploy.env")
	commit("drop deploy settings, old key " + inMsg)
	tip := gitHead(t, dir)

	_, err = check("")
	if !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("err = %v, want E_LEAK_BLOCKED for a key removed after it was committed", err)
	}
	out := err.Error() + "\nhint: " + hintOf(err)
	for _, want := range []string{
		"3 secret(s) in the history of HEAD (6 commits",
		shortSHA(added) + " deploy.env:1", "removed since, still in history",
		"add this line to the repo's .gitleaksignore and commit it: " + added + ":deploy.env:aws_access_key:1",
		"testdata/fixture.txt:1", "still in the current files, in 4 versions of the file",
		shortSHA(tip) + " commit message", "rotate each credential first",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the history refusal misses %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "testdata/fixture.txt:1 ") != 1 {
		t.Errorf("one value in four versions is listed more than once:\n%s", out)
	}
	for _, v := range []string{removed, kept, inMsg} {
		if strings.Contains(out, v) || strings.Contains(progress.String(), v) {
			t.Fatalf("the history check printed a value:\n%s\n%s", out, progress.String())
		}
	}
	if !strings.Contains(progress.String(), "reading 6 commits") {
		t.Errorf("no progress line: %q", progress.String())
	}

	// --since: the commits after it only
	if _, err := check(tip); err != nil {
		t.Fatalf("--since the tip read older commits: %v", err)
	}

	// a commit-pinned .gitleaksignore line accepts that finding only
	write(".gitleaksignore", added+":deploy.env:aws_access_key:1\n")
	commit("accept")
	_, err = check("")
	if err == nil || strings.Contains(err.Error(), "deploy.env:1") || !strings.Contains(err.Error(), "testdata/fixture.txt:1") {
		t.Fatalf("the pinned line did not accept deploy.env alone: %v", err)
	}
}

// TestParseHistoryPins reads gitleaks' history form only.
func TestParseHistoryPins(t *testing.T) {
	t.Parallel()
	sha := strings.Repeat("ab", 20)
	got := parseHistoryPins([]byte("# c\n" + sha + ":a:b/c.txt:rule-x:3\npath.txt:rule:1\n" + sha[:7] + ":x.go:r:0\n\n"))
	if len(got) != 1 || got[0] != (historyPin{commit: sha, path: "a:b/c.txt", rule: "rule-x", line: 3}) {
		t.Fatalf("pins = %+v", got)
	}
}

// TestCommitCheckInRepoWithoutLoreNamesGitleaksIgnore (devco D-870, the
// class of the history check's next steps): in a repo lore is not set up
// in, `lore secrets allow` refuses (E_NOT_A_STORE), so the commit check's
// `not a secret?` line is the staged-form .gitleaksignore entry it reads
// from the index, and staging that line lets the commit through.
// Break-verified: with secretsAllowSaves always true the refusal printed
// the `lore secrets allow --file` line that cannot run there.
func TestCommitCheckInRepoWithoutLoreNamesGitleaksIgnore(t *testing.T) {
	t.Parallel()
	ctx := WithNoCreate(context.Background()) // as every CLI check runs
	key := "AKIA" + strings.Repeat("W", 16)   // built at run time, never a literal key
	plain := t.TempDir()
	gitInitIn(t, plain)
	a, err := New(Options{RepoDir: plain, Home: t.TempDir(), FlagActor: testEmail})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	docs := map[string][]byte{"fixture.txt": []byte("id=" + key + "\n")}
	err = a.HookSecrets(ctx, plain, checkWhereCommit, docs)
	if !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("err = %v, want E_LEAK_BLOCKED", err)
	}
	out := err.Error() + "\nhint: " + hintOf(err)
	if !strings.Contains(out, "not a secret? add this line to the repo's .gitleaksignore and stage it: fixture.txt:aws_access_key:1") ||
		strings.Contains(out, "not a secret? lore secrets allow") || strings.Contains(out, ".lore/data/secret_allows") || strings.Contains(out, key) {
		t.Fatalf("the refusal in a repo without lore:\n%s", out)
	}
	if err := os.WriteFile(filepath.Join(plain, ".gitleaksignore"), []byte("fixture.txt:aws_access_key:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	add := exec.Command("git", "add", ".gitleaksignore")
	add.Dir, add.Env = plain, gitenv.Clean(os.Environ())
	if o, gerr := add.CombinedOutput(); gerr != nil {
		t.Fatalf("git add: %v %s", gerr, o)
	}
	if err := a.HookSecrets(ctx, plain, checkWhereCommit, docs); err != nil {
		t.Fatalf("the staged .gitleaksignore line was not honoured: %v", err)
	}
}
