package wire

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/trash"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// stubSync is a sync host whose row verbs answer with err (E_UNSUPPORTED:
// cmd/lore's live host; anything else: a host that implements them).
type stubSync struct {
	opssync.Env
	err error
}

func (s stubSync) SyncMergeRows(context.Context, opssync.MergeRowsIn) (opssync.DoneOut, error) {
	return opssync.DoneOut{Done: "host"}, s.err
}

func (s stubSync) SyncFixProjects(context.Context, opssync.FixProjectsIn) (opssync.DoneOut, error) {
	return opssync.DoneOut{Done: "host"}, s.err
}

// syncRowsWorld is an App with two repo projects, each holding a memory.
func syncRowsWorld(t *testing.T) (a *App, first, second, memFirst, memSecond ids.AnyID) {
	t.Helper()
	a = openWith(t, gitRepo(t), Options{RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	ctx := background()
	mk := func(table string, fields map[string]value.Value) ids.AnyID {
		w, err := a.Create(ctx, store.SourceRepo, row.Table(table), fields)
		if err != nil {
			t.Fatal(err)
		}
		return w.Row.ID
	}
	text := func(s string) value.Value {
		v, err := value.TextOf(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	mem := func(project ids.AnyID, body string) ids.AnyID {
		return mk(schema.TableMemories, map[string]value.Value{"project_id": text(project.String()), "kind": text(schema.MemoryKindCore), "body": text(body), "source_kind": text("manual")})
	}
	first = mk(schema.TableProjects, map[string]value.Value{"name": text("app")})
	second = mk(schema.TableProjects, map[string]value.Value{"name": text("app-2")})
	return a, first, second, mem(first, "first"), mem(second, "second")
}

// memProject is the project_id of memory id.
func memProject(t *testing.T, a *App, id ids.AnyID) string {
	t.Helper()
	_, r, err := a.Get(background(), engine.Ref{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	return r.Fields["project_id"].String()
}

// TestFixProjectsFoldsIntoOne (P3-SYNC-T2, E1): two live projects; a dry
// run changes nothing; fix-projects keeps the smallest id (no _meta.json),
// re-points the other's memory and puts the folded project in the trash
// with "merged into <keep>"; a second run has nothing to fix. --keep names
// the kept one; an unknown --keep is E_NOT_FOUND.
func TestFixProjectsFoldsIntoOne(t *testing.T) {
	a, first, second, memFirst, memSecond := syncRowsWorld(t)
	ctx := background()
	if _, err := a.FixProjects(ctx, "prj_00000000000000000000000000000000", false); !errs.Has(err, errs.NotFound) {
		t.Fatalf("unknown --keep = %v, want E_NOT_FOUND", err)
	}
	dry, err := a.FixProjects(ctx, second.String(), true)
	if err != nil || len(dry.Merged) != 1 || dry.Keep != second || len(dry.Merged[0].Referrers) == 0 {
		t.Fatalf("dry run = %+v, %v", dry, err)
	}
	if memProject(t, a, memFirst) != first.String() {
		t.Fatal("a dry run re-pointed a row")
	}
	fx, err := a.FixProjects(ctx, second.String(), false)
	if err != nil || len(fx.Merged) != 1 || fx.Merged[0].Drop != first {
		t.Fatalf("fix = %+v, %v", fx, err)
	}
	for _, m := range []ids.AnyID{memFirst, memSecond} {
		if got := memProject(t, a, m); got != second.String() {
			t.Fatalf("memory %s names %s, want the kept %s", m, got, second)
		}
	}
	all, err := a.Trashed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tr := range all {
		if in, ok := trash.Read(tr.Row); ok && tr.Row.ID == first && in.Reason == engine.MergedIntoReason+second.String() {
			found = true
		}
	}
	if !found {
		t.Fatalf("the folded project is not in the trash as merged: %+v", all)
	}
	again, err := a.FixProjects(ctx, "", false)
	if err != nil || len(again.Merged) != 0 {
		t.Fatalf("second run = %+v, %v", again, err)
	}
}

// TestSyncRowsFallsBackOnlyOnUnsupported: a host that implements
// merge-rows keeps its answer; a host answering E_UNSUPPORTED hands the
// verb to the core, which merges; no host at all registers nil (every
// sync verb E_UNSUPPORTED, P3-SYNC-1).
func TestSyncRowsFallsBackOnlyOnUnsupported(t *testing.T) {
	a, first, second, memFirst, _ := syncRowsWorld(t)
	ctx := background()
	if a.syncEnv() != nil {
		t.Fatal("no sync host must stay nil")
	}
	in := opssync.MergeRowsIn{Table: string(schema.TableProjects), Keep: second.String(), Drop: first.String()}
	own := syncRows{Env: stubSync{}, app: a}
	if out, err := own.SyncMergeRows(ctx, in); err != nil || out.Done != "host" {
		t.Fatalf("host answer = %+v, %v", out, err)
	}
	if memProject(t, a, memFirst) != first.String() {
		t.Fatal("the core ran although the host implements merge-rows")
	}
	core := syncRows{Env: stubSync{err: errs.New(errs.Unsupported, "not here")}, app: a}
	if _, err := core.SyncMergeRows(ctx, opssync.MergeRowsIn{Table: "memories", Keep: second.String(), Drop: first.String()}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("ids of another table = %v, want E_INVALID_INPUT", err)
	}
	out, err := core.SyncMergeRows(ctx, in)
	if err != nil || len(out.Items) < 3 {
		t.Fatalf("core merge = %+v, %v", out, err)
	}
	if memProject(t, a, memFirst) != second.String() {
		t.Fatal("the core merge did not re-point the memory")
	}
}

func (s stubSync) SyncTrashList(context.Context, opssync.TrashListIn) (opssync.TrashListOut, error) {
	return opssync.TrashListOut{}, s.err
}

func (s stubSync) SyncTrashRestore(context.Context, opssync.TrashRestoreIn) (opssync.DoneOut, error) {
	return opssync.DoneOut{}, s.err
}

// TestSyncTrashPointsAtTrashLocal (P3-SYNC-T1, DELETION 3.10, OV31-a): on
// a host without lore.db copies, `lore sync trash` prints the pointer line
// and answers as `lore trash --local` (no copies: empty); `sync trash
// restore <n>` prints its pointer and gets trash restore --local's own
// answer (no such copy: E_NOT_FOUND naming the listing).
func TestSyncTrashPointsAtTrashLocal(t *testing.T) {
	var notes []string
	unsupported := stubSync{err: errs.New(errs.Unsupported, "not here")}
	a := openWith(t, gitRepo(t), Options{Sync: unsupported, Notice: func(m string) { notes = append(notes, m) }, RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	env := a.syncEnv()
	out, err := env.SyncTrashList(background(), opssync.TrashListIn{})
	if err != nil || out.Count != 0 || len(out.Data) != 0 || out.Kind != opssync.KindTrash || out.Text() != "· trash is empty\n" {
		t.Fatalf("sync trash = %+v, %v", out, err)
	}
	if len(notes) == 0 || notes[0] != "note: lore sync trash is now lore trash --local; this spelling goes away in the next release" {
		t.Fatalf("pointer = %q", notes)
	}
	notes = nil
	if _, err := env.SyncTrashRestore(background(), opssync.TrashRestoreIn{ID: "3"}); !errs.Has(err, errs.NotFound) || !strings.Contains(err.Error(), "#3") {
		t.Fatalf("sync trash restore = %v, want E_NOT_FOUND for copy #3", err)
	}
	if len(notes) == 0 || !strings.Contains(notes[0], "lore trash restore --local 3") {
		t.Fatalf("restore pointer = %q", notes)
	}
}
