package lsync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// teamOf returns clone A (bootstrapped, with some knowledge) and a second
// clone B whose own pre-existing lore.db never synced.
func teamOf(t *testing.T) (*env, *env) {
	t.Helper()
	a := newEnv(t)
	a.addMemory("shared from A")
	a.reconcile()
	b := newEnv(t)
	return a, b
}

func TestAdopt_UnifiesProjectAndMergesBothSides(t *testing.T) {
	t.Parallel()
	a, b := teamOf(t)
	bOnly := b.addMemory("only on B")
	a.copyDataTo(b)

	rep := b.reconcile()
	if rep.Pass != PassAdopt {
		t.Fatalf("pass = %s", rep.Pass)
	}
	// B's rows now hang off A's project id.
	if b.count(`SELECT COUNT(*) FROM projects`) != 1 || b.count(`SELECT COUNT(*) FROM projects WHERE id = ?`, a.project) != 1 {
		t.Fatal("project id not adopted")
	}
	if b.count(`SELECT COUNT(*) FROM memories WHERE project_id = ?`, a.project) != 2 {
		t.Fatal("rows not re-pointed to the shared project")
	}
	// B's private memory is exported onto B's branch.
	if !contains(rep.Exported, "memories/"+bOnly+".json") {
		t.Fatalf("B-only row not exported: %+v", rep.Exported)
	}
	if b.readDoc("memories", bOnly)["project_id"] != a.project {
		t.Fatal("exported file still carries the old project id")
	}
	if !hasPrefixIn(rep.Warnings, "local project") {
		t.Fatalf("project adoption not reported: %v", rep.Warnings)
	}
	// Converged.
	if again := b.reconcile(); again.Changed() || again.Pass != PassNormal {
		t.Fatalf("not converged after adopt: %+v", again)
	}
}

func TestAdopt_FreshCloneImportsEverything(t *testing.T) {
	t.Parallel()
	a := newEnv(t)
	mem := a.addMemory("knowledge")
	a.reconcile()
	fresh := newEmptyEnv(t)
	a.copyDataTo(fresh)
	rep := fresh.reconcile()
	if rep.Pass != PassAdopt || fresh.memoryBody(mem) != "knowledge" {
		t.Fatalf("fresh clone did not import: %+v", rep)
	}
	if len(rep.Exported) != 0 {
		t.Fatalf("a fresh clone must not rewrite shared files: %v", rep.Exported)
	}
}

func TestAdopt_ClashNewestWinsAndCopiesLoser(t *testing.T) {
	t.Parallel()
	a := newEnv(t)
	mem := a.addMemory("v1")
	a.reconcile()
	// B is a restored copy of the same DB that diverged.
	b := newEmptyEnv(t)
	a.copyDataTo(b)
	b.reconcile()
	if err := ResetForRestore(context.Background(), b.db, PreferNewest); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if _, err := b.db.Exec(`UPDATE memories SET body = 'newer in db', updated_at = ? WHERE id = ?`, future, mem); err != nil {
		t.Fatal(err)
	}
	rep := b.reconcile()
	if rep.Pass != PassAdopt || len(rep.Conflicts) != 1 || rep.Conflicts[0].Kept != "db" {
		t.Fatalf("rep = %+v", rep)
	}
	if b.readDoc("memories", mem)["body"] != "newer in db" {
		t.Fatal("newer DB version must win and be exported")
	}
	cs, _ := ListConflicts(context.Background(), b.db, false)
	if len(cs) != 1 || !strings.Contains(cs[0].OtherDoc, "v1") {
		t.Fatalf("loser not kept: %+v", cs)
	}
}

func TestAdopt_PreferModes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		prefer   PreferMode
		wantBody string
	}{
		{PreferDB, "restored"},
		{PreferFiles, "v1"},
	} {
		t.Run(string(tc.prefer), func(t *testing.T) {
			t.Parallel()
			a := newEnv(t)
			mem := a.addMemory("v1")
			a.reconcile()
			// Restore an older DB: its row is OLDER than the file, so only
			// the explicit preference can make the DB win.
			past := time.Now().Add(-time.Hour)
			if _, err := a.db.Exec(`UPDATE memories SET body = 'restored', updated_at = ? WHERE id = ?`, past, mem); err != nil {
				t.Fatal(err)
			}
			if err := ResetForRestore(context.Background(), a.db, tc.prefer); err != nil {
				t.Fatal(err)
			}
			a.reconcile()
			if a.memoryBody(mem) != tc.wantBody || a.readDoc("memories", mem)["body"] != tc.wantBody {
				t.Fatalf("prefer %s: db=%q file=%v", tc.prefer, a.memoryBody(mem), a.readDoc("memories", mem)["body"])
			}
			if v, ok, _ := getMeta(context.Background(), a.db, metaAdoptPrefer); ok {
				t.Fatalf("prefer flag not cleared: %q", v)
			}
		})
	}
}

