package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
)

// maintClone is a store clone whose history holds over 600 loose, reachable
// objects (one commit of 600 files), as a busy store's clone collects.
func maintClone(t *testing.T) string {
	t.Helper()
	b := clone(t, newRemote(t, true))
	bulk := filepath.Join(b.work, "bulk")
	if err := os.MkdirAll(bulk, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := range 600 {
		if err := os.WriteFile(filepath.Join(bulk, fmt.Sprintf("r%03d.json", i)), []byte(fmt.Sprintf("{\"n\": %d}\n", i)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, b.work, "add", "bulk")
	gitT(t, b.work, "commit", "--quiet", "-m", "bulk")
	return b.work
}

func refsOf(t *testing.T, dir string) []string {
	t.Helper()
	return strings.Split(gitT(t, dir, "for-each-ref", "--format=%(objectname) %(refname)"), "\n")
}

// TestNewCloneTurnsGitAutoGCOffAndWaitsADay: a clone lore makes has
// gc.auto=0 and maintenance.auto=false in its config (git's detached auto
// gc never starts inside a lore command) and a maintenance stamp, so its
// first pass is a day away (devco:D-996).
func TestNewCloneTurnsGitAutoGCOffAndWaitsADay(t *testing.T) {
	t.Parallel()
	b := clone(t, newRemote(t, true))
	for _, kv := range gitAutoOff {
		if got := gitT(t, b.work, "config", "--local", "--get", kv[0]); got != kv[1] {
			t.Errorf("%s in a new clone's config = %q, want %q", kv[0], got, kv[1])
		}
	}
	last, ok := LastMaintenance(b.work)
	if !ok || time.Since(last) > time.Minute {
		t.Fatalf("a new clone's maintenance stamp = %v (%v), want just now", last, ok)
	}
	if MaintenanceDue(b.work, DefaultMaintEvery, time.Now()) {
		t.Fatal("a clone made just now is due for a pass")
	}
}

// TestRunnerPassesGitAutoGCOff: every call of lore's store runner carries
// gc.auto=0 and maintenance.auto=false, so a clone made before the clone
// config held them never starts git's auto gc either.
func TestRunnerPassesGitAutoGCOff(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gitT(t, dir, "init", "--quiet", "--initial-branch="+testBranch)
	r := runner{bin: mustGit(t), owned: true, noHooks: filepath.Join(dir, ".git", "lore", noHooksName)}
	for _, kv := range gitAutoOff {
		out, err := r.run(context.Background(), call{dir: dir, args: []string{"config", "--get", kv[0]}})
		if err != nil || trimmed(out) != kv[1] {
			t.Errorf("git config --get %s through the store runner = %q (%v), want %q", kv[0], trimmed(out), err, kv[1])
		}
	}
}

// TestMaintainClonePacksAndKeepsEverything: the pass runs at most once per
// interval; when due it packs the loose objects, keeps every ref and every
// reachable object, writes the commit-graph with changed-path filters and
// moves the stamp.
func TestMaintainClonePacksAndKeepsEverything(t *testing.T) {
	t.Parallel()
	dir := maintClone(t)
	ctx := context.Background()
	before, err := SizeOf(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !before.NeedsRepack() {
		t.Fatalf("fixture: %d loose objects, %d packs: want a clone that needs a repack", before.Loose, before.Packs)
	}
	refs := refsOf(t, dir)

	res, err := MaintainClone(ctx, dir, MaintOptions{})
	if err != nil || res.Skipped != MaintSkipNotDue {
		t.Fatalf("a pass within a day of the last one: %+v, %v; want skipped %s", res, err, MaintSkipNotDue)
	}

	later := time.Now().Add(DefaultMaintEvery + time.Hour)
	if !MaintenanceDue(dir, DefaultMaintEvery, later) {
		t.Fatal("a clone whose last pass is 25 h old is not due")
	}
	res, err = MaintainClone(ctx, dir, MaintOptions{Now: later})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != "" || !res.Repacked || !res.CommitGraph || !res.Refs {
		t.Fatalf("pass = %+v, want it to repack, write the commit-graph and tidy the refs", res)
	}
	if res.After.Loose > 0 || res.After.Packs > 2 {
		t.Fatalf("after the pass: %d loose objects, %d packs; want 0 loose and at most 2 packs", res.After.Loose, res.After.Packs)
	}
	if got := refsOf(t, dir); !slices.Equal(got, refs) {
		t.Fatalf("refs changed:\nbefore %v\nafter  %v", refs, got)
	}
	gitT(t, dir, "fsck", "--connectivity-only", "--no-progress")
	if _, err := os.Stat(filepath.Join(dir, ".git", "objects", "info", "commit-graphs", "commit-graph-chain")); err != nil {
		t.Fatalf("no split commit-graph after the pass: %v", err)
	}
	if last, ok := LastMaintenance(dir); !ok || !last.Equal(later.UTC().Round(0)) && last.Sub(later).Abs() > time.Millisecond {
		t.Fatalf("stamp = %v (%v), want %v", last, ok, later)
	}
	if MaintenanceDue(dir, DefaultMaintEvery, later.Add(time.Hour)) {
		t.Fatal("due again an hour after a pass")
	}
}

// TestMaintainCloneSkipsWhenAnotherPassRuns: a second pass on a clone
// whose pass is running returns at once as skipped and leaves the stamp,
// so the next command tries again (never two passes, never a wait).
func TestMaintainCloneSkipsWhenAnotherPassRuns(t *testing.T) {
	t.Parallel()
	dir := maintClone(t)
	release, err := rowfs.LockFile(context.Background(), filepath.Join(StateDirOf(dir), MaintLockName), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	stamp, _ := LastMaintenance(dir)
	start := time.Now()
	res, err := MaintainClone(context.Background(), dir, MaintOptions{Force: true})
	if took := time.Since(start); took > time.Second {
		t.Fatalf("a pass waited %v for the running one; want it to skip at once", took)
	}
	if err != nil || res.Skipped != MaintSkipBusy {
		t.Fatalf("pass beside a running one = %+v, %v; want skipped %s", res, err, MaintSkipBusy)
	}
	if now, _ := LastMaintenance(dir); !now.Equal(stamp) {
		t.Fatalf("a skipped pass moved the stamp from %v to %v", stamp, now)
	}
	if s, _ := SizeOf(dir); !s.NeedsRepack() {
		t.Fatal("a skipped pass repacked the clone")
	}
}

// TestMaintainCloneNeverWaitsForTheStoreLock: while a command holds the
// store lock the pass still packs the objects, skips only the ref and
// config steps, and never waits the lock timeout (10 s) for the lock.
func TestMaintainCloneNeverWaitsForTheStoreLock(t *testing.T) {
	t.Parallel()
	dir := maintClone(t)
	_, release, err := LockClone(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	start := time.Now()
	res, err := MaintainClone(context.Background(), dir, MaintOptions{Force: true, Gentle: true})
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the pass took %v beside a held store lock; it must not wait for it", took)
	}
	if res.Refs || !res.Repacked || res.After.Loose > 0 {
		t.Fatalf("pass beside a held store lock = %+v; want the objects packed and the ref steps skipped", res)
	}
}

// TestMaintenanceDueRules: due with no stamp, not within the interval, due
// past it; a pass that started within the last hour and never finished
// holds the next one back for that hour only.
func TestMaintenanceDueRules(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(StateDirOf(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	stamp := filepath.Join(StateDirOf(dir), MaintStampFile)
	try := filepath.Join(StateDirOf(dir), MaintTryFile)
	if !MaintenanceDue(dir, DefaultMaintEvery, now) {
		t.Error("a clone with no stamp is not due")
	}
	writeStamp(stamp, now.Add(-time.Hour))
	if MaintenanceDue(dir, DefaultMaintEvery, now) {
		t.Error("due an hour after a pass, with a 24 h interval")
	}
	writeStamp(stamp, now.Add(-25*time.Hour))
	if !MaintenanceDue(dir, DefaultMaintEvery, now) {
		t.Error("not due 25 h after a pass")
	}
	writeStamp(try, now.Add(-10*time.Minute))
	if MaintenanceDue(dir, DefaultMaintEvery, now) {
		t.Error("due 10 min after a pass started that never finished")
	}
	writeStamp(try, now.Add(-2*time.Hour))
	if !MaintenanceDue(dir, DefaultMaintEvery, now) {
		t.Error("a pass that failed 2 h ago holds the next one back")
	}
}
