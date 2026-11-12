package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/storetest"
)

const memories row.Table = "memories"

// rootRowPath is the clone-relative slash path of a memory row of the
// store's root folder (_store/memories/<id>.json, D-ad).
func rootRowPath(id ids.AnyID) string {
	return rowfs.LayoutStore.StorePath(rowfs.FolderRoot, rowfs.RowPath(memories, id))
}

// testBranch is the branch every test remote is created on.
const testBranch = "main"

// gitT runs git in dir with a fixed identity and fails the test on error.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := tryGit(t, dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

// tryGit runs git in dir with a fixed identity through the package runner.
func tryGit(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	bin, err := lookGit(DefaultBinary)
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid"}
	out, err := runner{bin: bin}.run(context.Background(), call{dir: dir, args: args, env: env})
	return strings.TrimSpace(string(out)), err
}

// newRemote makes a bare store remote whose first commit holds the marker
// (withMarker false leaves a plain repo with one code file).
func newRemote(t *testing.T, withMarker bool) string {
	t.Helper()
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	gitT(t, dir, "init", "--quiet", "--bare", "--initial-branch="+testBranch, remote)
	seed := filepath.Join(dir, "seed")
	gitT(t, dir, "init", "--quiet", "--initial-branch="+testBranch, seed)
	name, body := "main.go", []byte("package main\n")
	if withMarker {
		id, err := ids.New[ids.Store]()
		if err != nil {
			t.Fatal(err)
		}
		if body, err = (rowfs.Marker{ID: id}).Encode(); err != nil {
			t.Fatal(err)
		}
		name = rowfs.MarkerFile
	}
	if err := os.WriteFile(filepath.Join(seed, name), body, 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, seed, "add", name)
	gitT(t, seed, "commit", "--quiet", "-m", "init")
	gitT(t, seed, "push", "--quiet", remote, testBranch)
	return remote
}

