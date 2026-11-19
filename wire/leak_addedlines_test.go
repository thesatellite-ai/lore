package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// alGit runs git in dir with no global or system config.
func alGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Test Person", "-c", "user.email=test@example.test", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestCommitLeakDocsOnlyAddedLines (devco:D-720): the commit leak check
// reads only the lines the commit adds to an already-committed file; the
// old text is blanked with its line numbers kept, so a finding names the
// added line. lore's own files and a file new to the repo stay whole.
// Break-verified: with addedLinesOnly returning docs unchanged the old
// line is still read and the test fails.
func TestCommitLeakDocsOnlyAddedLines(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	ctx := context.Background()
	repo := t.TempDir()
	alGit(t, repo, "init", "-q", "-b", "main")
	write := func(rel, body string) {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const oldText = "old hand-written internal sentence"
	write("CLAUDE.md", "# Title\n"+oldText+"\nkeep\n")
	write(".lore/data/_meta.json", "{\"a\": 1}\n")
	alGit(t, repo, "add", "-A")
	alGit(t, repo, "commit", "-q", "--no-verify", "-m", "base")

	write("CLAUDE.md", "# Title\n"+oldText+"\nkeep changed\nnew line\n")
	write(".lore/data/_meta.json", "{\"a\": 2}\n")
	write("fresh.md", "first\nsecond\n")
	alGit(t, repo, "add", "-A")

	docs := map[string][]byte{}
	if err := stagedFiles(ctx, repo, docs); err != nil {
		t.Fatal(err)
	}
	added, err := stagedAddedLines(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	got := addedLinesOnly(docs, added)

	if want := "\n\nkeep changed\nnew line\n"; string(got["CLAUDE.md"]) != want {
		t.Fatalf("CLAUDE.md as checked = %q, want only the changed lines at their line numbers %q", got["CLAUDE.md"], want)
	}
	if strings.Contains(string(got["CLAUDE.md"]), oldText) {
		t.Fatalf("the old text of a committed file is still checked: %q", got["CLAUDE.md"])
	}
	if string(got["fresh.md"]) != "first\nsecond\n" {
		t.Fatalf("a file new to the repo is not checked whole: %q", got["fresh.md"])
	}
	if string(got[".lore/data/_meta.json"]) != string(docs[".lore/data/_meta.json"]) {
		t.Fatalf("lore's own file was narrowed: %q", got[".lore/data/_meta.json"])
	}
	// a path the diff did not describe (binary, quoted name) stays whole
	odd := map[string][]byte{"blob.bin": []byte("x\ny\n"), checkWhereCommitMsg: []byte("msg " + oldText)}
	if out := addedLinesOnly(odd, map[string]map[int]bool{}); string(out["blob.bin"]) != "x\ny\n" || !strings.Contains(string(out[checkWhereCommitMsg]), oldText) {
		t.Fatalf("an undescribed path or the commit message was narrowed: %q", out)
	}
}

// TestParseAddedLines reads -U0 hunk headers: a count of 0 adds no line,
// a missing count is one line, a binary file has no entry.
func TestParseAddedLines(t *testing.T) {
	patch := strings.Join([]string{
		"diff --git a/a.md b/a.md",
		"--- a/a.md",
		"+++ b/a.md",
		"@@ -2,0 +3,2 @@ head",
		"+x", "+y",
		"@@ -7 +9 @@",
		"-old", "+new",
		"@@ -12,2 +13,0 @@",
		"-gone", "-gone",
		"diff --git a/b.bin b/b.bin",
		"Binary files a/b.bin and b/b.bin differ",
	}, "\n")
	got := parseAddedLines(patch)
	want := map[int]bool{3: true, 4: true, 9: true}
	if len(got["a.md"]) != len(want) {
		t.Fatalf("a.md lines = %v, want %v", got["a.md"], want)
	}
	for n := range want {
		if !got["a.md"][n] {
			t.Fatalf("a.md lines = %v, want %v", got["a.md"], want)
		}
	}
	if _, ok := got["b.bin"]; ok {
		t.Fatalf("a binary file got lines: %v", got["b.bin"])
	}
}
