package wire

// dryhome_seed_test.go — T-3: `lore fix --dry-run` built its scratch
// cache from nothing on every run (5,500 rows read, decoded and indexed:
// 5-17 s). The scratch cache now starts as a copy of the real one, so its
// refresh reads only what changed; the real cache is never written.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/model/ids"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// plainText is an expander that leaves text as it is (the test only
// reads the cursor).
type plainText struct{}

func (plainText) Expand(text string) (string, []string, error) { return text, nil, nil }

// cursorAt is the repo store's cursor in the cache file at path ("" for a
// cache built from nothing).
func cursorAt(t *testing.T, path string) store.Cursor {
	t.Helper()
	reg, err := builtinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	c, err := cacheOpen(context.Background(), path, reg, plainText{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	cur, err := c.Cursor(context.Background(), store.SourceRepo)
	if err != nil {
		t.Fatal(err)
	}
	return cur
}

// TestDryRunCacheStartsFromTheRealOne: under the dry-run switch the repo
// cache in the scratch folder is a copy of the real one (same cursor, so
// the open's refresh has nothing to re-read), a dry run reads every row,
// and the real cache file is left byte for byte as it was.
// Break-verification: with seeding skipped the scratch cache starts empty.
func TestDryRunCacheStartsFromTheRealOne(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	repo := gitRepo(t)
	home := t.TempDir()
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{RepoDir: repo, Home: home, Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	withProject(t, a)
	if _, err := run(t, a, opsmemory.OpAdd, `{"text":"read by the dry run"}`, false); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	real := RepoCachePath(home, repo)
	realCur := cursorAt(t, real)
	if realCur == "" {
		t.Fatal("the real cache has no cursor after a run")
	}
	before, err := os.ReadFile(real)
	if err != nil {
		t.Fatal(err)
	}

	undo := git.ForbidClones()
	t.Cleanup(undo)
	t.Cleanup(RemoveDryScratch)

	scratch := RepoCachePath(home, repo)
	if scratch == real {
		t.Fatal("under the dry-run switch the cache must live in the scratch folder")
	}
	if err := os.MkdirAll(filepath.Dir(scratch), 0o700); err != nil { // the open's mkCacheDir
		t.Fatal(err)
	}
	t.Run("break-verification: no seed, empty cache", func(t *testing.T) {
		if cur := cursorAt(t, scratch); cur != "" {
			t.Fatalf("a scratch cache opened without the seed has cursor %q", cur)
		}
		dropDryCache(scratch)
	})
	if !seedDryCache(scratch) {
		t.Fatal("the real cache was not copied")
	}
	if seedDryCache(scratch) {
		t.Fatal("a second seed must keep the scratch copy")
	}
	if cur := cursorAt(t, scratch); cur != realCur {
		t.Fatalf("the scratch cache's cursor %q, the real one's %q", cur, realCur)
	}
	dropDryCache(scratch)

	dry, err := New(Options{RepoDir: repo, Home: home, Actor: actor})
	if err != nil {
		t.Fatal(err)
	}
	out, err := run(t, dry, opsmemory.OpList, ``, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "read by the dry run") {
		t.Fatalf("the dry run lists the row: %s", out)
	}
	if err := dry.Close(); err != nil {
		t.Fatal(err)
	}
	if cur := cursorAt(t, scratch); cur == "" {
		t.Fatal("the dry run's scratch cache has no cursor")
	}
	after, err := os.ReadFile(real)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a dry run wrote the real cache")
	}
}