// clone opens a store clone of remote under its own home.
func clone(t *testing.T, remote string, opts ...Option) *Backend {
	t.Helper()
	b, err := OpenClone(context.Background(), remote, filepath.Join(t.TempDir(), "clones", "host", "store"), rowfs.FolderRoot, store.OpenOptions{}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	return b
}

// cloneFixture is one remote and one clone folder: every Open is another
// handle on the same clone, as two lore processes would be.
type cloneFixture struct {
	remote, base, folder string
}

func (f cloneFixture) Open(t *testing.T, o store.OpenOptions) store.Backend {
	t.Helper()
	b, err := OpenClone(context.Background(), f.remote, f.base, f.folder, o)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Files implements storetest.PlainFiles: the clone is the folder a person
// browses; lore's own files live in its .git (the lock, the journal and
// the stamps in .git/lore).
func (f cloneFixture) Files(*testing.T) (string, []string) { return f.base, []string{".git"} }

// Layout implements storetest.StoreLayout: the clone is a store checkout.
func (f cloneFixture) Layout(*testing.T) (string, string) { return f.base, f.folder }

// Tracked implements storetest.PlainFiles: rel is in the clone's HEAD.
func (f cloneFixture) Tracked(t *testing.T, rel string) bool {
	_, err := tryGit(t, f.base, "cat-file", "-e", "HEAD:"+rel)
	return err == nil
}

func TestContractClone(t *testing.T) {
	t.Parallel()
	storetest.Run(t, func(t *testing.T) storetest.Fixture {
		return cloneFixture{remote: newRemote(t, true), base: filepath.Join(t.TempDir(), "c"), folder: rowfs.FolderRoot}
	})
}

func TestContractCloneOrgFolder(t *testing.T) {
	t.Parallel()
	storetest.Run(t, func(t *testing.T) storetest.Fixture {
		return cloneFixture{remote: newRemote(t, true), base: filepath.Join(t.TempDir(), "c"), folder: rowfs.FolderOrg}
	})
}

// repoFixture is a code repository opened in repo mode.
type repoFixture struct{ dir string }

func (f repoFixture) Open(t *testing.T, o store.OpenOptions) store.Backend {
	t.Helper()
	b, err := OpenRepo(context.Background(), f.dir, rowfs.FolderRoot, o)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitT(t, dir, "init", "--quiet", "--initial-branch="+testBranch, ".")
	return dir
}

func TestContractRepo(t *testing.T) {
	t.Parallel()
	storetest.Run(t, func(t *testing.T) storetest.Fixture { return repoFixture{dir: newRepo(t)} })
}

// gen builds rows with one test clock.
type gen struct {
	t     *testing.T
	clock *hlc.Clock
}

func newGen(t *testing.T) *gen {
	t.Helper()
	actor, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	return &gen{t: t, clock: hlc.NewClock(actor, time.Now)}
}

func (g *gen) row(id ids.AnyID, body string, prev hlc.HLC) store.Row {
	g.t.Helper()
	v, err := value.TextOf(body)
	if err != nil {
		g.t.Fatal(err)
	}
	stamp := g.clock.Now(prev)
	r, err := store.FromRow(row.Row{Table: memories, ID: id, HLC: stamp, UpdatedAt: stamp.Wall, UpdatedBy: stamp.Actor, Fields: map[string]value.Value{"body": v}})
	if err != nil {
		g.t.Fatal(err)
	}
	return r
}

func newID(t *testing.T) ids.AnyID {
	t.Helper()
	id, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	return id.Any()
}

func write(t *testing.T, b *Backend, changes ...store.Change) {
	t.Helper()
	if err := b.Write(context.Background(), changes); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func syncOK(t *testing.T, b *Backend) store.SyncReport {
	t.Helper()
	rep, err := b.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if rep.Offline || len(rep.Refused) > 0 {
		t.Fatalf("sync report: %+v", rep)
	}
	return rep
}

func get(t *testing.T, b *Backend, id ids.AnyID) store.Row {
	t.Helper()
	r, err := b.Get(context.Background(), memories, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return r
}

// newestMerger is the test Merger: the side with the higher HLC wins the
// whole row (enough to prove the wiring; logic/merge does it per field).
type newestMerger struct{}

func (newestMerger) Merge(_, ours, theirs []byte) ([]byte, bool, error) {
	o, err := row.Decode(ours)
	if err != nil {
		return nil, false, err
	}
	th, err := row.Decode(theirs)
	if err != nil {
		return nil, false, err
	}
	if th.HLC.Compare(o.HLC) > 0 {
		return theirs, false, nil
	}
	return ours, false, nil
}

// conflictMerger calls every row a conflict.
type conflictMerger struct{}

func (conflictMerger) Merge(_, _, _ []byte) ([]byte, bool, error) { return nil, true, nil }

// garbageMerger returns a document that is not the row.
type garbageMerger struct{}

func (garbageMerger) Merge(_, _, _ []byte) ([]byte, bool, error) { return []byte("{}\n"), false, nil }

func TestWriteIsOneCommitPerBatch(t *testing.T) {
	t.Parallel()
	b, g := clone(t, newRemote(t, true)), newGen(t)
	before := gitT(t, b.Dir(), "rev-list", "--count", "HEAD")
	a, c := g.row(newID(t), "a", hlc.HLC{}), g.row(newID(t), "c", hlc.HLC{})
	author := Signature{Name: "Mira", Email: "mira@example.invalid"}
	if err := b.WriteBatch(context.Background(), Batch{Op: "memory.add", Author: author}, []store.Change{{Row: a}, {Row: c}}); err != nil {
		t.Fatal(err)
	}
	if after := gitT(t, b.Dir(), "rev-list", "--count", "HEAD"); after != "2" || before != "1" {
		t.Fatalf("commits %s → %s, want 1 → 2", before, after)
	}
	want := "lore: memory.add " + a.ID.String() + " (+1 more)|Mira|mira@example.invalid"
	if got := gitT(t, b.Dir(), "log", "-1", "--format=%s|%an|%ae"); got != want {
		t.Fatalf("commit = %q, want %q", got, want)
	}
	files := gitT(t, b.Dir(), "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(files, rowfs.RowPath(memories, a.ID)) || !strings.Contains(files, rowfs.RowPath(memories, c.ID)) || strings.Contains(files, ".lore/") {
		t.Fatalf("commit files:\n%s", files)
	}
	if status := gitT(t, b.Dir(), "status", "--porcelain"); status != "" {
		t.Fatalf("worktree not clean after a write:\n%s", status)
	}
	if err := b.WriteBatch(context.Background(), Batch{Op: "bad op\nInjected: x"}, []store.Change{{Row: g.row(newID(t), "x", hlc.HLC{})}}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("bad op name: %v", err)
	}
}

func TestRepoModeNeverCommits(t *testing.T) {
	t.Parallel()
	dir, g, ctx := newRepo(t), newGen(t), context.Background()
	b, err := OpenRepo(ctx, dir, rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := g.row(newID(t), "a", hlc.HLC{})
	write(t, b, store.Change{Row: a})
	if out, gerr := tryGit(t, dir, "rev-parse", "--verify", "--quiet", "HEAD"); gerr == nil {
		t.Fatalf("repo mode made a commit: %s", out)
	}
	if !strings.Contains(gitT(t, dir, "status", "--porcelain", "--untracked-files=all"), rowfs.RowPath(memories, a.ID)) {
		t.Fatal("row file not in the worktree")
	}
	rep, err := b.Sync(ctx)
	if err != nil || rep.Sent != 0 || rep.Received != 0 || rep.Offline || len(rep.Refused) != 0 {
		t.Fatalf("repo sync = %+v %v", rep, err)
	}
	// OVERNIGHT Qs 2026-11-05 (spec 5.5): repo mode reports History (row
	// history reads the repo's git log), nothing else.
	if got := b.Capabilities(); got != (store.Capabilities{History: true}) {
		t.Fatalf("repo mode capabilities = %+v, want History only", got)
	}
	if _, err := OpenRepo(ctx, t.TempDir(), rowfs.FolderRoot, store.OpenOptions{}); !errors.Is(err, store.ErrNotAStore) {
		t.Fatalf("outside a repo: %v", err)
	}
}

// TestRepoCursorSeesUncommittedEdits commits a row like a person would,
// then edits the file without committing: Read with the old cursor must
// return the edit.
func TestRepoCursorSeesUncommittedEdits(t *testing.T) {
	t.Parallel()
	dir, g, ctx := newRepo(t), newGen(t), context.Background()
	b, err := OpenRepo(ctx, dir, rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := g.row(newID(t), "a", hlc.HLC{})
	write(t, b, store.Change{Row: a})
	gitT(t, dir, "add", ".lore/data")
	gitT(t, dir, "commit", "--quiet", "-m", "rows")
	_, cur, err := b.Read(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	a2 := g.row(a.ID, "a2", a.HLC)
	write(t, b, store.Change{Row: a2, Expect: a.HLC})
	rows, _, err := b.Read(ctx, cur)
	if err != nil || len(rows) != 1 || !bytes.Equal(rows[0].Doc, a2.Doc) {
		t.Fatalf("uncommitted edit not read: %v %v", rows, err)
	}
}

func TestCapabilitiesClone(t *testing.T) {
	t.Parallel()
	b := clone(t, newRemote(t, true))
	if got := b.Capabilities(); got != (store.Capabilities{History: true, Review: true, Remote: true}) {
		t.Fatalf("capabilities = %+v", got)
	}
}

// TestTwoClonesConverge writes on two clones, including the same row on
// both sides, syncs both ways and checks both end with the same tree.
func TestTwoClonesConverge(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	b := clone(t, remote, WithMerger(newestMerger{}))
	ga, gb := newGen(t), newGen(t)

	x := ga.row(newID(t), "x v1", hlc.HLC{})
	write(t, a, store.Change{Row: x})
	if rep := syncOK(t, a); rep.Sent != 1 {
		t.Fatalf("first push sent %d rows, want 1", rep.Sent)
	}
	if rep := syncOK(t, b); rep.Received != 1 {
		t.Fatalf("b received %d rows, want 1", rep.Received)
	}
	if got := get(t, b, x.ID); !bytes.Equal(got.Doc, x.Doc) {
		t.Fatal("b does not have x")
	}

	xa := ga.row(x.ID, "x from a", x.HLC)
	write(t, a, store.Change{Row: xa, Expect: x.HLC}, store.Change{Row: ga.row(newID(t), "only a", hlc.HLC{})})
	syncOK(t, a)
	xb := gb.row(x.ID, "x from b", xa.HLC) // stamped after a's edit
	onlyB := gb.row(newID(t), "only b", hlc.HLC{})
	write(t, b, store.Change{Row: xb, Expect: x.HLC}, store.Change{Row: onlyB})
	rep := syncOK(t, b)
	if len(rep.Merged) != 1 || rep.Merged[0] != x.ID {
		t.Fatalf("b merged %v, want [x]", rep.Merged)
	}
	if rep.Received != 1 { // "only a"; x merged to b's own newer side
		t.Fatalf("b received %d rows, want 1", rep.Received)
	}
	syncOK(t, a)

	if gotA, gotB := get(t, a, x.ID), get(t, b, x.ID); !bytes.Equal(gotA.Doc, xb.Doc) || !bytes.Equal(gotB.Doc, xb.Doc) {
		t.Fatal("the newer edit did not win on both clones")
	}
	treeA, treeB := gitT(t, a.Dir(), "rev-parse", "HEAD^{tree}"), gitT(t, b.Dir(), "rev-parse", "HEAD^{tree}")
	if treeA != treeB {
		t.Fatalf("clones differ: %s vs %s", treeA, treeB)
	}
	if got := get(t, a, onlyB.ID); !bytes.Equal(got.Doc, onlyB.Doc) {
		t.Fatal("a lacks b's row")
	}
	if gitT(t, a.Dir(), "rev-parse", "HEAD") != gitT(t, remote, "rev-parse", testBranch) {
		t.Fatal("the remote is not at the clones' tip")
	}
}

// TestMergeFailsClosed: a row both sides changed with no merger, a merger
// that calls it a conflict, or a merger that returns a wrong document,
// never reaches the store; the local commit is kept and nothing is pushed.
func TestMergeFailsClosed(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		opts    []Option
		refused bool // false: Sync returns an error instead
	}{
		"no merger":      {nil, true},
		"conflict":       {[]Option{WithMerger(conflictMerger{})}, true},
		"wrong document": {[]Option{WithMerger(garbageMerger{})}, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			remote := newRemote(t, true)
			a := clone(t, remote)
			b := clone(t, remote, c.opts...)
			ga, gb := newGen(t), newGen(t)
			x := ga.row(newID(t), "x", hlc.HLC{})
			write(t, a, store.Change{Row: x})
			syncOK(t, a)
			syncOK(t, b)
			write(t, a, store.Change{Row: ga.row(x.ID, "a side", x.HLC), Expect: x.HLC})
			syncOK(t, a)
			xb := gb.row(x.ID, "b side", x.HLC)
			write(t, b, store.Change{Row: xb, Expect: x.HLC})
			localTip := gitT(t, b.Dir(), "rev-parse", "HEAD")
			remoteTip := gitT(t, remote, "rev-parse", testBranch)

			rep, err := b.Sync(context.Background())
			if c.refused {
				if err != nil || len(rep.Refused) != 1 || rep.Refused[0].ID != x.ID || rep.Refused[0].Code != errs.Stale {
					t.Fatalf("sync = %+v %v, want x refused", rep, err)
				}
			} else if err == nil {
				t.Fatalf("a bad merge result was accepted: %+v", rep)
			}
			if got := gitT(t, b.Dir(), "rev-parse", "HEAD"); got != localTip {
				t.Fatal("the local commit was not kept")
			}
			if got := gitT(t, remote, "rev-parse", testBranch); got != remoteTip {
				t.Fatal("something was pushed")
			}
			if st := gitT(t, b.Dir(), "status", "--porcelain"); st != "" {
				t.Fatalf("the merge was not aborted:\n%s", st)
			}
			if got := get(t, b, x.ID); !bytes.Equal(got.Doc, xb.Doc) {
				t.Fatal("b lost its own edit")
			}
		})
	}
}

// TestLostRaceRetries makes another clone push between this clone's fetch
// and push; the sync must fetch, merge and win on the retry.
func TestLostRaceRetries(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	a := clone(t, remote)
	b := clone(t, remote)
	ga, gb := newGen(t), newGen(t)
	y := ga.row(newID(t), "y", hlc.HLC{})
	var attempts []int
	b.beforePush = func(attempt int) {
		attempts = append(attempts, attempt)
		if attempt == 0 {
			write(t, a, store.Change{Row: y})
			syncOK(t, a)
		}
	}
	z := gb.row(newID(t), "z", hlc.HLC{})
	write(t, b, store.Change{Row: z})
	rep := syncOK(t, b)
	if len(attempts) != 2 {
		t.Fatalf("push attempts %v, want a lost race then a win", attempts)
	}
	if rep.Sent != 1 || rep.Received != 1 {
		t.Fatalf("report %+v, want sent 1 received 1", rep)
	}
	if got := get(t, b, y.ID); !bytes.Equal(got.Doc, y.Doc) {
		t.Fatal("b lacks the racing row")
	}
	if gitT(t, b.Dir(), "rev-parse", "HEAD") != gitT(t, remote, "rev-parse", testBranch) {
		t.Fatal("b's tip is not on the remote")
	}
}

// TestLostRaceGivesUp loses every race: after MaxPushAttempts the sync is
// E_CONTENDED and the local commit is kept.
func TestLostRaceGivesUp(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	a := clone(t, remote)
	b := clone(t, remote)
	ga, gb := newGen(t), newGen(t)
	b.beforePush = func(int) {
		write(t, a, store.Change{Row: ga.row(newID(t), "racer", hlc.HLC{})})
		syncOK(t, a)
	}
	z := gb.row(newID(t), "z", hlc.HLC{})
	write(t, b, store.Change{Row: z})
	if _, err := b.Sync(context.Background()); !errs.Has(err, errs.Contended) {
		t.Fatalf("err = %v, want E_CONTENDED", err)
	}
	if got := get(t, b, z.ID); !bytes.Equal(got.Doc, z.Doc) {
		t.Fatal("the local row was lost")
	}
}

// TestOfflineKeepsCommits removes the remote: Sync reports Offline, keeps
// the commit, and sends it once the remote is back.
func TestOfflineKeepsCommits(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	b, g := clone(t, remote), newGen(t)
	write(t, b, store.Change{Row: g.row(newID(t), "online", hlc.HLC{})})
	syncOK(t, b)
	away := remote + ".away"
	if err := os.Rename(remote, away); err != nil {
		t.Fatal(err)
	}
	w := g.row(newID(t), "offline", hlc.HLC{})
	write(t, b, store.Change{Row: w})
	rep, err := b.Sync(context.Background())
	if err != nil || !rep.Offline || rep.Sent != 0 {
		t.Fatalf("offline sync = %+v %v", rep, err)
	}
	if got := get(t, b, w.ID); !bytes.Equal(got.Doc, w.Doc) {
		t.Fatal("offline write lost")
	}
	if err := os.Rename(away, remote); err != nil {
		t.Fatal(err)
	}
	if rep := syncOK(t, b); rep.Sent != 1 {
		t.Fatalf("sent %d after reconnect, want 1", rep.Sent)
	}
}

func TestFirstOpenUnreachable(t *testing.T) {
	t.Parallel()
	_, err := OpenClone(context.Background(), filepath.Join(t.TempDir(), "absent.git"), filepath.Join(t.TempDir(), "c"), rowfs.FolderRoot, store.OpenOptions{})
	if !errs.Has(err, errs.StoreUnreachable) {
		t.Fatalf("err = %v, want E_STORE_UNREACHABLE", err)
	}
}

func TestNotAStore(t *testing.T) {
	t.Parallel()
	_, err := OpenClone(context.Background(), newRemote(t, false), filepath.Join(t.TempDir(), "c"), rowfs.FolderRoot, store.OpenOptions{})
	if !errors.Is(err, store.ErrNotAStore) {
		t.Fatalf("err = %v, want ErrNotAStore", err)
	}
}

// TestDeletedFileIsNotADelete removes a row file in a commit: Read with the
// old cursor does not return it, Missing reports it.
func TestDeletedFileIsNotADelete(t *testing.T) {
	t.Parallel()
	b, g, ctx := clone(t, newRemote(t, true)), newGen(t), context.Background()
	a := g.row(newID(t), "a", hlc.HLC{})
	write(t, b, store.Change{Row: a})
	_, cur, err := b.Read(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	gitT(t, b.Dir(), "rm", "--quiet", rootRowPath(a.ID))
	gitT(t, b.Dir(), "commit", "--quiet", "-m", "remove by hand")
	rows, _, err := b.Read(ctx, cur)
	if err != nil || len(rows) != 0 {
		t.Fatalf("a removed file was read as a change: %v %v", rows, err)
	}
	missing, err := b.Missing(ctx, cur)
	if err != nil || len(missing) != 1 || missing[0] != rowfs.RowPath(memories, a.ID) {
		t.Fatalf("missing = %v %v", missing, err)
	}
}
