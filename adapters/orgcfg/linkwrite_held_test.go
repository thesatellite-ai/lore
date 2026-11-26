package orgcfg

// devco:D-1010: a push that lost a race used to rebuild its commit on the
// remote's tip and move the branch there, so every commit the clone held
// for lore sync (a manual-mode store setting, rows) was dropped. Now the
// commit stays on top of that waiting work, Held, for lore sync to merge.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// fileCommit is a storeCommit putting one file with body.
func fileCommit(clone, file, body string, noPush bool) storeCommit {
	return storeCommit{Dir: clone, Store: testNorm, Branch: "main", Message: "put " + file, NoPush: noPush,
		Signature: gitref.Signature{Name: "t", Email: "t@example.com", When: time.Date(2026, time.November, 6, 0, 0, 0, 0, time.UTC)},
		Change: func(context.Context, *gitref.Repo, string) (map[string][]byte, error) {
			return map[string][]byte{file: []byte(body + "\n")}, nil
		}}
}

func TestCommitStoreLostRaceKeepsWaitingCommits(t *testing.T) {
	ctx := context.Background()
	origin, clone := pushFixture(t)
	// the clone's own commit, waiting for lore sync (not pushed)
	if _, err := commitStore(ctx, fileCommit(clone, "waiting.txt", "w", true)); err != nil {
		t.Fatal(err)
	}
	// another machine pushes first: the remote moves
	other := filepath.Join(t.TempDir(), "other")
	if err := ensureClone(ctx, other, origin, false); err != nil {
		t.Fatal(err)
	}
	if err := fetch(ctx, nil, other); err != nil {
		t.Fatal(err)
	}
	if res, err := commitStore(ctx, fileCommit(other, "theirs.txt", "o", false)); err != nil || !res.Pushed {
		t.Fatalf("the other push: res=%+v err=%v", res, err)
	}
	res, err := commitStore(ctx, fileCommit(clone, testFile, "x", false))
	if err != nil || res.Pushed || res.Offline || !res.Held {
		t.Fatalf("a lost race over waiting commits is Held, not pushed: res=%+v err=%v", res, err)
	}
	for _, f := range []string{"waiting.txt", testFile} {
		if got := testGit(t, clone, "show", "main:"+f); got == "" {
			t.Fatalf("the clone's main lost %s", f)
		}
	}
	if got := testGit(t, origin, "show", "main:theirs.txt"); got != "o" {
		t.Fatalf("origin keeps the other push, got %q", got)
	}
}

// A clone merely behind the remote holds no work of its own: a lost race
// still rebuilds on the remote's tip and pushes.
func TestCommitStoreLostRaceBehindStillPushes(t *testing.T) {
	ctx := context.Background()
	origin, clone := pushFixture(t)
	other := filepath.Join(t.TempDir(), "other")
	if err := ensureClone(ctx, other, origin, false); err != nil {
		t.Fatal(err)
	}
	if err := fetch(ctx, nil, other); err != nil {
		t.Fatal(err)
	}
	if res, err := commitStore(ctx, fileCommit(other, "theirs.txt", "o", false)); err != nil || !res.Pushed {
		t.Fatalf("the other push: res=%+v err=%v", res, err)
	}
	res, err := commitStore(ctx, fileCommit(clone, testFile, "x", false))
	if err != nil || !res.Pushed || res.Held {
		t.Fatalf("a clone with nothing waiting rebuilds and pushes: res=%+v err=%v", res, err)
	}
	for _, f := range []string{"theirs.txt", testFile} {
		if got := testGit(t, origin, "show", "main:"+f); got == "" {
			t.Fatalf("origin main lacks %s", f)
		}
	}
}
