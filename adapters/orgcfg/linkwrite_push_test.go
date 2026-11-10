package orgcfg

// commitStore's push verdicts (F11 race): a push the store refused because
// another push held or moved the branch is a lost race and is rebuilt on
// the new tip; it must never be reported as offline, which kept the commit
// in the local clone only and lost one of two concurrent links.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// testFile is the file each commitStore test puts.
const testFile = "f.txt"

func TestClassifyPush(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want pushOutcome
	}{
		{"empty output", "", pushUnreachable},
		{"only To and Done", "To ../r.git\nDone\n", pushUnreachable},
		{"ok line", "To r\n \tabc:refs/heads/main\t1..2\nDone\n", pushUnreachable},
		{"fetch first", "To r\n!\tabc:refs/heads/main\t[rejected] (fetch first)\nDone\n", pushLostRace},
		{"non-fast-forward", "!\tabc:refs/heads/main\t[rejected] (non-fast-forward)\n", pushLostRace},
		{"stale info", "!\tabc:refs/heads/main\t[rejected] (stale info)\n", pushLostRace},
		{"server lock held (git 2.53)", "!\tHEAD:refs/heads/main\t[remote rejected] (reference already exists)\n", pushLostRace},
		{"server failed to update ref", "!\tabc:refs/heads/main\t[remote rejected] (failed to update ref)\n", pushLostRace},
		{"server incorrect old value", "!\tabc:refs/heads/main\t[remote rejected] (incorrect old value provided)\n", pushLostRace},
		{"server cannot lock ref", "!\tabc:refs/heads/main\t[remote rejected] (cannot lock ref 'refs/heads/main')\n", pushLostRace},
		{"hook declined", "!\tabc:refs/heads/main\t[remote rejected] (pre-receive hook declined)\n", pushRefused},
		{"protected", "!\tabc:refs/heads/main\t[remote rejected] (protected branch hook declined)\n", pushRefused},
		{"bang with a space is not a ref line", "! not a porcelain line\n", pushUnreachable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, _ := classifyPush(c.out); got != c.want {
				t.Fatalf("classifyPush(%q) = %d, want %d", c.out, got, c.want)
			}
		})
	}
}

// pushFixture is a store origin plus a fetched bare clone of it.
func pushFixture(t *testing.T) (origin, clone string) {
	t.Helper()
	origin = storeOrigin(t)
	clone = filepath.Join(t.TempDir(), "clone")
	ctx := context.Background()
	if err := ensureBare(ctx, clone, origin); err != nil {
		t.Fatal(err)
	}
	if err := fetch(ctx, nil, clone); err != nil {
		t.Fatal(err)
	}
	return origin, clone
}

// putCommit is a storeCommit putting testFile; onChange runs before each
// attempt's change (attempt numbers from 0).
func putCommit(clone string, onChange func(attempt int)) storeCommit {
	attempt := 0
	return storeCommit{Dir: clone, Store: testNorm, Branch: "main", Message: "put",
		Signature: gitref.Signature{Name: "t", Email: "t@example.com", When: time.Date(2026, time.November, 6, 0, 0, 0, 0, time.UTC)},
		Change: func(context.Context, *gitref.Repo, string) (map[string][]byte, error) {
			onChange(attempt)
			attempt++
			return map[string][]byte{testFile: []byte("x\n")}, nil
		}}
}

// lockOriginMain holds origin's main ref lock, as a concurrent push does
// while receive-pack updates the ref.
func lockOriginMain(t *testing.T, origin string) string {
	t.Helper()
	lock := filepath.Join(origin, "refs", "heads", "main.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return lock
}

func TestCommitStoreRetriesAServerSideLostRace(t *testing.T) {
	origin, clone := pushFixture(t)
	lock := lockOriginMain(t, origin)
	attempts := 0
	res, err := commitStore(context.Background(), putCommit(clone, func(a int) {
		attempts = a + 1
		if a == 1 {
			_ = os.Remove(lock) // the other push finished
		}
	}))
	if err != nil || !res.Pushed || res.Offline {
		t.Fatalf("a push that lost the ref lock must be rebuilt and pushed: res=%+v err=%v", res, err)
	}
	if attempts != 2 {
		t.Fatalf("want 2 attempts, got %d", attempts)
	}
	if got := testGit(t, origin, "show", "main:"+testFile); got != "x" {
		t.Fatalf("origin main must hold the write, got %q", got)
	}
}

func TestCommitStoreNeverCallsAHeldRefOffline(t *testing.T) {
	origin, clone := pushFixture(t)
	lockOriginMain(t, origin)
	res, err := commitStore(context.Background(), putCommit(clone, func(int) {}))
	if !errors.Is(err, ErrLinkRace) || res.Offline || res.Pushed {
		t.Fatalf("a store that keeps refusing on a held ref is a race error, never offline: res=%+v err=%v", res, err)
	}
}

func TestCommitStoreReportsAHookRefusal(t *testing.T) {
	origin, clone := pushFixture(t)
	// A pre-receive hook that always fails: a link to false(1), which needs
	// no file of ours to be executable.
	falseBin, err := exec.LookPath("false")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(falseBin, filepath.Join(origin, "hooks", "pre-receive")); err != nil {
		t.Fatal(err)
	}
	res, err := commitStore(context.Background(), putCommit(clone, func(int) {}))
	if !errors.Is(err, ErrStoreRefused) || res.Offline || res.Pushed {
		t.Fatalf("a hook refusal is an error, never offline: res=%+v err=%v", res, err)
	}
}

func TestCommitStoreOfflineKeepsTheCommit(t *testing.T) {
	origin, clone := pushFixture(t)
	if err := os.Rename(origin, origin+".gone"); err != nil {
		t.Fatal(err)
	}
	res, err := commitStore(context.Background(), putCommit(clone, func(int) {}))
	if err != nil || !res.Offline || res.Pushed {
		t.Fatalf("an unreachable store keeps the commit locally: res=%+v err=%v", res, err)
	}
	if got := testGit(t, clone, "show", "main:"+testFile); got != "x" {
		t.Fatalf("the clone's main must hold the commit, got %q", got)
	}
}
