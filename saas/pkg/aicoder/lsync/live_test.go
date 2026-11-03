package lsync

import (
	"context"
	"os"
	"strings"
	"testing"
)

func divertMemories(r RowRef) bool { return r.Table == "memories" }

// A diverted row is never written to .lore/data, stays dirty until
// ClearDirty, and is handed back exactly once per pass.
func TestDivertKeepsRowOutOfDataAndDirtyUntilCleared(t *testing.T) {
	e := newEnv(t)
	e.reconcile() // bootstrap
	id := e.addMemory("shared at once")
	rep := e.reconcile(func(o *Options) { o.Divert = divertMemories })
	if len(rep.Diverted) != 1 || rep.Diverted[0].ID != id || rep.Diverted[0].Doc == nil || rep.Diverted[0].HasBase {
		t.Fatalf("diverted: %+v", rep.Diverted)
	}
	if _, err := os.Stat(e.file("memories", id)); err == nil {
		t.Fatal("a diverted row must not reach .lore/data")
	}
	if n := e.count(`SELECT COUNT(*) FROM _lore_sync_dirty WHERE row_id = ?`, id); n != 1 {
		t.Fatal("a diverted row stays dirty until stored (crash safety)")
	}
	again := e.reconcile(func(o *Options) { o.Divert = divertMemories })
	if len(again.Diverted) != 1 {
		t.Fatal("not stored yet: the next pass hands it back again")
	}
	if err := ClearDirty(context.Background(), e.db, []RowRef{rep.Diverted[0].RowRef}); err != nil {
		t.Fatal(err)
	}
	if last := e.reconcile(func(o *Options) { o.Divert = divertMemories }); len(last.Diverted) != 0 {
		t.Fatalf("stored rows are not diverted again: %+v", last.Diverted)
	}
}

// A secret is refused before it reaches the caller (F2 edge case).
func TestDivertRunsTheSecretCheck(t *testing.T) {
	e := newEnv(t)
	e.reconcile()
	e.addMemory("key AKIA")
	rep := e.reconcile(func(o *Options) {
		o.Divert = divertMemories
		o.CheckSecrets = func(b []byte) string {
			if strings.Contains(string(b), "AKIA") {
				return "credential"
			}
			return ""
		}
	})
	if len(rep.Diverted) != 0 || !hasKind(rep.Errors, ErrKindSecret) {
		t.Fatalf("a secret must be refused, not diverted: %+v %+v", rep.Diverted, rep.Errors)
	}
}

