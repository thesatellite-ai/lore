package wire

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/storepush"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// TestStorePushReadSaysWaitingWhileBackingOff (devco:D-387): a read
// command whose store fetch backs off (the remote was out of reach less
// than live.fetch_ttl ago, devco:U-6) still names the local commits that
// wait and `lore sync`, once; it neither fetches nor pushes. Before, the
// back-off returned before the waiting line, so an offline person was not
// told their write had not left the machine. Break-verified by dropping
// the readWaiting call in refresh's back-off branch.
func TestStorePushReadSaysWaitingWhileBackingOff(t *testing.T) {
	t.Parallel()
	ctx := background()
	root := t.TempDir()
	bare := filepath.Join(root, "notes.git")
	seed := filepath.Join(root, "seed")
	spGit(t, root, "init", "-q", "--bare", "-b", "main", bare)
	spGit(t, root, "init", "-q", "-b", "main", seed)
	id, err := ids.New[ids.Store]()
	if err != nil {
		t.Fatal(err)
	}
	marker, err := rowfs.Marker{ID: id}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, rowfs.MarkerFile), marker, 0o600); err != nil {
		t.Fatal(err)
	}
	spGit(t, seed, "add", rowfs.MarkerFile)
	spGit(t, seed, "commit", "-q", "-m", "init")
	spGit(t, seed, "push", "-q", bare, "main")
	clone, err := gitbackend.OpenClone(ctx, bare, filepath.Join(root, "alice", "clones", "host", "notes"), rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clone.Close() })
	// one commit made offline: committed in the clone, never pushed
	if err := clone.Write(ctx, []store.Change{{Row: cloneTestRow(t)}}); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	now := time.Now()
	if err := live.WriteFetchStamp(state, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := live.WriteFetchFailed(state, now.Add(-10*time.Second)); err != nil {
		t.Fatal(err)
	}
	var notes bytes.Buffer
	b := &storePush{inner: clone, name: "notes", res: pushRes(t), stateDir: state, notes: &notes, push: nowPush()}
	read := WithNoCreate(ctx)
	for i := 0; i < 2; i++ {
		if _, _, err := b.Read(read, ""); err != nil {
			t.Fatal(err)
		}
	}
	if !b.backingOff {
		t.Fatal("the read did not back off: the test no longer covers the back-off branch")
	}
	if got := strings.Count(notes.String(), WaitingLine("notes", 1)); got != 1 {
		t.Fatalf("the read said the waiting line %d times, want once: %q", got, notes.String())
	}
	if n, err := clone.Unpushed(ctx); err != nil || n != 1 {
		t.Fatalf("unpushed = %d (%v), want 1: a backing-off read never pushes", n, err)
	}
}

// TestStorePushManualStoreSaysLocalWhileBackingOff (devco:D-387): a write
// to a store that pushes only on `lore sync` (the default, C5) says
// "committed locally … lore sync sends it" even while the fetch backs off
// (devco:U-6); the back-off note ("lore did not try it again") is only for
// a store that would have pushed. Break-verified by moving the backingOff
// check in pushAfterWrite back above the push-mode checks.
func TestStorePushManualStoreSaysLocalWhileBackingOff(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		push *storepush.Settings
		want string
	}{
		{"manual (default)", nil, "committed locally to store notes; lore sync sends it"},
		{"now", nowPush(), backoffWhy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := t.TempDir()
			now := time.Now()
			if err := live.WriteFetchStamp(state, now.Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			if err := live.WriteFetchFailed(state, now.Add(-10*time.Second)); err != nil {
				t.Fatal(err)
			}
			f := &fakePushBackend{caps: store.Capabilities{Remote: true}}
			var notes bytes.Buffer
			b := &storePush{inner: f, name: "notes", res: pushRes(t), stateDir: state, notes: &notes, push: tc.push}
			ctx := background()
			if _, _, err := b.Read(ctx, ""); err != nil { // a write command's first read decides the back-off
				t.Fatal(err)
			}
			if err := b.Write(ctx, nil); err != nil {
				t.Fatal(err)
			}
			if !b.backingOff {
				t.Fatal("the write did not back off: the test no longer covers the back-off")
			}
			if f.syncs != 0 {
				t.Fatalf("a backing-off write synced %d times", f.syncs)
			}
			if !strings.Contains(notes.String(), tc.want) {
				t.Fatalf("note %q, want it to say %q", notes.String(), tc.want)
			}
		})
	}
}
