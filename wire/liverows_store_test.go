package wire

import (
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// Fixed ids of rows that exist only on a store's live refs.
const (
	storeLiveOnlyMem  = "mem_01a1a59b891c70b789144cd3e711fc01"
	storeLiveRemote   = "mem_01a1a59b891c70b789144cd3e711fc02"
	storeLiveElsewher = "mem_01a1a59b891c70b789144cd3e711fc03"
)

// liveStoreName is the attached git store of TestMigrateLiveStore.
const liveStoreName store.Source = "kb"

// storeRow reads id from the kb store through the engine (ok=false when
// the store has no live row: missing, purged or trashed).
func storeRow(t *testing.T, a *App, id string) (row.Row, error) {
	t.Helper()
	eng, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	aid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	_, r, err := eng.Get(background(), engine.Ref{Store: liveStoreName, Table: schema.TableMemories, ID: aid})
	return r, err
}

// TestMigrateLiveStore (later 221, t2e-wire): lore migrate run brings the
// v0.1.17 live rows of an ATTACHED git store (its clone's
// refs/lore/live/<branch> and the copy fetched from the store remote) into
// that store. Matrix of the store side: absent (written with the live
// HLC), older (merged, the newer live field wins), newer (left alone),
// trashed and purged (tombstone kept, never resurrected by a newer live
// copy); the local live ref vs the remote's newer copy (newer wins); rows
// of another folder, a non-core table and the ref's own `_live/` files are
// skipped. The dry run lists "table/id (kb)" and writes nothing; the run
// lands the rows on the store remote; the note goes quiet and a second run
// is a no-op.
func TestMigrateLiveStore(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts := histGitWorld(t)
	bare := hosts + "/acme/kb"
	gitIn(t, hosts, nil, "init", "-q", "--bare", "-b", "main", bare)

	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withProject(t, a)
	if _, err := run(t, a, opsstores.OpInit, `{"name":"kb","url":"https://`+histHost+`/acme/kb"}`, true); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openWith(t, repo, Options{Home: a.opts.Home})

	older := histAddMemory(t, a, string(liveStoreName), "store older")
	newer := histAddMemory(t, a, string(liveStoreName), "store newer")
	trashed := histAddMemory(t, a, string(liveStoreName), "store trashed")
	purged := histAddMemory(t, a, string(liveStoreName), "store purged")
	if _, err := run(t, a, opsmemory.OpDelete, `{"id":"`+trashed+`"}`, true); err != nil {
		t.Fatal(err)
	}
	histPurge(t, a, liveStoreName, purged)
	cur, err := storeRow(t, a, older)
	if err != nil {
		t.Fatal(err)
	}
	proj := cur.Fields["project_id"].String()
	newerBefore, err := storeRow(t, a, newer)
	if err != nil {
		t.Fatal(err)
	}
	trashedBefore, err := storeRow(t, a, trashed)
	if err != nil {
		t.Fatal(err)
	}
	tombBefore, err := storeRow(t, a, purged)
	if err != nil || !tombBefore.Deleted {
		t.Fatalf("fixture: %s is not a tombstone: %+v %v", purged, tombBefore, err)
	}

	st, err := a.state(background())
	if err != nil {
		t.Fatal(err)
	}
	gb, ok := concrete(st.byName[liveStoreName]).(gitStoreBackend)
	if !ok || gb.Branch() == "" {
		t.Fatalf("kb is not a git store with a branch: %T", st.byName[liveStoreName])
	}
	branch := gb.Branch()
	histCommitLive(t, gb.Dir(), liveRefLocal+"/"+branch, map[string]string{
		livePath(storeLiveOnlyMem): liveMemory(storeLiveOnlyMem, proj, "live only", liveNewer),
		livePath(older):            liveMemory(older, proj, "live wins", liveNewer),
		livePath(newer):            liveMemory(newer, proj, "stale live", liveOld),
		livePath(trashed):          liveMemory(trashed, proj, "back from the trash", liveNewest),
		livePath(purged):           liveMemory(purged, proj, "back from the dead", liveNewest),
		livePath(storeLiveRemote):  liveMemory(storeLiveRemote, proj, "local copy", liveNewer),
		"other/" + schema.TableMemories + "/" + storeLiveElsewher + ".json": liveMemory(storeLiveElsewher, proj, "another folder", liveNewer),
		liveRootFolder + "/not_a_core_table/x.json":                         `{"_table":"not_a_core_table","_v":1,"id":"x"}`,
		"_live/meta.json": `{"not":"a row"}`,
	})
	// the store remote holds a newer copy of one row on its own live ref:
	// migrate fetches it into the clone's tracking ref first
	histCommitLive(t, bare, liveRefLocal+"/"+branch, map[string]string{
		livePath(storeLiveRemote): liveMemory(storeLiveRemote, proj, "remote copy", liveNewest),
	})

	got, err := a.MigrateLive(background(), true)
	if err != nil {
		t.Fatal(err)
	}
	suffix := " (" + string(liveStoreName) + ")"
	want := []string{
		schema.TableMemories + "/" + storeLiveOnlyMem + suffix,
		schema.TableMemories + "/" + older + suffix,
		schema.TableMemories + "/" + storeLiveRemote + suffix,
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("dry run lines: %v, want %v", got, want)
	}
	if _, gerr := storeRow(t, a, storeLiveOnlyMem); gerr == nil {
		t.Fatal("the dry run wrote a row")
	}
	remoteBefore := gitIn(t, bare, nil, "rev-parse", "main")

	if _, err = run(t, a, opsmigrate.OpRun, `{}`, false); err != nil {
		t.Fatal(err)
	}

	check := func(id, body, stamp string) {
		t.Helper()
		r, gerr := storeRow(t, a, id)
		if gerr != nil {
			t.Fatalf("%s: %v", id, gerr)
		}
		if r.Fields["body"].String() != body || r.HLC.String() != stamp {
			t.Fatalf("%s: body %q hlc %s, want %q %s", id, r.Fields["body"].String(), r.HLC, body, stamp)
		}
	}
	check(storeLiveOnlyMem, "live only", liveNewer)
	check(older, "live wins", liveNewer)
	check(storeLiveRemote, "remote copy", liveNewest)
	check(newer, "store newer", newerBefore.HLC.String())
	// the trashed row stays in the trash exactly as it was (a soft delete
	// keeps the row by id; the live copy must not edit it)
	if r, gerr := storeRow(t, a, trashed); gerr != nil || r.HLC != trashedBefore.HLC {
		t.Fatalf("trashed row changed: hlc %s -> %s (%v)", trashedBefore.HLC, r.HLC, gerr)
	}
	if _, err = run(t, a, opsmemory.OpShow, `{"id":"`+trashed+`"}`, false); err == nil {
		t.Fatal("the trashed memory is visible again")
	}
	if r, gerr := storeRow(t, a, purged); gerr != nil || !r.Deleted || r.HLC != tombBefore.HLC || len(r.Fields) != 0 {
		t.Fatalf("the tombstone was not kept: %+v %v", r, gerr)
	}
	if _, gerr := storeRow(t, a, storeLiveElsewher); gerr == nil {
		t.Fatal("a row of another folder was brought in")
	}
	if gitIn(t, bare, nil, "rev-parse", "main") == remoteBefore {
		t.Fatal("migrate did not push the store's live rows to its remote")
	}
	if tree := gitIn(t, bare, nil, "ls-tree", "-r", "--name-only", "main"); strings.Contains(tree, "not_a_core_table") || strings.Contains(tree, storeLiveElsewher) {
		t.Fatalf("a skipped live file reached the store:\n%s", tree)
	}

	if note := a.LiveNote(background()); note != "" {
		t.Fatalf("note after the run: %q", note)
	}
	if again, aerr := a.MigrateLive(background(), false); aerr != nil || len(again) != 0 {
		t.Fatalf("second run: %v %v", again, aerr)
	}
}
