package wire

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/store"
)

// TestCommandPushesOnce (devco:D-1002, store growth item 4): in push mode
// now, a command that writes a store three times pushes once, at its end:
// the remote gains one commit and the command prints one push line.
// Break-verified: before, each write pushed (three remote commits, three
// lines).
func TestCommandPushesOnce(t *testing.T) {
	t.Parallel()
	bare, clone := squashTestClone(t)
	var notes bytes.Buffer
	b := &storePush{inner: clone, name: "notes", res: pushRes(t), stateDir: t.TempDir(), notes: &notes, push: nowPush()}
	before := squashRemoteCount(t, bare)
	ctx, flush := withCommandPush(background())
	for i := 0; i < 3; i++ {
		if err := b.Write(ctx, []store.Change{{Row: cloneTestRow(t)}}); err != nil {
			t.Fatal(err)
		}
	}
	if got := squashRemoteCount(t, bare) - before; got != 0 {
		t.Fatalf("the remote gained %d commits before the command ended, want 0 (%s)", got, notes.String())
	}
	flush(background())
	if got := squashRemoteCount(t, bare) - before; got != 1 {
		t.Fatalf("the remote gained %d commits, want 1 (%s)", got, notes.String())
	}
	if n := strings.Count(notes.String(), "pushed store"); n != 1 {
		t.Fatalf("%d push lines, want 1:\n%s", n, notes.String())
	}
	if n, _ := clone.Unpushed(background()); n != 0 {
		t.Fatalf("%d commits still wait after the command", n)
	}
}

// TestCommandPushSkipsWhatSyncSent: a command that writes and then syncs
// the store itself (lore sync, a roster step that pushes on purpose) has
// nothing left to push at its end: no second push line.
func TestCommandPushSkipsWhatSyncSent(t *testing.T) {
	t.Parallel()
	_, clone := squashTestClone(t)
	var notes bytes.Buffer
	b := &storePush{inner: clone, name: "notes", res: pushRes(t), stateDir: t.TempDir(), notes: &notes, push: nowPush()}
	ctx, flush := withCommandPush(background())
	if err := b.Write(ctx, []store.Change{{Row: cloneTestRow(t)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	flush(background())
	if n := strings.Count(notes.String(), "pushed store"); n != 1 {
		t.Fatalf("%d push lines, want 1:\n%s", n, notes.String())
	}
}

// TestCommandPushCancelledWaits: a command cancelled before its end
// pushes nothing; the commit waits in the clone and the command says so.
func TestCommandPushCancelledWaits(t *testing.T) {
	t.Parallel()
	bare, clone := squashTestClone(t)
	var notes bytes.Buffer
	b := &storePush{inner: clone, name: "notes", res: pushRes(t), stateDir: t.TempDir(), notes: &notes, push: nowPush()}
	before := squashRemoteCount(t, bare)
	ctx, flush := withCommandPush(background())
	if err := b.Write(ctx, []store.Change{{Row: cloneTestRow(t)}}); err != nil {
		t.Fatal(err)
	}
	outer, cancel := context.WithCancel(background())
	cancel()
	flush(outer)
	if got := squashRemoteCount(t, bare) - before; got != 0 {
		t.Fatalf("the remote gained %d commits from a cancelled command", got)
	}
	if !strings.Contains(notes.String(), "committed locally") {
		t.Fatalf("no note that the write stayed local:\n%s", notes.String())
	}
}

// TestCommandPushNestedRunPushesOnce: a Run inside a Run (one operation
// running another) leaves the push to the outer command.
func TestCommandPushNestedRunPushesOnce(t *testing.T) {
	t.Parallel()
	outer, flushOuter := withCommandPush(background())
	inner, flushInner := withCommandPush(outer)
	if inner != outer {
		t.Fatal("a nested Run collects on its own")
	}
	flushInner(background())
	flushOuter(background())
}
