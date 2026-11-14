package live

import (
	"context"
	"os"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitremote/gitremotetest"
)

// TestFetchWritesNoStateFile (C-8): a read fetches too, and a read writes
// nothing in .git. A fetch keeps the url it reached in memory; no state
// file appears under StateDir until a seen marker or a push needs it.
func TestFetchWritesNoStateFile(t *testing.T) {
	ctx := context.Background()
	w := gitremotetest.NewWorld(t)
	b := w.Clone("b")
	rb := newRef(t, b, b.Remote)
	rb.RemoteURL = "file:///srv/widgets.git"
	if err := rb.Fetch(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := rb.Integrate(ctx); err != nil { // no live ref on the remote yet
		t.Fatal(err)
	}
	if ents, err := os.ReadDir(rb.StateDir); err == nil && len(ents) > 0 {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("a fetch with no live ref wrote state files: %v", names)
	}
}

// TestMovedRemoteAcrossRuns (G-1, one Ref per run like real processes):
// a run that fetched the old remote records its url with the seen marker;
// after `git remote set-url` a run that does not fetch keeps that url; the
// next fetch of the new remote (no live ref) is a move, not a deletion.
func TestMovedRemoteAcrossRuns(t *testing.T) {
	ctx := context.Background()
	w := gitremotetest.NewWorld(t)
	a, b := w.Clone("a"), w.Clone("b")
	ra := newRef(t, a, a.Remote)
	write(t, ra, put("tsk_1", NewVersion(nil, row("tsk_1", "keep", "low"), t0, "act_a")))
	mustPush(t, ra)
	run := func(url string) *Ref {
		r := newRef(t, b, b.Remote)
		r.RemoteURL = url
		return r
	}
	r1 := run("file:///srv/old.git")
	if err := r1.Fetch(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r1.Integrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := run("file:///srv/new.git").Integrate(ctx); err != nil { // set-url; no fetch
		t.Fatal(err)
	}
	gitremotetest.Git(t, w.OriginDir, "update-ref", "-d", RefLive) // the new remote has no live ref
	r3 := run("file:///srv/new.git")
	if err := r3.Fetch(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := r3.Integrate(ctx)
	if err != nil || res.Action != IntegrateNeverPushed {
		t.Fatalf("want %s, got %+v %v", IntegrateNeverPushed, res, err)
	}
}
