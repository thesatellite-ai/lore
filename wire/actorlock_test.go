package wire

// actorlock_test.go — devco D-738: an agent's first write after lore org
// link hung for good. ensureActor held state.actorMu while it wrote the
// actor row; that write's audit looked up the session row, the lookup
// resolved the folder's project (rosterProject), found no row yet and
// wrote it through withAgentRow -> withActorRows -> actorMu.Lock: the
// process waited on itself.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// actorLockApp is an agent App over a folder store whose web folder has no
// project row yet (TestAgentFolderProjectRowFollowsTheAgentRow's setup).
func actorLockApp(t *testing.T) (*App, *state, store.Source) {
	t.Helper()
	repo, storeDir, yamlPath, yaml := folderStoreRepo(t)
	for _, f := range []string{"org", "web"} {
		if err := os.MkdirAll(filepath.Join(storeDir, f), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(storeDir, f, rowfs.MetaFileName), []byte(`{"_v":1}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		yaml += "  - name: northwind/" + f + "\n    url: file://" + filepath.ToSlash(storeDir) + "\n    folder: " + f + "\n"
	}
	if err := os.WriteFile(yamlPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{RepoDir: repo, Home: t.TempDir(), FlagActor: agentKey, RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	withProject(t, a)
	st, err := a.writeState(background())
	if err != nil {
		t.Fatal(err)
	}
	return a, st, store.Source("northwind/web")
}

// returnsWithin runs fn and fails the test when it does not return in time (the
// old code never returned: it waited on a lock its own call chain held).
func returnsWithin(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatalf("[devco:D-738] %s did not return in 20s: it waits on the actor lock its own write holds", what)
	}
}

// TestSessionLookupNeverWritesTheFolderProject: the session lookup made
// while ensureActor holds actorMu resolves a folder with no project row
// to the folder's natural id, without writing the row (and so without
// asking for actorMu again).
func TestSessionLookupNeverWritesTheFolderProject(t *testing.T) {
	t.Parallel()
	a, st, web := actorLockApp(t)
	pid, err := folderProjectIDOf(background(), st, web)
	if err != nil {
		t.Fatal(err)
	}
	st.actorMu.Lock() // as ensureActor holds it while its actor row's audit runs
	var got string
	var rerr error
	returnsWithin(t, "the session lookup's project resolution", func() {
		ref, e := a.rosterProject(withFindingSession(background()), st, web, "web")
		got, rerr = ref.String(), e
	})
	st.actorMu.Unlock()
	switch {
	case pid.IsZero() && !errors.Is(rerr, engine.ErrNoPlaceProject):
		// a folder with no natural id (this test backend): no project to link yet
		t.Fatalf("[devco:D-738] the session lookup resolved %q, %v; want no project yet (ErrNoPlaceProject)", got, rerr)
	case !pid.IsZero() && (rerr != nil || got != pid.String()):
		t.Fatalf("[devco:D-738] the session lookup resolved %q, %v; want the folder's natural id %s", got, rerr, pid)
	}
	rows, err := st.eng.List(background(), web, schema.TableProjects)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if !r.Deleted {
			t.Fatalf("[devco:D-738] the session lookup wrote the folder's project row %s (only a write makes it)", r.ID)
		}
	}
	// the write itself still makes the row, under the same natural id
	ref, err := a.rosterProject(background(), st, web, "web")
	if err != nil || (!pid.IsZero() && ref.String() != pid.String()) {
		t.Fatalf("the write's own project resolution: %q, %v; want %s", ref.String(), err, pid)
	}
}

// TestActorLockReentryRefused: a call chain that already holds actorMu
// and asks for it again (a nested actor-row batch, a nested ensureActor)
// gets E_INTERNAL at once, never a wait on itself.
func TestActorLockReentryRefused(t *testing.T) {
	t.Parallel()
	a, st, web := actorLockApp(t)
	ctx, unlock, err := lockActors(background(), st)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { unlock() }()
	var berr, aerr error
	returnsWithin(t, "a nested actor-row batch", func() {
		_, berr = a.actorBatchOnce(ctx, st, func(context.Context) (engine.Written, error) { return engine.Written{}, nil })
	})
	returnsWithin(t, "a nested ensureActor", func() { aerr = a.ensureActor(ctx, st, web) })
	for what, e := range map[string]error{"nested batch": berr, "nested ensureActor": aerr} {
		if !errs.Has(e, errs.Internal) {
			t.Fatalf("[devco:D-738] %s on a chain holding the actor lock: %v; want E_INTERNAL", what, e)
		}
	}
	// another chain (no mark on its context) still waits its turn, then gets the lock
	got := make(chan struct{})
	go func() {
		_, u, lerr := lockActors(background(), st)
		if lerr == nil {
			u()
		}
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("another call chain got the actor lock while this one holds it")
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	unlock = func() {}
	returnsWithin(t, "another chain after unlock", func() { <-got })
}
