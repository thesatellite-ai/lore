//go:build !windows

package git

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestReadOnlyHomeCloneReads (devco:U-4): a store clone in a lore home
// made read-only (a read-only mount, a home restored without write bits)
// opens without its lock: reads see its rows, a write and a sync answer
// E_READ_ONLY_FS (still store.ErrReadOnly) naming the home and the fix
// that works. Before, every command, reads included, failed taking the
// store lock, and the hint's chmod of the lock folder fixed nothing.
func TestReadOnlyHomeCloneReads(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through any mode")
	}
	ctx, g := context.Background(), newGen(t)
	remote := newRemote(t, true)
	home := filepath.Join(t.TempDir(), "lorehome")
	dir := filepath.Join(home, "stores", "acme", "team-store")
	b, err := OpenClone(ctx, remote, dir, rowfs.FolderRoot, store.OpenOptions{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	a := g.row(newID(t), "kept while writable", hlc.HLC{})
	write(t, b, store.Change{Row: a})
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	setModes(t, home, false)
	t.Cleanup(func() { setModes(t, home, true) })

	ro, err := OpenClone(ctx, remote, dir, rowfs.FolderRoot, store.OpenOptions{Home: home})
	if err != nil {
		t.Fatalf("open of a read-only home's clone: %v", err)
	}
	defer func() { _ = ro.Close() }()
	if !ro.Unwritable() {
		t.Fatal("the clone is not marked unwritable")
	}
	rows, _, err := ro.Read(ctx, "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("read: %d rows, %v; want the one row", len(rows), err)
	}
	werr := ro.Write(ctx, []store.Change{{Row: g.row(newID(t), "refused", hlc.HLC{})}})
	if !errs.Has(werr, errs.ReadOnlyFS) || !errors.Is(werr, store.ErrReadOnly) {
		t.Fatalf("write: %v, want E_READ_ONLY_FS wrapping store.ErrReadOnly", werr)
	}
	var le *errs.Error
	if !errors.As(werr, &le) || !strings.Contains(le.Hint, "chmod -R u+rwX "+home) || !strings.Contains(le.Hint, "LORE_HOME") {
		t.Fatalf("write hint %q must name the home's chmod and LORE_HOME", le.Hint)
	}
	if _, serr := ro.Sync(ctx); !errs.Has(serr, errs.ReadOnlyFS) {
		t.Fatalf("sync: %v, want E_READ_ONLY_FS", serr)
	}
}

// setModes makes every file and folder under root writable for its owner
// (on) or takes every write bit away (off), like chmod -R u+w / a-w.
func setModes(t *testing.T, root string, on bool) {
	t.Helper()
	paths := []string{}
	_ = filepath.WalkDir(root, func(p string, _ fs.DirEntry, err error) error {
		if err == nil {
			paths = append(paths, p)
		}
		return nil
	})
	if !on { // children first: a folder without its write bit is still walked
		for i, j := 0, len(paths)-1; i < j; i, j = i+1, j-1 {
			paths[i], paths[j] = paths[j], paths[i]
		}
	}
	for _, p := range paths {
		fi, err := os.Lstat(p)
		if err != nil || fi.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		mode := fi.Mode().Perm() &^ 0o222
		if on {
			mode = fi.Mode().Perm() | 0o200
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
}

// TestGitMissingFromPath (devco:U-5): no git on PATH is E_GIT_MISSING
// (refused, exit 23) naming the folders searched and the fixes that exist;
// before, it was E_INTERNAL "install git or pass its path", and no flag or
// variable takes a path.
func TestGitMissingFromPath(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	_, err := lookGit(DefaultBinary)
	if !errs.Has(err, errs.GitMissing) {
		t.Fatalf("lookGit with no git on PATH: %v, want E_GIT_MISSING", err)
	}
	var le *errs.Error
	if !errors.As(err, &le) || !strings.Contains(le.Message, empty) || strings.Contains(le.Hint, "pass its path") ||
		!strings.Contains(le.Hint, "install git, or put the folder that holds it on PATH") {
		t.Fatalf("message %q / hint %q", le.Message, le.Hint)
	}
}

// TestNewestCommitSeesAFutureStamp (devco:U-3): NewestCommit returns the
// newest committer stamp of the clone's branch, a future one included, with
// its author, so doctor can compare it with this machine's clock.
func TestNewestCommitSeesAFutureStamp(t *testing.T) {
	ctx := context.Background()
	b := clone(t, newRemote(t, true))
	future := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	stamp := future.UTC().Format(time.RFC3339)
	bin, err := lookGit(DefaultBinary)
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"GIT_AUTHOR_NAME=Kai Ahead", "GIT_AUTHOR_EMAIL=kai@example.invalid", "GIT_COMMITTER_NAME=Kai Ahead",
		"GIT_COMMITTER_EMAIL=kai@example.invalid", "GIT_AUTHOR_DATE=" + stamp, "GIT_COMMITTER_DATE=" + stamp}
	if _, err := (runner{bin: bin}).run(ctx, call{dir: b.Dir(), args: []string{"commit", "--quiet", "--allow-empty", "-m", "ahead"}, env: env}); err != nil {
		t.Fatal(err)
	}
	at, author, ok := b.NewestCommit(ctx)
	if !ok || !at.Equal(future) || author != "Kai Ahead" {
		t.Fatalf("NewestCommit = %v %q %v, want %v by Kai Ahead", at, author, ok, future)
	}
}
