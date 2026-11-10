package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestReadPushState: a store clone's standing against its remote, read
// without the network: in sync after a push (with its time), ahead with
// the oldest unpushed commit's time after a local write, behind after a
// fetch that saw another clone's push, and no push on record in a clone
// that only fetched.
func TestReadPushState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	a, b := clone(t, remote), clone(t, remote)
	ga, gb := newGen(t), newGen(t)

	st, err := ReadPushState(ctx, "", a.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != testBranch || !st.Tracked || st.Ahead != 0 || st.Behind != 0 || !st.LastPush.IsZero() {
		t.Fatalf("fresh clone: %+v", st)
	}

	before := time.Now().Add(-time.Second)
	write(t, a, store.Change{Row: ga.row(newID(t), "placeholder one", hlc.HLC{})})
	write(t, a, store.Change{Row: ga.row(newID(t), "placeholder two", hlc.HLC{})})
	if st, err = ReadPushState(ctx, "", a.Dir()); err != nil {
		t.Fatal(err)
	}
	if st.Ahead != 2 || st.Behind != 0 || st.OldestUnpushed.Before(before) || st.OldestUnpushed.After(time.Now()) || !st.LastPush.IsZero() {
		t.Fatalf("two local writes: %+v", st)
	}

	syncOK(t, a)
	if st, err = ReadPushState(ctx, "", a.Dir()); err != nil {
		t.Fatal(err)
	}
	if st.Ahead != 0 || !st.OldestUnpushed.IsZero() || st.LastPush.Before(before) {
		t.Fatalf("after the push: %+v", st)
	}

	// b fetches a.s push (two commits) without merging it: behind 2 (as
	// a read would leave it)
	gitT(t, b.Dir(), "fetch", "--quiet", RemoteName)
	write(t, b, store.Change{Row: gb.row(newID(t), "placeholder three", hlc.HLC{})})
	if st, err = ReadPushState(ctx, "", b.Dir()); err != nil {
		t.Fatal(err)
	}
	if st.Ahead != 1 || st.Behind != 2 || !st.LastPush.IsZero() {
		t.Fatalf("b, one local and two remote commits: %+v", st)
	}
}

// TestLastPushReadsOnlyPushes: fetch lines are no push; a missing log is
// no push.
func TestLastPushReadsOnlyPushes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if got, err := lastPush(filepath.Join(dir, "none")); err != nil || !got.IsZero() {
		t.Fatalf("missing log: %v %v", got, err)
	}
	log := filepath.Join(dir, "main")
	body := "0 1 t <t@example.com> 1700000000 +0000\tfetch: fast-forward\n" +
		"1 2 t <t@example.com> 1700000100 +0000\tupdate by push\n" +
		"2 3 t <t@example.com> 1700000200 +0000\tfetch origin: fast-forward\n"
	if err := os.WriteFile(log, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := lastPush(log)
	if err != nil || got.Unix() != 1700000100 {
		t.Fatalf("last push = %v %v, want 1700000100", got.Unix(), err)
	}
}
