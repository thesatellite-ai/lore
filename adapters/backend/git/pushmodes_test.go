package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// openPushAt opens the clone at base (a second handle on the same clone, as a
// later lore process would) with opts.
func openPushAt(t *testing.T, remote, base string, opts ...Option) *Backend {
	t.Helper()
	b, err := OpenClone(context.Background(), remote, base, rowfs.FolderRoot, store.OpenOptions{}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

// fixedClock is a clock option at t.
func fixedClock(at time.Time) Option { return WithClock(func() time.Time { return at }) }

func remoteCount(t *testing.T, remote string) int {
	t.Helper()
	n, err := strconv.Atoi(gitT(t, remote, "rev-list", "--count", testBranch))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestSquashMakesOneBatchCommit: three writes are three local commits;
// a squashing sync puts ONE commit on the remote with the same tree and a
// subject that counts the rows per kind.
func TestSquashMakesOneBatchCommit(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	before := remoteCount(t, remote)
	for i := 0; i < 3; i++ {
		write(t, a, store.Change{Row: g.row(newID(t), "row", hlc.HLC{})})
	}
	if n, err := a.Unpushed(context.Background()); err != nil || n != 3 {
		t.Fatalf("Unpushed = %d, %v; want 3", n, err)
	}
	rep, err := a.SyncWith(context.Background(), SyncOptions{Squash: true})
	if err != nil || rep.Offline || rep.Sent != 3 {
		t.Fatalf("SyncWith = %+v, %v", rep, err)
	}
	if got := remoteCount(t, remote); got != before+1 {
		t.Fatalf("remote has %d commits, want %d (one batch)", got, before+1)
	}
	subj := gitT(t, remote, "log", "-1", "--format=%s", testBranch)
	if subj != "lore: batch 3 rows (memories 3)" {
		t.Fatalf("batch subject = %q", subj)
	}
	if gitT(t, remote, "rev-parse", testBranch+"^{tree}") != gitT(t, a.Dir(), "rev-parse", "HEAD^{tree}") {
		t.Fatal("the batch commit does not carry the clone's tree")
	}
	if n, _ := a.Unpushed(context.Background()); n != 0 {
		t.Fatalf("Unpushed after the push = %d", n)
	}
}

// TestUnpulledCountsFetchedCommits: commits a plain fetch brought into the
// tracking ref but not into the branch are counted until a sync merges
// them; a clone level with its remote, or holding only its own commits,
// has none.
func TestUnpulledCountsFetchedCommits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	b := clone(t, remote, WithMerger(newestMerger{}))
	ga, gb := newGen(t), newGen(t)
	if n, err := b.Unpulled(ctx); err != nil || n != 0 {
		t.Fatalf("fresh clone: Unpulled = %d, %v; want 0", n, err)
	}
	write(t, b, store.Change{Row: gb.row(newID(t), "own", hlc.HLC{})})
	if n, err := b.Unpulled(ctx); err != nil || n != 0 {
		t.Fatalf("own unpushed commit: Unpulled = %d, %v; want 0", n, err)
	}
	pushed := ga.row(newID(t), "from a", hlc.HLC{})
	write(t, a, store.Change{Row: pushed})
	write(t, a, store.Change{Row: ga.row(newID(t), "from a again", hlc.HLC{})})
	syncOK(t, a)
	gitT(t, b.Dir(), "fetch", "--quiet", RemoteName)
	if n, err := b.Unpulled(ctx); err != nil || n != 2 {
		t.Fatalf("after a tracking-only fetch: Unpulled = %d, %v; want 2", n, err)
	}
	if _, err := b.SyncWith(ctx, SyncOptions{NoPush: true}); err != nil {
		t.Fatal(err)
	}
	if n, err := b.Unpulled(ctx); err != nil || n != 0 {
		t.Fatalf("after the merge: Unpulled = %d, %v; want 0", n, err)
	}
	get(t, b, pushed.ID)
}

// TestNoPushKeepsTheOutbox: a fetch-only sync merges the remote but sends
// nothing; the commits stay local for a later push.
func TestNoPushKeepsTheOutbox(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	b := clone(t, remote, WithMerger(newestMerger{}))
	ga, gb := newGen(t), newGen(t)
	write(t, b, store.Change{Row: gb.row(newID(t), "from b", hlc.HLC{})})
	syncOK(t, b)
	local := ga.row(newID(t), "waits", hlc.HLC{})
	write(t, a, store.Change{Row: local})
	before := gitT(t, remote, "rev-parse", testBranch)
	rep, err := a.SyncWith(context.Background(), SyncOptions{NoPush: true})
	if err != nil || rep.Received != 1 || rep.Sent != 0 {
		t.Fatalf("SyncWith NoPush = %+v, %v", rep, err)
	}
	if gitT(t, remote, "rev-parse", testBranch) != before {
		t.Fatal("a fetch-only sync pushed")
	}
	if n, _ := a.Unpushed(context.Background()); n == 0 {
		t.Fatal("the local write left the outbox")
	}
	get(t, a, local.ID)
}

func TestBatchMessage(t *testing.T) {
	t.Parallel()
	msg := BatchMessage(4, map[string]int{"tasks": 1, "memories": 3}, []string{"memories mem_a", "memories mem_b"})
	if !strings.HasPrefix(msg, "lore: batch 4 rows (memories 3, tasks 1)\n\nmemories mem_a\nmemories mem_b\n(+2 more)") {
		t.Fatalf("message = %q", msg)
	}
	if got := BatchMessage(1, map[string]int{"tasks": 1}, nil); got != "lore: batch 1 row (tasks 1)" {
		t.Fatalf("one-row message = %q", got)
	}
}

// TestCompactThenMemberReplays: history older than the cutoff becomes one
// parentless snapshot, later commits are kept, the epoch goes to 1 and
// every row stays; a second clone with an unpushed row re-fetches the new
// history and replays its row on top instead of merging.
func TestCompactThenMemberReplays(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	old := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	base := filepath.Join(t.TempDir(), "clones", "host", "store")
	aOld := openPushAt(t, remote, base, WithMerger(newestMerger{}), fixedClock(old))
	g := newGen(t)
	var rows []store.Row
	for i := 0; i < 3; i++ {
		r := g.row(newID(t), "old", hlc.HLC{})
		rows = append(rows, r)
		write(t, aOld, store.Change{Row: r})
	}
	syncOK(t, aOld)
	a := openPushAt(t, remote, base, WithMerger(newestMerger{}), fixedClock(now))
	fresh := g.row(newID(t), "new", hlc.HLC{})
	rows = append(rows, fresh)
	write(t, a, store.Change{Row: fresh})
	syncOK(t, a)

	b := clone(t, remote, WithMerger(newestMerger{}), fixedClock(now))
	syncOK(t, b)
	gb := newGen(t)
	mine := gb.row(newID(t), "b waits", hlc.HLC{})
	write(t, b, store.Change{Row: mine})

	plan, err := a.Compact(context.Background(), CompactRequest{Cutoff: now.Add(-30 * 24 * time.Hour), DryRun: true})
	if err != nil || plan.Nothing || plan.Kept != 1 || plan.FromEpoch != 0 || plan.ToEpoch != 1 {
		t.Fatalf("dry run = %+v, %v", plan, err)
	}
	tipBefore := gitT(t, remote, "rev-parse", testBranch)
	treeBefore := gitT(t, remote, "rev-parse", testBranch+"^{tree}")
	res, err := a.Compact(context.Background(), CompactRequest{Cutoff: now.Add(-30 * 24 * time.Hour)})
	if err != nil || res.Nothing || res.ToEpoch != 1 {
		t.Fatalf("Compact = %+v, %v", res, err)
	}
	if got := remoteCount(t, remote); got != 3 { // snapshot, the kept write, the epoch commit
		t.Fatalf("remote has %d commits after compaction, want 3", got)
	}
	if gitT(t, a.Dir(), "rev-parse", res.Backup) != tipBefore {
		t.Fatal("the backup ref does not hold the old tip")
	}
	diff := gitT(t, remote, "diff", "--name-only", treeBefore, testBranch+"^{tree}")
	if diff != rowfs.MarkerFile {
		t.Fatalf("the compaction changed %q, want only the marker (epoch)", diff)
	}
	for _, r := range rows {
		get(t, a, r.ID)
	}

	rep, err := b.Sync(context.Background())
	if err != nil || rep.Offline || len(rep.Refused) > 0 {
		t.Fatalf("member sync after compaction = %+v, %v", rep, err)
	}
	if b.Epoch(context.Background()) != 1 {
		t.Fatalf("member epoch = %d, want 1", b.Epoch(context.Background()))
	}
	if !strings.HasPrefix(gitT(t, remote, "log", "-1", "--format=%s", testBranch), replaySubject) {
		t.Fatal("the member's row did not go out as a replay commit")
	}
	for _, r := range append(rows, mine) {
		get(t, b, r.ID)
	}
	syncOK(t, a)
	get(t, a, mine.ID)
	if again, err := a.Compact(context.Background(), CompactRequest{Cutoff: now.Add(-30 * 24 * time.Hour)}); err != nil || !again.Nothing {
		t.Fatalf("second compaction = %+v, %v; want nothing to do", again, err)
	}
}

// TestCompactOfflineChangesNothing: no remote, no rewrite.
func TestCompactOfflineChangesNothing(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	write(t, a, store.Change{Row: newGen(t).row(newID(t), "x", hlc.HLC{})})
	syncOK(t, a)
	head := gitT(t, a.Dir(), "rev-parse", "HEAD")
	if err := os.Rename(remote, remote+".away"); err != nil {
		t.Fatal(err)
	}
	_, err := a.Compact(context.Background(), CompactRequest{Cutoff: time.Now().Add(time.Hour)})
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.Offline {
		t.Fatalf("Compact offline = %v, want E_OFFLINE", err)
	}
	if gitT(t, a.Dir(), "rev-parse", "HEAD") != head {
		t.Fatal("an offline compaction moved the branch")
	}
}

// TestRepoModeRefusesCompaction: the code repo's history is never rewritten.
func TestRepoModeRefusesCompaction(t *testing.T) {
	t.Parallel()
	b, err := OpenRepo(context.Background(), newRepo(t), rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	_, err = b.Compact(context.Background(), CompactRequest{Cutoff: time.Now()})
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.Refused {
		t.Fatalf("Compact in repo mode = %v, want E_REFUSED", err)
	}
}

// TestCompactDryRunCountsTheRemote: a teammate's commit pushed after this
// clone's last fetch is counted by the dry run as the real run will (the
// real run syncs first), and the dry run pushes nothing of its own
// (machines TestLifeStorePush/compaction: "dry run said Kept 1", the run
// kept 2).
func TestCompactDryRunCountsTheRemote(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	b := clone(t, remote, WithMerger(newestMerger{}))
	ga, gb := newGen(t), newGen(t)
	write(t, a, store.Change{Row: ga.row(newID(t), "a's row", hlc.HLC{})})
	syncOK(t, a)
	write(t, b, store.Change{Row: gb.row(newID(t), "b's row", hlc.HLC{})})
	syncOK(t, b)
	write(t, a, store.Change{Row: ga.row(newID(t), "a's unpushed row", hlc.HLC{})})
	remoteTip := gitT(t, remote, "rev-parse", "main")
	cutoff := time.Now().Add(time.Hour) // every commit so far is history
	plan, err := a.Compact(ctx, CompactRequest{Cutoff: cutoff, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := gitT(t, remote, "rev-parse", "main"); got != remoteTip {
		t.Fatal("a dry run pushed")
	}
	res, err := a.Compact(ctx, CompactRequest{Cutoff: cutoff})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Nothing || plan.Kept != res.Kept || plan.Squashed != res.Squashed || plan.Rows != res.Rows {
		t.Fatalf("dry run planned %+v; the run did %+v", plan, res)
	}
}

// TestSquashKeepsHandEditCommits: push mode batch never erases the person
// of a hand edit (brief 2.1). Lore's own commits around a hand edit are
// squashed into one batch commit each side; the hand edit stays a commit
// of its own, authored by the person with the org identity as committer,
// and the pushed tree is the clone's. When the local history holds a
// merge (a teammate pushed meanwhile), it is pushed as it is, the hand
// edit's author kept.
func TestSquashKeepsHandEditCommits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	org := Signature{Name: "Northwind", Email: "lore@northwind.invalid"}
	mira := Signature{Name: "Mira Lore", Email: "mira@northwind.invalid"}
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}), WithSigner(org), WithIdentity(func(context.Context) Signature { return mira }))
	g := newGen(t)
	hand := store.WithPersonAuthor(store.WithCommitMessage(ctx, "hand edit: memories x"))
	lw := func(b *Backend, gen *gen) {
		t.Helper()
		write(t, b, store.Change{Row: gen.row(newID(t), "lore", hlc.HLC{})})
	}
	before := remoteCount(t, remote)
	lw(a, g)
	lw(a, g)
	if err := a.Write(hand, []store.Change{{Row: g.row(newID(t), "by hand", hlc.HLC{})}}); err != nil {
		t.Fatal(err)
	}
	lw(a, g)
	lw(a, g)
	if _, err := a.SyncWith(ctx, SyncOptions{Squash: true}); err != nil {
		t.Fatal(err)
	}
	if got := remoteCount(t, remote); got != before+3 {
		t.Fatalf("remote has %d new commits, want 3 (batch, hand edit, batch):\n%s", got-before, gitT(t, remote, "log", "--format=%s|%an", testBranch))
	}
	want := []string{
		"lore: batch 2 rows (memories 2)|Northwind|Northwind",
		"hand edit: memories x|Mira Lore|Northwind",
		"lore: batch 2 rows (memories 2)|Northwind|Northwind",
	}
	got := strings.Split(gitT(t, remote, "log", "--reverse", "--format=%s|%an|%cn", "-3", testBranch), "\n")
	if !slices.Equal(got, want) {
		t.Fatalf("pushed history:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if gitT(t, remote, "rev-parse", testBranch+"^{tree}") != gitT(t, a.Dir(), "rev-parse", "HEAD^{tree}") {
		t.Fatal("the pushed tree is not the clone's")
	}

	// A teammate pushes meanwhile: a's sync merges, the history is not
	// linear, and it goes out as it is with the hand edit's author.
	b := clone(t, remote, WithMerger(newestMerger{}))
	gb := newGen(t)
	lw(b, gb)
	syncOK(t, b)
	lw(a, g)
	if err := a.Write(hand, []store.Change{{Row: g.row(newID(t), "by hand again", hlc.HLC{})}}); err != nil {
		t.Fatal(err)
	}
	lw(a, g)
	if _, err := a.SyncWith(ctx, SyncOptions{Squash: true}); err != nil {
		t.Fatal(err)
	}
	authors := gitT(t, remote, "log", "--format=%s|%an", "--grep=^hand edit", testBranch)
	if strings.Count(authors, "hand edit: memories x|Mira Lore") != 2 {
		t.Fatalf("hand edits on the remote:\n%s\nwant two by Mira Lore", authors)
	}
}
