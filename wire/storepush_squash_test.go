package wire

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/storepush"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// TestStorePushSquashesEveryMode (devco:D-1000, store growth item 4): a
// `lore sync` of a store in the default push mode (manual) sends the 20
// writes waiting in the clone as ONE remote commit with the clone's tree;
// so do batch and now; squash=off keeps one remote commit per write.
// Break-verified: before, only batch squashed, and manual and now pushed
// all 20 commits.
func TestStorePushSquashesEveryMode(t *testing.T) {
	t.Parallel()
	off := storepush.Default()
	off.NoSquash = true
	batch := storepush.Default()
	batch.Push = storepush.ModeBatch
	for _, tc := range []struct {
		name string
		push *storepush.Settings
		want int
	}{
		{"manual (default)", nil, 1},
		{"batch", &batch, 1},
		{"now", nowPush(), 1},
		{"squash off", &off, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := background()
			bare, clone := squashTestClone(t)
			for i := 0; i < 20; i++ {
				if err := clone.Write(ctx, []store.Change{{Row: cloneTestRow(t)}}); err != nil {
					t.Fatal(err)
				}
			}
			before := squashRemoteCount(t, bare)
			var notes bytes.Buffer
			b := &storePush{inner: clone, name: "notes", res: pushRes(t), stateDir: t.TempDir(), notes: &notes, push: tc.push}
			rep, err := b.Sync(ctx)
			if err != nil || rep.Offline || len(rep.Refused) > 0 {
				t.Fatalf("sync = %+v, %v (%s)", rep, err, notes.String())
			}
			if got := squashRemoteCount(t, bare) - before; got != tc.want {
				t.Fatalf("the remote gained %d commits, want %d", got, tc.want)
			}
			if spOut(t, bare, "rev-parse", "main^{tree}") != spOut(t, clone.Dir(), "rev-parse", "HEAD^{tree}") {
				t.Fatal("the pushed tree is not the clone's")
			}
			if n, _ := clone.Unpushed(ctx); n != 0 {
				t.Fatalf("%d commits still wait after the sync", n)
			}
		})
	}
}

// squashTestClone is a bare store remote with a marker and lore's clone
// of it.
func squashTestClone(t *testing.T) (string, *gitbackend.Backend) {
	t.Helper()
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
	clone, err := gitbackend.OpenClone(background(), bare, filepath.Join(root, "alice", "clones", "host", "notes"), rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clone.Close() })
	return bare, clone
}

// squashRemoteCount is the commit count of the remote's main.
func squashRemoteCount(t *testing.T, bare string) int {
	t.Helper()
	n, err := strconv.Atoi(spOut(t, bare, "rev-list", "--count", "main"))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// spOut runs git in dir and returns its trimmed output.
func spOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}
