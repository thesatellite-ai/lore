package git

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// devco:D-1029: a read's early fetch of a store clone (StartPrefetch) is
// the fetch of the clone's sync in that run: the sync makes no upload-pack
// of its own and still merges what a teammate pushed. Without the early
// fetch in ctx the same sync fetches (the counter bites); a second sync
// in the run fetches again (one sync takes it).
func TestSyncTakesTheReadsEarlyFetch(t *testing.T) {
	calls := sshCalls(t)
	remote := "ssh://fakehost" + filepath.ToSlash(newRemote(t, true))
	a := clone(t, remote, WithMerger(newestMerger{}))
	b := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	mate := g.row(newID(t), "a teammate's row", hlc.HLC{})
	write(t, b, store.Change{Row: mate})
	syncOK(t, b)
	calls(true)

	set := &Prefetches{}
	ctx := WithPrefetches(context.Background(), set)
	set.Add(a.Dir(), StartPrefetch(ctx, a.Dir()))
	set.Wait()
	if f, _ := calls(true); f != 1 {
		t.Fatalf("the early fetch made %d fetch(es), want 1", f)
	}
	rep, err := a.SyncWith(ctx, SyncOptions{NoPush: true})
	if err != nil || rep.Offline || rep.Received != 1 {
		t.Fatalf("sync over the early fetch: %+v %v", rep, err)
	}
	if f, _ := calls(true); f != 0 {
		t.Fatalf("the sync after the read's early fetch made %d fetch(es) of its own, want 0", f)
	}
	if got := get(t, a, mate.ID); !bytes.Equal(got.Doc, mate.Doc) {
		t.Fatal("the teammate's row fetched early was not merged")
	}
	if _, err := os.Stat(filepath.Join(StateDirOf(a.Dir()), FetchStampFile)); err != nil {
		t.Fatal("the sync over the early fetch wrote no fetch stamp")
	}
	// taken once: the next sync of the run fetches itself
	if _, err := a.SyncWith(ctx, SyncOptions{NoPush: true}); err != nil {
		t.Fatal(err)
	}
	if f, _ := calls(true); f != 1 {
		t.Fatalf("a second sync in the run made %d fetch(es), want 1 (the early fetch is taken once)", f)
	}
	// no early fetch in ctx: the sync fetches
	if _, err := a.SyncWith(context.Background(), SyncOptions{NoPush: true}); err != nil {
		t.Fatal(err)
	}
	if f, _ := calls(true); f != 1 {
		t.Fatalf("a sync with no early fetch made %d fetch(es), want 1", f)
	}
}

// devco:D-1029: an early fetch that failed is not taken: the sync fetches
// itself and reports the remote as before (offline), so an offline read
// says what it said before.
func TestSyncFetchesAgainAfterAFailedEarlyFetch(t *testing.T) {
	calls := sshCalls(t)
	bare := newRemote(t, true)
	a := clone(t, "ssh://fakehost"+filepath.ToSlash(bare), WithMerger(newestMerger{}))
	calls(true)
	set := &Prefetches{}
	ctx := WithPrefetches(context.Background(), set)
	t.Setenv("GIT_SSH_COMMAND", "false") // the host does not answer
	set.Add(a.Dir(), StartPrefetch(ctx, a.Dir()))
	set.Wait()
	rep, err := a.SyncWith(ctx, SyncOptions{NoPush: true})
	if err != nil || !rep.Offline {
		t.Fatalf("a sync after a failed early fetch: %+v %v, want offline as a plain sync", rep, err)
	}
}

// devco:D-1029: a fetch of the clone another caller made in a read (the
// org link index, NoteFetched) is the sync's fetch too, and the tracking
// tip from before that fetch is the one the sync compares (B3-29).
func TestSyncTakesAFetchNotedByTheOrgLookup(t *testing.T) {
	calls := sshCalls(t)
	remote := "ssh://fakehost" + filepath.ToSlash(newRemote(t, true))
	a := clone(t, remote, WithMerger(newestMerger{}))
	b := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	mate := g.row(newID(t), "a teammate's row", hlc.HLC{})
	write(t, b, store.Change{Row: mate})
	syncOK(t, b)
	ctx := WithPrefetches(context.Background(), &Prefetches{})
	before := TrackingTips(ctx, a.Dir())
	if before == nil {
		t.Fatal("no tracking tips read under a read's early fetches")
	}
	if _, err := a.git.run(ctx, call{dir: a.Dir(), args: storeFetchArgs()}); err != nil {
		t.Fatal(err)
	}
	NoteFetched(ctx, a.Dir(), before)
	calls(true)
	rep, err := a.SyncWith(ctx, SyncOptions{NoPush: true})
	if err != nil || rep.Received != 1 {
		t.Fatalf("sync over the noted fetch: %+v %v", rep, err)
	}
	if f, _ := calls(true); f != 0 {
		t.Fatalf("the sync after the org lookup's fetch made %d fetch(es), want 0", f)
	}
	if TrackingTips(context.Background(), a.Dir()) != nil {
		t.Fatal("tracking tips read outside a read's early fetches")
	}
}

// devco:D-1029: the run decides its early fetches beside the read
// (NewPrefetches, Started): a sync that comes first waits for that
// decision, then takes the fetch, so it never fetches a second time.
func TestSyncWaitsUntilTheEarlyFetchesStarted(t *testing.T) {
	calls := sshCalls(t)
	remote := "ssh://fakehost" + filepath.ToSlash(newRemote(t, true))
	a := clone(t, remote, WithMerger(newestMerger{}))
	calls(true)
	set := NewPrefetches()
	ctx := WithPrefetches(context.Background(), set)
	go func() {
		defer set.Started()
		time.Sleep(100 * time.Millisecond) // the decision reads config and stamp files
		set.Add(a.Dir(), StartPrefetch(ctx, a.Dir()))
	}()
	if _, err := a.SyncWith(ctx, SyncOptions{NoPush: true}); err != nil {
		t.Fatal(err)
	}
	set.Wait()
	if f, _ := calls(true); f != 1 {
		t.Fatalf("a sync that came before the early fetch was started made %d fetch(es) in all, want 1", f)
	}
}
