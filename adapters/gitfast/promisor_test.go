package gitfast

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// bloblessClone makes an origin with two versions of one file and a
// blobless (--filter=blob:none) clone of it. It returns the clone and the
// id of the old version's blob, which the clone does not hold.
func bloblessClone(t *testing.T) (clone, oldBlob string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	gitT(t, root, "init", "-q", "-b", "main", origin)
	gitT(t, origin, "config", "uploadpack.allowFilter", "true")
	gitT(t, origin, "config", "uploadpack.allowAnySHA1InWant", "true")
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(origin, "row.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		gitT(t, origin, "add", "row.json")
		gitT(t, origin, "commit", "-q", "-m", body)
	}
	write("v1\n")
	oldBlob = gitT(t, origin, "rev-parse", "HEAD:row.json")
	write("v2\n")
	clone = filepath.Join(root, "clone")
	gitT(t, root, "clone", "-q", "--filter=blob:none", "file://"+origin, clone)
	if out, code := realGit(t, clone, []string{"cat-file", "--batch-check", "--batch-all-objects"}, nil); code != 0 || strings.Contains(string(out), oldBlob) {
		t.Fatalf("the blobless clone holds the old blob already (exit %d); the test needs it missing", code)
	}
	return clone, oldBlob
}

// TestPromisorCloneMissingBlobFallsBack: in a partial clone a blob absent
// from the local store is never answered "missing" (git would fetch it from
// the promisor remote): every read of it falls back to git. Break-verified:
// the old reader printed "<oid> missing" for the batch read, which lore's
// readers took as "no such row" (store growth SG-3a, devco:D-997).
func TestPromisorCloneMissingBlobFallsBack(t *testing.T) {
	clone, oldBlob := bloblessClone(t)
	s := New()
	defer s.Close()
	ctx := context.Background()
	for _, tc := range []struct {
		args  []string
		stdin string
	}{
		{[]string{"cat-file", "--batch"}, oldBlob + "\n"},
		{[]string{"cat-file", "--batch-check"}, oldBlob + "\n"},
		{[]string{"cat-file", "--batch"}, "HEAD~1:row.json\n"},
		{[]string{"cat-file", "blob", oldBlob}, ""},
		{[]string{"cat-file", "-e", "HEAD~1:row.json"}, ""},
	} {
		var stdin []byte
		if tc.stdin != "" {
			stdin = []byte(tc.stdin)
		}
		if rep, ok := s.Answer(ctx, clone, tc.args, nil, stdin); ok {
			t.Errorf("git %v <<%q in a blobless clone was answered in-process (%q); it must fall back to git, which fetches the blob", tc.args, tc.stdin, rep.Out)
		}
	}
	// the tip's blob is local: still answered in-process
	rep, ok := s.Answer(ctx, clone, []string{"cat-file", "blob", "HEAD:row.json"}, nil, nil)
	if !ok || string(rep.Out) != "v2\n" {
		t.Errorf("the tip blob in a blobless clone: answered %v %q, want in-process v2", ok, rep.Out)
	}
	// and git itself fetches the missing one on demand
	if got := gitT(t, clone, "cat-file", "blob", oldBlob); got != "v1" {
		t.Errorf("git cat-file of the old blob = %q, want v1 (lazy fetch)", got)
	}
}

// TestFullCloneMissingObjectStaysMissing: outside a partial clone an absent
// object is still answered "missing" in-process, as git answers it.
func TestFullCloneMissingObjectStaysMissing(t *testing.T) {
	work, _, _ := fixture(t)
	s := New()
	defer s.Close()
	none := strings.Repeat("ab", 20)
	rep, ok := s.Answer(context.Background(), work, []string{"cat-file", "--batch-check"}, nil, []byte(none+"\n"))
	if !ok || string(rep.Out) != none+" missing\n" {
		t.Errorf("a missing object in a full clone: answered %v %q, want %q", ok, rep.Out, none+" missing\n")
	}
}

// TestPartialCloneConfig: the config shapes that make a repository a
// partial clone, and the ones that leave it to git.
func TestPartialCloneConfig(t *testing.T) {
	for _, tc := range []struct {
		body         string
		promisor, ok bool
	}{
		{"[core]\n\tbare = false\n", false, true},
		{"[extensions]\n\tpartialClone = origin\n", true, true},
		{"[remote \"origin\"]\n\tpromisor = true\n", true, true},
		{"[remote \"origin\"]\n\tpromisor\n", true, true},
		{"[remote \"origin\"]\n\tpromisor = false\n", false, true},
		{"[remote \"origin\"]\n\tpromisor = 2\n", false, false},
	} {
		p := filepath.Join(t.TempDir(), "config")
		if err := os.WriteFile(p, []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		promisor, ok := partialClone(p)
		if promisor != tc.promisor || ok != tc.ok {
			t.Errorf("partialClone(%q) = %v,%v want %v,%v", tc.body, promisor, ok, tc.promisor, tc.ok)
		}
	}
}
