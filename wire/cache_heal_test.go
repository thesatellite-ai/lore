package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestCacheDamagedIsMovedAsideAndRebuilt (B11 port of the old binary's
// doctor_cache_test.go): a damaged repo cache (garbage in place of the
// repo's cache under the lore home, no WAL) no longer breaks every
// command: the next App moves it aside beside itself
// (<file>.corrupt-<time>), says so exactly once through Options.Notice,
// rebuilds from .lore/data and answers with every row.
// With healing off (break-verification of openHealing) the same open fails.
func TestCacheDamagedIsMovedAsideAndRebuilt(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	repo := gitRepo(t)
	home := t.TempDir()
	actor, aerr := ids.Parse[ids.Actor](testActor)
	if aerr != nil {
		t.Fatal(aerr)
	}
	openApp := func(notes *[]string) *App {
		t.Helper()
		opts := Options{RepoDir: repo, Home: home, Actor: actor}
		if notes != nil {
			opts.Notice = func(m string) { *notes = append(*notes, m) }
		}
		a, nerr := New(opts)
		if nerr != nil {
			t.Fatal(nerr)
		}
		return a
	}

	first := openApp(nil)
	withProject(t, first)
	out, addErr := run(t, first, opsmemory.OpAdd, `{"text":"survives a damaged cache"}`, false)
	if addErr != nil {
		t.Fatal(addErr)
	}
	id := idOf(t, out)
	if cerr := first.Close(); cerr != nil {
		t.Fatal(cerr)
	}

	cache := RepoCachePath(home, repo)
	damage := func() {
		t.Helper()
		for _, s := range cacheSidecars {
			if err := os.Remove(cache + s); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(cache, []byte("not a database"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Break-verification: healing off, the damaged file fails the command.
	damage()
	t.Run("healing off fails", func(t *testing.T) {
		healCaches = false
		t.Cleanup(func() { healCaches = true })
		a := openApp(nil)
		defer func() { _ = a.Close() }() // the open failed; Close has nothing to report
		if _, err := run(t, a, opsmemory.OpList, ``, false); err == nil {
			t.Fatal("a damaged cache with healing off must fail the command")
		}
	})

	damage()
	var notes []string
	a := openApp(&notes)
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	got, listErr := run(t, a, opsmemory.OpList, ``, false)
	if listErr != nil {
		t.Fatalf("a damaged cache must be rebuilt, not fail: %v", listErr)
	}
	if !strings.Contains(got, id) {
		t.Fatalf("the rebuilt cache lost the row %s: %s", id, got)
	}
	n := 0
	for _, m := range notes {
		if strings.Contains(m, "was damaged; moved it to") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want exactly one heal notice, got %d: %q", n, notes)
	}
	kept, globErr := filepath.Glob(cache + corruptSuffix + "*")
	if globErr != nil {
		t.Fatal(globErr)
	}
	var mains []string
	for _, k := range kept {
		if !strings.HasSuffix(k, "-wal") && !strings.HasSuffix(k, "-shm") {
			mains = append(mains, k)
		}
	}
	if len(mains) != 1 {
		t.Fatalf("the damaged file is kept once beside the cache, got %v", kept)
	}
}
