package wire

import (
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
// address that did not answer is tried again; one refused (a teammate
// pushed there after the move) or with no repository is said once, with
// the rename caveat and the step each teammate runs, named flags only.
func TestStoreForwardFailureD699(t *testing.T) {
	t.Parallel()
	const old, raw = "git@example.test:acme-co/notes.git", "git@example.test:acme-co/notes-new.git"
	res, line := forwardFailure("ssh: connect to host example.test port 22: Connection timed out\nfatal: Could not read from remote repository.", "acme", old, raw)
	if res != forwardRetry || !strings.Contains(line, "the next lore sync tries again") {
		t.Fatalf("[devco:D-699] no answer: %v %q, want a retry", res, line)
	}
	for _, stderr := range []string{
		" ! [rejected]        main -> main (fetch first)\nerror: failed to push some refs",
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
