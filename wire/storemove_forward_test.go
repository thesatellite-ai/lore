package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestStoreForwardRecordD699 (devco:D-699): an admin's set-url records the
// old address the move commit is owed to; a later set-url keeps it, a move
// back to it drops it, and the new address and local paths are never one.
func TestStoreForwardRecordD699(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	const id, oldURL, newURL = "sto_0123456789abcdef", "git@example.test:acme-co/notes.git", "git@example.test:acme-co/notes-new.git"
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := recordStoreAddress(home, id, newURL, now); err != nil {
		t.Fatal(err)
	}
	fwd := forwardAddresses([]string{oldURL, newURL, "/srv/git/notes.git"}, newURL)
	if len(fwd) != 1 || fwd[0] != oldURL {
		t.Fatalf("[devco:D-699] forward addresses = %q, want only %s", fwd, oldURL)
	}
	if err := recordStoreForward(home, id, newURL, "main", fwd); err != nil {
		t.Fatal(err)
	}
	if e := readStoreAddrRecord(home).Stores[id]; len(e.Forward) != 1 || e.Forward[0] != oldURL || e.Branch != "main" {
		t.Fatalf("[devco:D-699] record = %+v, want the old address owed the move on main", e)
	}
	// a second move keeps what is still owed
	const third = "git@example.test:acme-co/notes-3.git"
	if err := recordStoreAddress(home, id, third, now); err != nil {
		t.Fatal(err)
	}
	if e := readStoreAddrRecord(home).Stores[id]; len(e.Forward) != 1 || e.Forward[0] != oldURL {
		t.Fatalf("[devco:D-699] after a second move the record = %+v, want %s still owed", e, oldURL)
	}
	// a move back to the old address owes it nothing
	if err := recordStoreAddress(home, id, oldURL, now); err != nil {
		t.Fatal(err)
	}
	if e := readStoreAddrRecord(home).Stores[id]; len(e.Forward) != 0 {
		t.Fatalf("[devco:D-699] after moving back the record = %+v, want nothing owed", e)
	}
}

// TestStoreForwardFailureD699 (devco:D-699): a push of the move to an old
// address that did not answer is tried again, and so is one rejected as
// not a fast-forward (what the old address held was merged just before,
// so a teammate pushed there during the sync: never "pushed there after
// it"); one with no repository is said once, with the rename caveat and
// the step each teammate runs, named flags only.
func TestStoreForwardFailureD699(t *testing.T) {
	t.Parallel()
	const old, raw = "git@example.test:acme-co/notes.git", "git@example.test:acme-co/notes-new.git"
	res, line := forwardFailure("ssh: connect to host example.test port 22: Connection timed out\nfatal: Could not read from remote repository.", "acme", old, raw)
	if res != forwardRetry || !strings.Contains(line, "the next lore sync tries again") {
		t.Fatalf("[devco:D-699] no answer: %v %q, want a retry", res, line)
	}
	res, line = forwardFailure(" ! [rejected]        main -> main (fetch first)\nerror: failed to push some refs", "acme", old, raw)
	if res != forwardRetry || !strings.Contains(line, "(a teammate pushed there just now); the next lore sync takes them in and sends the move again") || strings.Contains(line, "after it") {
		t.Fatalf("[devco:D-699] rejected: %v %q, want a retry naming the push during the sync", res, line)
	}
	for _, stderr := range []string{
		"ERROR: Repository not found.\nfatal: Could not read from remote repository.",
	} {
		res, line = forwardFailure(stderr, "acme", old, raw)
		if res != forwardRefused {
			t.Fatalf("[devco:D-699] %q: result %v, want said once", stderr, res)
		}
		for _, want := range []string{"did not reach its old address", "follows only if it redirects to " + raw + " (a renamed repo)", "each teammate runs: lore store set-url --name acme --url " + raw} {
			if !strings.Contains(line, want) {
				t.Fatalf("[devco:D-699] %q: line %q, want %q", stderr, line, want)
			}
		}
	}
	if line := forwardPlanLine("acme", []string{old}, raw); !strings.Contains(line, "lore store push --store acme") || !strings.Contains(line, "lore store set-url --name acme --url "+raw) {
		t.Fatalf("[devco:D-699] plan line %q, want named-flag commands", line)
	}
}

