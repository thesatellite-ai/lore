package lsync

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestDanglingReferences(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	mem := e.addMemory("x")
	e.reconcile()
	ghost := "msn_01a10237c9e87e4c843d8045973a65ee"
	if _, err := e.db.Exec(`UPDATE memories SET superseded_by_id = 'mem_01a10237c9e87e4c843d8045973a65ef' WHERE id = ?`, mem); err != nil {
		t.Fatal(err)
	}
	tsk, err := e.client.Task.Create().SetProjectID(e.project).SetTitle("t").SetBody("b").SetMissionID(ghost).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := DanglingReferences(ctx, e.db)
	if err != nil {
		t.Fatal(err)
	}
	// msn_ has no rows anywhere, so the mission reference is not provably a
	// synced id; the memory → memory reference is.
	if len(refs) != 1 || refs[0].RowID != mem || refs[0].Column != "superseded_by_id" {
		t.Fatalf("refs = %+v (task %s)", refs, tsk.ID)
	}
}

func TestFindDuplicatesAndMergeRows(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	a := e.addMemory("Use  JWT for auth")
	b := e.addMemory("use jwt   for AUTH")
	e.addMemory("different")
	tag, err := e.client.Tag.Create().SetProjectID(e.project).SetName("auth").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.client.EntityTag.Create().SetEntityTable("memories").SetEntityID(b).SetTagID(tag.ID).Save(ctx); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	groups, err := FindDuplicates(ctx, e.db)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].Table != "memories" || len(groups[0].IDs) != 2 {
		t.Fatalf("groups = %+v", groups)
	}
	if err := MergeRows(ctx, e.db, "memories", a, b); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	if e.memoryExists(b) || !e.memoryExists(a) {
		t.Fatal("merge did not drop the duplicate")
	}
	if e.count(`SELECT COUNT(*) FROM entity_tags WHERE entity_id = ?`, a) != 1 {
		t.Fatal("tag binding not moved to the kept row")
	}
	if _, err := os.Stat(e.file("memories", b)); !os.IsNotExist(err) {
		t.Fatal("dropped row's file not removed")
	}
	trash, _ := ListTrash(ctx, e.db)
	if len(trash) != 1 || trash[0].RowID != b {
		t.Fatalf("trash must keep the dropped row with its own id: %+v", trash)
	}
	if err := MergeRows(ctx, e.db, "memories", a, a); err == nil {
		t.Fatal("self-merge must fail")
	}
	if err := MergeRows(ctx, e.db, "memories", a, "mem_01a10237c9e87e4c843d8045973a6500"); err == nil {
		t.Fatal("missing row must fail")
	}
	if err := MergeRows(ctx, e.db, "query_logs", a, b); err == nil {
		t.Fatal("local table must fail")
	}
	if groups, _ := FindDuplicates(ctx, e.db); len(groups) != 0 {
		t.Fatalf("still duplicated: %+v", groups)
	}
}

func TestDecodeRowFile(t *testing.T) {
	t.Parallel()
	if _, err := DecodeRowFile([]byte(`{"_v":1,"id":"x"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRowFile([]byte(`{"_v":9}`)); err == nil {
		t.Fatal("newer format must fail")
	}
}

func TestCheckFiles(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	good := e.addMemory("ok")
	bad := e.addMemory("will break")
	e.reconcile()
	e.writeRaw("memories", bad, []byte("{nope"))
	errs, err := CheckFiles(e.data)
	if err != nil || len(errs) != 1 || errs[0].Path != "memories/"+bad+".json" || errs[0].Kind != ErrKindInvalid {
		t.Fatalf("errs = %+v err %v (good %s)", errs, err, good)
	}
	if _, err := CheckFiles(t.TempDir() + "/missing"); err == nil {
		t.Fatal("missing dir must error")
	}
}

func TestOnChangeObserver(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	var got []Change
	observe := func(kind ChangeKind) func(*Options) {
		return func(o *Options) {
			o.DBChangeKind = kind
			o.OnChange = func(_ context.Context, _ Execer, c Change) error {
				got = append(got, c)
				return nil
			}
		}
	}
	e.reconcile() // bootstrap: not reported
	mem := e.addMemory("x")
	e.reconcile(observe(ChangeLocal))
	if len(got) != 1 || got[0].Kind != ChangeLocal || got[0].BeforeHash != "" || got[0].AfterHash == "" || got[0].ID != mem {
		t.Fatalf("local write: %+v", got)
	}
	got = nil
	if _, err := e.db.Exec(`UPDATE memories SET body = 'sqlite3 edit' WHERE id = ?`, mem); err != nil {
		t.Fatal(err)
	}
	e.reconcile(observe(ChangeExternal))
	if len(got) != 1 || got[0].Kind != ChangeExternal {
		t.Fatalf("external write: %+v", got)
	}
	got = nil
	e.editFile("memories", mem, func(d map[string]any) { d["body"] = "pulled" })
	e.reconcile(observe(ChangeLocal))
	if len(got) != 1 || got[0].Kind != ChangeImport {
		t.Fatalf("import: %+v", got)
	}
	got = nil
	// A pass that changes nothing reports nothing; an updated_at-only bump
	// is not a change.
	if _, err := e.db.Exec(`UPDATE memories SET updated_at = ? WHERE id = ?`, time.Now().Add(time.Hour), mem); err != nil {
		t.Fatal(err)
	}
	e.reconcile(observe(ChangeLocal))
	if len(got) != 0 {
		t.Fatalf("no-op reported: %+v", got)
	}
	// Errors abort the pass.
	if _, err := e.db.Exec(`UPDATE memories SET body = 'again' WHERE id = ?`, mem); err != nil {
		t.Fatal(err)
	}
	_, err := Reconcile(ctx, e.opts(func(o *Options) {
		o.OnChange = func(context.Context, Execer, Change) error { return os.ErrPermission }
	}))
	if err == nil {
		t.Fatal("observer error must abort the pass")
	}
}

func TestUnexportedChanges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	mem := e.addMemory("x")
	e.reconcile()
	if got, err := UnexportedChanges(ctx, e.db); err != nil || len(got) != 0 {
		t.Fatalf("clean: %+v %v", got, err)
	}
	if _, err := e.db.Exec(`UPDATE memories SET body = 'HACKED' WHERE id = ?`, mem); err != nil {
		t.Fatal(err)
	}
	got, err := UnexportedChanges(ctx, e.db)
	if err != nil || len(got) != 1 || got[0].ID != mem {
		t.Fatalf("tamper not seen: %+v %v", got, err)
	}
	// updated_at-only bumps are not changes
	e.reconcile()
	if _, err := e.db.Exec(`UPDATE memories SET updated_at = ? WHERE id = ?`, time.Now().Add(time.Hour), mem); err != nil {
		t.Fatal(err)
	}
	if got, _ := UnexportedChanges(ctx, e.db); len(got) != 0 {
		t.Fatalf("updated_at bump flagged: %+v", got)
	}
}