func hasKind(errs []FileError, kind string) bool {
	for _, e := range errs {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

// A branch file change of a live row is recorded but never imported (live
// wins, fixed rule 4).
func TestDivertedRowIgnoresBranchFileChange(t *testing.T) {
	e := newEnv(t)
	id := e.addMemory("v1")
	e.reconcile() // bootstrap: exported to the branch
	e.editFile("memories", id, func(d map[string]any) { d["body"] = "branch edit" })
	e.reconcile(func(o *Options) { o.Divert = divertMemories })
	if got := e.memoryBody(id); got != "v1" {
		t.Fatalf("a live row must not import its branch file: %q", got)
	}
}

// LiveApply: live wins over the DB copy; unchanged live rows are skipped;
// a tombstone deletes; a row that leaves live keeps its DB copy unless
// purged.
func TestLiveApplyLifecycle(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	id := e.addMemory("local")
	e.reconcile()
	b, ok, err := RowDoc(ctx, e.db, "memories", id)
	if err != nil || !ok {
		t.Fatal(err)
	}
	doc := e.readDoc("memories", id)
	doc["body"] = "live version"
	e.writeDoc("memories", "tmp", doc) // only to get canonical bytes
	liveBody, _ := os.ReadFile(e.file("memories", "tmp"))
	_ = os.Remove(e.file("memories", "tmp"))
	k := LiveKey{Table: "memories", ID: id}

	rep, err := LiveApply(ctx, LiveApplyOptions{DB: e.db, Rows: map[LiveKey]LiveDoc{k: {Body: liveBody}}})
	if err != nil || len(rep.Imported) != 1 || e.memoryBody(id) != "live version" {
		t.Fatalf("live wins: %+v %v %q", rep, err, e.memoryBody(id))
	}
	if n := e.count(`SELECT COUNT(*) FROM _lore_sync_dirty WHERE row_id = ?`, id); n != 0 {
		t.Fatal("importing the live version must not leave the row dirty")
	}
	rep, _ = LiveApply(ctx, LiveApplyOptions{DB: e.db, Rows: map[LiveKey]LiveDoc{k: {Body: liveBody}}})
	if rep.Changed() {
		t.Fatal("an unchanged live row is not re-imported")
	}
	states, _ := LiveStates(ctx, e.db)
	if _, ok := states[k]; !ok {
		t.Fatal("the row is recorded as live")
	}
	// Leaves live: the DB keeps the (landed) copy.
	rep, _ = LiveApply(ctx, LiveApplyOptions{DB: e.db, Rows: map[LiveKey]LiveDoc{}})
	if len(rep.Left) != 1 || !e.memoryExists(id) {
		t.Fatalf("a row that left live keeps its DB copy: %+v", rep)
	}
	// Live again, then tombstoned.
	_, _ = LiveApply(ctx, LiveApplyOptions{DB: e.db, Rows: map[LiveKey]LiveDoc{k: {Body: b}}})
	rep, _ = LiveApply(ctx, LiveApplyOptions{DB: e.db, Rows: map[LiveKey]LiveDoc{k: {Deleted: true}}})
	if len(rep.Deleted) != 1 || e.memoryExists(id) {
		t.Fatal("a tombstone deletes the DB row")
	}
	if n := e.count(`SELECT COUNT(*) FROM _lore_sync_trash WHERE row_id = ?`, id); n != 1 {
		t.Fatal("a row deleted by a tombstone is kept in the trash")
	}
}

func TestLiveApplyPurgedRowLeavesTheDB(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	id := e.addMemory("client is slow to pay")
	e.reconcile()
	b, _, _ := RowDoc(ctx, e.db, "memories", id)
	k := LiveKey{Table: "memories", ID: id}
	_, _ = LiveApply(ctx, LiveApplyOptions{DB: e.db, Rows: map[LiveKey]LiveDoc{k: {Body: b}}})
	if _, err := LiveApply(ctx, LiveApplyOptions{DB: e.db, Rows: map[LiveKey]LiveDoc{}, Purged: map[string]bool{id: true}}); err != nil {
		t.Fatal(err)
	}
	if e.memoryExists(id) {
		t.Fatal("a purged id must vanish from every clone (§6.6)")
	}
}

func TestDirtyRefs(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	kept := e.addMemory("kept")
	e.reconcile()
	gone := e.addMemory("gone")
	if _, err := e.db.Exec(`DELETE FROM memories WHERE id = ?`, gone); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE memories SET body = 'edited' WHERE id = ?`, kept); err != nil {
		t.Fatal(err)
	}
	refs, err := DirtyRefs(ctx, e.db)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]RowRef{}
	for _, r := range refs {
		got[r.ID] = r
	}
	if r := got[kept]; !r.HasBase || r.Deleted {
		t.Fatalf("edited branch row: %+v", r)
	}
	if r := got[gone]; r.HasBase || !r.Deleted {
		t.Fatalf("new then deleted row: %+v", r)
	}
}

// F11: lsync owns only _v and project_id in _meta.json; a store project's
// links and global flag survive every rewrite.
func TestWriteProjectMetaKeepsOtherKeys(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/"+MetaFileName, []byte(`{"_v":1,"global":true,"links":[{"project_id":"prj_x","remote":"github.com/acme/app"}],"project_id":"prj_old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeProjectMeta(dir, ProjectMeta{ProjectID: "prj_01a10237c9e87e4c843d8045973a65a1"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(dir + "/" + MetaFileName)
	for _, want := range []string{`"links"`, `"global": true`, "prj_01a10237c9e87e4c843d8045973a65a1"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("%s missing after rewrite: %s", want, b)
		}
	}
	if strings.Contains(string(b), "prj_old") {
		t.Fatal("project_id must be replaced")
	}
}

// F16: a refused live write leaves nothing behind: the row leaves lore.db
// (kept in the trash) and the dirty set, so nothing is retried or pushed.
func TestDropRowsLeavesNothingToPush(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.reconcile()
	id := e.addMemory("follow up: client is slow to pay")
	if err := DropRows(ctx, e.db, []RowRef{{Table: "memories", ID: id}}, "refused by the leak check"); err != nil {
		t.Fatal(err)
	}
	if e.memoryExists(id) {
		t.Fatal("the refused row must leave lore.db")
	}
	if n := e.count(`SELECT COUNT(*) FROM _lore_sync_dirty WHERE row_id = ?`, id); n != 0 {
		t.Fatal("nothing may stay dirty (it would be retried and pushed later)")
	}
	if n := e.count(`SELECT COUNT(*) FROM _lore_sync_trash WHERE row_id = ? AND reason = ?`, id, "refused by the leak check"); n != 1 {
		t.Fatal("the row is kept in the sync trash")
	}
}

// F5 × F1: a row that left live is shown as its landed copy, not as the
// older live version this clone saw last.
func TestLiveApplyLeftRowTakesTheLandedCopy(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	id := e.addMemory("old live version")
	e.reconcile()
	old, _, _ := RowDoc(ctx, e.db, "memories", id)
	k := LiveKey{Table: "memories", ID: id}
	_, _ = LiveApply(ctx, LiveApplyOptions{DB: e.db, Rows: map[LiveKey]LiveDoc{k: {Body: old}}})
	doc := e.readDoc("memories", id)
	doc["body"] = "final version that landed"
	e.writeDoc("memories", "landed", doc)
	landed, _ := os.ReadFile(e.file("memories", "landed"))
	_ = os.Remove(e.file("memories", "landed"))
	rep, err := LiveApply(ctx, LiveApplyOptions{DB: e.db, Rows: map[LiveKey]LiveDoc{},
		Landed: func(LiveKey) ([]byte, bool) { return landed, true }})
	if err != nil || len(rep.Left) != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	if got := e.memoryBody(id); got != "final version that landed" {
		t.Fatalf("the left row must show its landed copy: %q", got)
	}
	if n := e.count(`SELECT COUNT(*) FROM _lore_sync_dirty WHERE row_id = ?`, id); n != 0 {
		t.Fatal("importing the landed copy leaves nothing dirty")
	}
}

// D8: two actors known only from client projections (no stable_key) are
// two rows, never merged as "the same" person; a clone that knows the real
// key keeps it.
func TestProjectedActorsGetDistinctPlaceholderKeys(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.reconcile()
	mk := func(id, name string) LiveDoc {
		b := []byte(`{"_table":"actors","_v":1,"display_name":"` + name + `","id":"` + id + `","kind":"human"}`)
		return LiveDoc{Body: b}
	}
	a, b := "act_01a10237c9e87e4c843d8045973a65a1", "act_01a10237c9e87e4c843d8045973a65a2"
	rep, err := LiveApply(ctx, LiveApplyOptions{DB: e.db, Rows: map[LiveKey]LiveDoc{
		{Table: "actors", ID: a}: mk(a, "Alice Doe"), {Table: "actors", ID: b}: mk(b, "Dev One")}})
	if err != nil || len(rep.Errors) != 0 || len(rep.Imported) != 2 {
		t.Fatalf("%+v %v", rep, err)
	}
	if n := e.count(`SELECT COUNT(*) FROM actors WHERE stable_key LIKE 'projected:%'`); n != 2 {
		t.Fatalf("two distinct projected actors expected, got %d", n)
	}
	// The env's own actor keeps its real key when a projection of it arrives.
	real := e.actor
	_, _ = LiveApply(ctx, LiveApplyOptions{DB: e.db, Rows: map[LiveKey]LiveDoc{{Table: "actors", ID: real}: mk(real, "dev")}})
	if n := e.count(`SELECT COUNT(*) FROM actors WHERE id = ? AND stable_key LIKE 'projected:%'`, real); n != 0 {
		t.Fatal("a known actor's real stable_key must never be replaced")
	}
}