// TestStoreForwardLineCountsItsOwnCommitsD699 (devco:D-699 follow-up): the
// sync or push summary's "sent N" counts the commits that reached the
// store's address; the forward to the old address prints its own line with
// the commits that reached the old one ("forwarded 3 commits to the old
// address …", "forwarded 2 commits …" after a fast-forward), and a forward
// that moved nothing says the old address already holds the move.
func TestStoreForwardLineCountsItsOwnCommitsD699(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	old, clone := filepath.Join(root, "old.git"), filepath.Join(root, "clone")
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Ann", "GIT_AUTHOR_EMAIL=ann@acme.test", "GIT_COMMITTER_NAME=Ann", "GIT_COMMITTER_EMAIL=ann@acme.test")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(root, "init", "--quiet", "--bare", "-b", "main", old)
	git(root, "init", "--quiet", "-b", "main", clone)
	commit := func(n int) string {
		for i := 0; i < n; i++ {
			git(clone, "commit", "--quiet", "--allow-empty", "-m", "c")
		}
		return git(clone, "rev-parse", "HEAD")
	}
	const raw = "git@example.test:acme-co/notes-new.git"
	forward := func(sha string) string {
		t.Helper()
		res, line := forwardOne(ctx, sha, "refs/heads/main", "acme", raw, old, clone)
		if res != forwardTaken {
			t.Fatalf("[devco:D-699] forward of %s: %v %q, want taken", sha, res, line)
		}
		if got := git(old, "rev-parse", "main"); got != sha {
			t.Fatalf("[devco:D-699] old address main = %s, want %s", got, sha)
		}
		return line
	}
	// an empty old repository: the whole history goes there
	if line := forward(commit(3)); !strings.Contains(line, "forwarded 3 commits to the old address "+old) || !strings.Contains(line, "follows the move to "+raw) {
		t.Fatalf("[devco:D-699] first forward line %q, want its own count of 3 commits", line)
	}
	// a fast-forward: only the commits the old address lacked
	if line := forward(commit(2)); !strings.Contains(line, "forwarded 2 commits to the old address "+old) {
		t.Fatalf("[devco:D-699] second forward line %q, want its own count of 2 commits", line)
	}
	// nothing new: no push is claimed
	if line := forward(git(clone, "rev-parse", "HEAD")); !strings.Contains(line, "already holds the move") || strings.Contains(line, "forwarded") {
		t.Fatalf("[devco:D-699] up-to-date forward line %q, want \"already holds the move\" and no count", line)
	}
	// a teammate pushed to the old address meanwhile: git prints the
	// rejection on stdout under --porcelain, and it is still read as one
	// (retried next sync), never as an address that refuses for good
	mate := filepath.Join(root, "mate")
	git(root, "clone", "--quiet", old, mate)
	git(mate, "commit", "--quiet", "--allow-empty", "-m", "teammate")
	git(mate, "push", "--quiet", "origin", "main")
	if res, line := forwardOne(ctx, commit(1), "refs/heads/main", "acme", raw, old, clone); res != forwardRetry || !strings.Contains(line, "a teammate pushed there just now") {
		t.Fatalf("[devco:D-699] forward after a teammate's push to the old address: %v %q, want a retry naming the teammate's push", res, line)
	}
	// the take-in's one wording: a count git can say, never "commit(s)"
	if got := tookInLine(1, "its old address"); got != "took in 1 commit a teammate pushed to its old address before following the move" {
		t.Fatalf("[devco:D-699] take-in line %q", got)
	}
	if got := forwardedLine("acme", plural(2, "commit"), "new", raw, "refs/heads/main", "0123456789abcdef"); !strings.HasPrefix(got, "lore: store acme: forwarded 2 commits to the new address "+raw+" (branch main, now at commit ") {
		t.Fatalf("[devco:D-699] forwarded line %q", got)
	}
}
