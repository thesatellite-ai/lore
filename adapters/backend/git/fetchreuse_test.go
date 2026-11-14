package git

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/tests/fakeexec"
)

// countingSSH is a fake ssh that logs the command git asks the host to run
// (git-upload-pack is a fetch, git-receive-pack a push) and runs it here.
const countingSSH = "#!/bin/sh\nfor a; do cmd=$a; done\necho \"$cmd\" >> \"$FAKE_SSH_LOG\"\nexec sh -c \"$cmd\"\n"

// sshCalls sets up countingSSH for git and returns a counter of the
// upload-pack and receive-pack calls logged since the last reset.
func sshCalls(t *testing.T) func(reset bool) (fetches, pushes int) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	bin := fakeexec.Write(t, filepath.Join(dir, "ssh"), countingSSH, 0o755)
	t.Setenv("GIT_SSH_COMMAND", filepath.ToSlash(bin))
	t.Setenv("GIT_SSH_VARIANT", "simple")
	t.Setenv("FAKE_SSH_LOG", log)
	return func(reset bool) (int, int) {
		b, _ := os.ReadFile(log) // no file yet: no call
		if reset {
			_ = os.Remove(log)
		}
		return strings.Count(string(b), "git-upload-pack"), strings.Count(string(b), "git-receive-pack")
	}
}

// devco L-12-perf: a sync of a clone this run already fetched
// (WithFetchedClone) pushes over that fetch with no upload-pack of its
// own; without the mark it fetches (so the counter bites), and a push that
// loses the race over the reused fetch still fetches, merges and lands
// both sides.
func TestSyncReusesThisRunsFetch(t *testing.T) {
	calls := sshCalls(t)
	remote := "ssh://fakehost" + filepath.ToSlash(newRemote(t, true))
	a := clone(t, remote, WithMerger(newestMerger{}))
	b := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	calls(true)

	write(t, a, store.Change{Row: g.row(newID(t), "one", hlc.HLC{})})
	rep, err := a.SyncWith(WithFetchedClone(context.Background(), a.Dir()), SyncOptions{})
	if err != nil || !rep.Pushed || rep.Sent != 1 {
		t.Fatalf("sync over the reused fetch: %+v %v", rep, err)
	}
	if f, p := calls(true); f != 0 || p != 1 {
		t.Fatalf("a sync of a clone fetched earlier in the run made %d fetch(es) and %d push(es), want 0 and 1", f, p)
	}

	write(t, a, store.Change{Row: g.row(newID(t), "two", hlc.HLC{})})
	syncOK(t, a)
	if f, p := calls(true); f != 1 || p != 1 {
		t.Fatalf("a plain sync made %d fetch(es) and %d push(es), want 1 and 1", f, p)
	}

	// b's tracking ref is stale: its push over the reused fetch is
	// rejected, so it fetches, merges and pushes again
	onlyB := g.row(newID(t), "only b", hlc.HLC{})
	write(t, b, store.Change{Row: onlyB})
	rep, err = b.SyncWith(WithFetchedClone(context.Background(), b.Dir()), SyncOptions{})
	if err != nil || !rep.Pushed || rep.Received != 2 {
		t.Fatalf("lost race over the reused fetch: %+v %v", rep, err)
	}
	syncOK(t, a)
	if got := get(t, a, onlyB.ID); !bytes.Equal(got.Doc, onlyB.Doc) {
		t.Fatal("b's row did not reach a after the retried push")
	}
	// a mark naming another clone's folder reuses nothing
	write(t, a, store.Change{Row: g.row(newID(t), "three", hlc.HLC{})})
	calls(true)
	if _, err := a.SyncWith(WithFetchedClone(context.Background(), b.Dir()), SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	if f, _ := calls(true); f != 1 {
		t.Fatalf("another clone's mark skipped this clone's fetch (%d fetches)", f)
	}
}