func TestAdopt_PurgedIDsAreNotResurrected(t *testing.T) {
	t.Parallel()
	a, b := teamOf(t)
	stale := b.addMemory("purged on main")
	a.copyDataTo(b)
	if err := writePurged(b.data, map[string]bool{stale: true}); err != nil {
		t.Fatal(err)
	}
	rep := b.reconcile()
	if b.memoryExists(stale) {
		t.Fatal("purged id resurrected")
	}
	if _, err := os.Stat(b.file("memories", stale)); !os.IsNotExist(err) {
		t.Fatal("purged id exported")
	}
	if !contains(rep.Deleted, "memories/"+stale+".json") {
		t.Fatalf("purge not reported: %+v", rep)
	}
}

func TestAdopt_BrokenMetaFails(t *testing.T) {
	t.Parallel()
	a, b := teamOf(t)
	a.copyDataTo(b)
	if err := os.WriteFile(filepath.Join(b.data, MetaFileName), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Reconcile(context.Background(), b.opts()); err == nil {
		t.Fatal("adopt with an unreadable _meta.json must fail loudly")
	}
}

func TestFixProjects_CollapsesParallelBootstraps(t *testing.T) {
	t.Parallel()
	a, b := teamOf(t)
	bMem := b.addMemory("from B")
	b.reconcile() // B bootstrapped in parallel: its own project file + meta
	// Simulate the merge of both branches: B's data dir gets A's files too
	// (A's _meta.json wins the textual merge).
	if err := filepath.WalkDir(a.data, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(a.data, p)
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		dst := filepath.Join(b.data, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	}); err != nil {
		t.Fatal(err)
	}
	rep := b.reconcile()
	if !hasPrefixIn(rep.Warnings, "2 project files") {
		t.Fatalf("parallel bootstrap not flagged: %v", rep.Warnings)
	}
	keep, merged, err := FixProjects(context.Background(), b.db, b.data, "")
	if err != nil || keep != a.project || len(merged) != 1 {
		t.Fatalf("fix = %s %v %v", keep, merged, err)
	}
	rep = b.reconcile()
	if CountRowFiles(b.data, projectsTable) != 1 {
		t.Fatalf("still multiple project files: %+v", rep)
	}
	if b.readDoc("memories", bMem)["project_id"] != a.project {
		t.Fatal("rows not re-pointed")
	}
	if k, m, err := FixProjects(context.Background(), b.db, b.data, ""); err != nil || k != "" || m != nil {
		t.Fatal("second fix must be a no-op")
	}
}

func TestFixProjects_UnknownKeep(t *testing.T) {
	t.Parallel()
	a, b := teamOf(t)
	b.addMemory("x")
	if _, err := b.client.Project.Create().SetName("second").Save(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = a
	if _, _, err := FixProjects(context.Background(), b.db, b.data, "prj_00000000000070008000000000000009"); err == nil {
		t.Fatal("unknown keep id must error")
	}
}

func TestStatus(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	st, err := ReadStatus(ctx, e.db, e.data)
	if err != nil || st.Baselined || st.DataDirPresent {
		t.Fatalf("pre-bootstrap status = %+v %v", st, err)
	}
	e.addMemory("x")
	e.reconcile()
	e.addMemory("pending")
	st, err = ReadStatus(ctx, e.db, e.data)
	if err != nil || !st.Baselined || !st.DataDirPresent || st.PendingExports != 1 || st.TrackedFiles < 3 {
		t.Fatalf("status = %+v %v", st, err)
	}
}

func TestRestoreTrashUnknownID(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.reconcile()
	if err := RestoreTrash(context.Background(), e.db, 999); err == nil {
		t.Fatal("unknown trash id must error")
	}
}

// Review finding: a restore preferring the DB must roll back rows that were
// added after the backup (they exist only in the files), and a restore
// preferring the files must drop rows that exist only in the DB.
func TestAdopt_PreferIsARollback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// prefer db: file-only row is removed
	a := newEnv(t)
	keep := a.addMemory("in backup")
	a.reconcile()
	junk := a.addMemory("added after backup")
	a.reconcile()
	if _, err := a.db.Exec(`DELETE FROM memories WHERE id = ?`, junk); err != nil {
		t.Fatal(err) // the restored DB does not have it
	}
	if _, err := a.db.Exec(`DELETE FROM _lore_sync_dirty`); err != nil {
		t.Fatal(err)
	}
	if err := ResetForRestore(ctx, a.db, PreferDB); err != nil {
		t.Fatal(err)
	}
	a.reconcile()
	if a.memoryExists(junk) {
		t.Fatal("prefer db re-imported a row the restored DB does not have")
	}
	if _, err := os.Stat(a.file("memories", junk)); !os.IsNotExist(err) {
		t.Fatal("prefer db must remove the file of a row not in the restored DB")
	}
	if !a.memoryExists(keep) {
		t.Fatal("row present on both sides must survive")
	}
	// prefer files: DB-only row goes to the trash
	b := newEnv(t)
	b.reconcile()
	extra := b.addMemory("only in db")
	if _, err := b.db.Exec(`DELETE FROM _lore_sync_dirty`); err != nil {
		t.Fatal(err)
	}
	if err := ResetForRestore(ctx, b.db, PreferFiles); err != nil {
		t.Fatal(err)
	}
	b.reconcile()
	if b.memoryExists(extra) {
		t.Fatal("prefer files kept a row that has no file")
	}
	if tr, _ := ListTrash(ctx, b.db); len(tr) != 1 || tr[0].RowID != extra {
		t.Fatalf("row must be recoverable from the trash: %+v", tr)
	}
}
