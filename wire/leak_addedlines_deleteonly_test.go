package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommitLeakDeleteOnlyAddsNothing (issue #156, devco:D-946): a staged
// file whose diff only removes lines, a mode-only change and a pure rename
// add no text, so the commit leak check reads none of their lines (it used
// to read them whole and name an unchanged line). A file with one added
// line is still checked on that line, and a file new to the repo whole.
// Break-verified: with parseAddedFiles dropping files that add no line
// (the old rule) every unchanged line of env.example is read again.
func TestCommitLeakDeleteOnlyAddsNothing(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	ctx := context.Background()
	repo := t.TempDir()
	alGit(t, repo, "init", "-q", "-b", "main")
	write := func(rel, body string, mode os.FileMode) {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
	const kept = "SERVICE_TOKEN_NAME=\nSERVICE_TOKEN_SECRET_NAME=\n"
	write("env.example", "# copy to .env\nPORT=\nLEVEL=\n"+kept+"TTL=\n", 0o644)
	write("run.sh", "echo internal words here\n", 0o644)
	write("old-name.md", "an internal passage moved as is\n", 0o644)
	write("main.go", "package main\n", 0o644)
	alGit(t, repo, "add", "-A")
	alGit(t, repo, "commit", "-q", "--no-verify", "-m", "base")

	write("env.example", "# copy to .env\n"+kept, 0o644) // only deletes
	write("run.sh", "echo internal words here\n", 0o755) // mode only
	alGit(t, repo, "mv", "old-name.md", "new-name.md")   // pure rename
	write("main.go", "package main\n// helper\n", 0o644) // one added line
	write("fresh.md", "first\nsecond\n", 0o644)          // new file
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
	for _, p := range []string{"env.example", "run.sh", "new-name.md"} {
		if _, ok := docs[p]; !ok {
			t.Fatalf("%s is not among the staged files: %v", p, docs)
		}
		if strings.TrimSpace(string(got[p])) != "" {
			t.Fatalf("%s adds nothing but is checked as %q (unchanged lines are never findings)", p, got[p])
		}
	}
	if want := "\n// helper\n"; string(got["main.go"]) != want {
		t.Fatalf("main.go as checked = %q, want only its added line %q", got["main.go"], want)
	}
	if string(got["fresh.md"]) != "first\nsecond\n" {
		t.Fatalf("a file new to the repo is not checked whole: %q", got["fresh.md"])
	}
}

// TestParseAddedFilesNoAddedLines: a file whose hunks only remove lines,
// a mode-only change and a 100% rename are described as adding nothing; a
// binary file, a quoted path and a "+++" with no hunk are not described
// (the caller reads them whole). Break-verified with the old filter.
func TestParseAddedFilesNoAddedLines(t *testing.T) {
	patch := strings.Join([]string{
		"diff --git a/del.env b/del.env",
		"index 1111111..2222222 100644",
		"--- a/del.env", "+++ b/del.env",
		"@@ -2,2 +1,0 @@ head", "-A=", "-B=",
		"@@ -7 +5,0 @@", "-C=",
		"diff --git a/run.sh b/run.sh",
		"old mode 100644", "new mode 100755",
		"diff --git a/old.md b/new.md",
		"similarity index 100%", "rename from old.md", "rename to new.md",
		"diff --git a/b.bin b/b.bin",
		"old mode 100644", "new mode 100755",
		"index 3333333..4444444",
		"Binary files a/b.bin and b/b.bin differ",
		`diff --git "a/q\tx" "b/q\tx"`,
		"old mode 100644", "new mode 100755",
		"diff --git a/nohunk b/nohunk",
		"--- a/nohunk", "+++ b/nohunk",
		"diff --git a/add.go b/add.go",
		"index 5555555..6666666 100644",
		"--- a/add.go", "+++ b/add.go",
		"@@ -3,0 +4 @@", "+x",
	}, "\n")
	got := parseAddedLines(patch)
	for _, p := range []string{"del.env", "run.sh", "new.md"} {
		lines, ok := got[p]
		if !ok || len(lines) != 0 {
			t.Fatalf("%s = %v (present %v), want described with no lines: %v", p, lines, ok, got)
		}
	}
	for _, p := range []string{"b.bin", "q\tx", "nohunk", "old.md"} {
		if _, ok := got[p]; ok {
			t.Fatalf("%s must stay undescribed (read whole): %v", p, got)
		}
	}
	if len(got["add.go"]) != 1 || !got["add.go"][4] {
		t.Fatalf("add.go lines = %v, want [4]", got["add.go"])
	}
}

// TestPushAddedLinesDeleteOnly (issue #156): a pushed commit whose diff
// only removes lines of a file names its new blob with no lines (known,
// adds nothing), so the pre-push check reads none of it; a mode-only
// change names no blob at all.
func TestPushAddedLinesDeleteOnly(t *testing.T) {
	const commit, oldBlob, newBlob = "c1", "b0", "b1"
	patch := strings.Join([]string{
		commit,
		"diff --git a/.env.example b/.env.example",
		"index " + oldBlob + ".." + newBlob + " 100644",
		"--- a/.env.example", "+++ b/.env.example",
		"@@ -2,2 +1,0 @@", "-A=", "-B=",
		"diff --git a/run.sh b/run.sh",
		"old mode 100644", "new mode 100755",
	}, "\n")
	got := PushAddedLines(patch, map[string]int{commit: 1})
	lines, ok := got[newBlob]
	if !ok || len(lines) != 0 {
		t.Fatalf("delete-only blob = %v (present %v), want known with no lines: %v", lines, ok, got)
	}
	if len(got) != 1 {
		t.Fatalf("blobs = %v, want only %s", got, newBlob)
	}
	ctx := WithAddedLines(context.Background(), map[string]map[int]bool{".env.example": lines})
	docs := AddedLinesOnly(ctx, map[string][]byte{".env.example": []byte("# head\nAWS_KEY_NAME=\n")})
	if strings.TrimSpace(string(docs[".env.example"])) != "" {
		t.Fatalf("delete-only file still read: %q", docs[".env.example"])
	}
}
