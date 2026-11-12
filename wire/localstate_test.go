package wire

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/cache/sqlite"
	"github.com/thesatellite-ai/lore/store/localstate"
)

// TestLocalStatePath (later 53; one machine file 2026-11-12): the
// machine-local state lives in the machine store's file
// <home>/state/machine.db, App.LocalState opens it there (creating the
// folder), and a value written through one handle is read by the next.
func TestLocalStatePath(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if got, want := LocalStatePath(home), filepath.Join(home, "state", "machine.db"); got != want {
		t.Fatalf("LocalStatePath = %q, want %q", got, want)
	}
	a := openWith(t, gitRepo(t), Options{Home: home})
	ctx := background()
	s, err := a.LocalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Put(ctx, localstate.SecretChecks, "vault/x", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(LocalStatePath(home)); err != nil {
		t.Fatalf("the file is not at LocalStatePath: %v", err)
	}
	s, err = a.LocalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if e, ok, err := s.Get(ctx, localstate.SecretChecks, "vault/x"); err != nil || !ok || string(e.Doc) != `{"ok":true}` {
		t.Fatalf("second handle: %+v %v %v", e, ok, err)
	}
}

// TestLocalStateAdoptsOldFile: an older state/local.db (its own file
// before 2026-11-12) is moved into the machine store's file at the first
// open: every row kept (a row already in machine.db wins), the old file
// and its sidecars gone, and a second open has nothing to move.
func TestLocalStateAdoptsOldFile(t *testing.T) {
	t.Parallel()
	ctx := background()
	home := t.TempDir()
	old, err := sqlite.OpenLocalState(ctx, oldLocalStatePath(home))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"run/a": `{"min":5}`, "run/b": `{"min":7}`} {
		if err = old.Put(ctx, localstate.TimeOutbox, k, []byte(v)); err != nil {
			t.Fatal(err)
		}
	}
	if err = old.Close(); err != nil {
		t.Fatal(err)
	}
	cur, err := sqlite.OpenLocalState(ctx, LocalStatePath(home))
	if err != nil {
		t.Fatal(err)
	}
	if err = cur.Put(ctx, localstate.TimeOutbox, "run/b", []byte(`{"min":9}`)); err != nil {
		t.Fatal(err)
	}
	if err = cur.Close(); err != nil {
		t.Fatal(err)
	}
	ls, err := openLocalState(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ls.Close() }()
	for k, want := range map[string]string{"run/a": `{"min":5}`, "run/b": `{"min":9}`} {
		if e, ok, gerr := ls.Get(ctx, localstate.TimeOutbox, k); gerr != nil || !ok || string(e.Doc) != want {
			t.Fatalf("%s after the move: %q %v %v, want %s", k, e.Doc, ok, gerr, want)
		}
	}
	for _, suf := range []string{"", "-wal", "-shm"} {
		if _, serr := os.Stat(oldLocalStatePath(home) + suf); !os.IsNotExist(serr) {
			t.Fatalf("state/local.db%s is still there (%v)", suf, serr)
		}
	}
	if err = ls.AdoptFile(ctx, oldLocalStatePath(home)); err != nil {
		t.Fatalf("a second move: %v", err)
	}
}
