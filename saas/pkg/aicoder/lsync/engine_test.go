package lsync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"saas/pkg/aicoder/merge3"
)

func TestBootstrap_ExportsSyncedRowsAndPinsProject(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	mem := e.addMemory("use JWT")
	if _, err := e.db.Exec(`INSERT INTO query_logs(id, created_at, updated_at, project_id, command, query_text, result_count, latency_ms)
		VALUES ('qry_01a10237c9e87e4c843d8045973a65a1', datetime('now'), datetime('now'), ?, 'search', 'x', 0, 1)`, e.project); err != nil {
		t.Fatal(err)
	}
	rep := e.reconcile()
	if rep.Pass != PassBootstrap {
		t.Fatalf("pass = %s", rep.Pass)
	}
	for _, rel := range []string{"memories/" + mem + ".json", "projects/" + e.project + ".json", "actors/" + e.actor + ".json"} {
		if !contains(rep.Exported, rel) {
			t.Fatalf("%s not exported: %v", rel, rep.Exported)
		}
	}
	if _, err := os.Stat(filepath.Join(e.data, "query_logs")); !os.IsNotExist(err) {
		t.Fatal("local telemetry table must never be exported")
	}
	meta, err := ReadProjectMeta(e.data)
	if err != nil || meta.ProjectID != e.project {
		t.Fatalf("meta = %+v err %v", meta, err)
	}
	doc := e.readDoc("memories", mem)
	if doc["body"] != "use JWT" || doc[keyTable] != "memories" {
		t.Fatalf("doc = %v", doc)
	}
	for _, vol := range []string{"last_accessed_at", "embedding", "embedding_dim"} {
		if _, has := doc[vol]; has {
			t.Fatalf("volatile column %s leaked into file", vol)
		}
	}
	if s, _ := doc["created_at"].(string); len(s) != len(timeLayout) || !strings.HasSuffix(s, "Z") {
		t.Fatalf("timestamp not canonical: %q", s)
	}
	// Run twice: nothing changes the second time.
	if again := e.reconcile(); again.Changed() || again.Pass != PassNormal {
		t.Fatalf("second pass changed things: %+v", again)
	}
}

func TestBootstrap_SkippedInReadOnly(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	rep := e.reconcile(func(o *Options) { o.ReadOnly = true })
	if rep.Pass != PassSkipped || MetaExists(e.data) {
		t.Fatalf("read-only must not bootstrap: %+v", rep)
	}
}

func TestBootstrap_BacksUpFirst(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	called := false
	rep := e.reconcile(func(o *Options) {
		o.Backup = func(context.Context) (string, error) { called = true; return "/tmp/x.sqlite", nil }
	})
	if !called || rep.BackupPath != "/tmp/x.sqlite" {
		t.Fatalf("backup not taken: %+v", rep)
	}
}

func TestBootstrap_BackupFailureAborts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, err := Reconcile(context.Background(), e.opts(func(o *Options) {
		o.Backup = func(context.Context) (string, error) { return "", os.ErrPermission }
	}))
	if err == nil || MetaExists(e.data) {
		t.Fatalf("bootstrap must abort without a backup: err=%v", err)
	}
}

func TestExport_EntWriteRawSQLAndDelete(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.reconcile()

	// ent write
	mem := e.addMemory("first")
	rep := e.reconcile()
	if !contains(rep.Exported, "memories/"+mem+".json") {
		t.Fatalf("ent insert not exported: %+v", rep)
	}
	// raw SQL write, bypassing ent (E42)
	if _, err := e.db.Exec(`UPDATE memories SET body = 'edited by sqlite3' WHERE id = ?`, mem); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	if e.readDoc("memories", mem)["body"] != "edited by sqlite3" {
		t.Fatal("raw SQL update not exported")
	}
	// delete
	if _, err := e.db.Exec(`DELETE FROM memories WHERE id = ?`, mem); err != nil {
		t.Fatal(err)
	}
	rep = e.reconcile()
	if !contains(rep.Removed, "memories/"+mem+".json") {
		t.Fatalf("delete not propagated: %+v", rep)
	}
	if _, err := os.Stat(e.file("memories", mem)); !os.IsNotExist(err) {
		t.Fatal("file still exists")
	}
}

func TestExport_VolatileOnlyUpdateDoesNotChurn(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	mem := e.addMemory("x")
	e.reconcile()
	before, _ := os.ReadFile(e.file("memories", mem))
	// ent bumps updated_at on every update; only a volatile column changes.
	if err := e.client.Memory.UpdateOneID(mem).SetLastAccessedAt(time.Now()).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	rep := e.reconcile()
	after, _ := os.ReadFile(e.file("memories", mem))
	if rep.Changed() || string(before) != string(after) {
		t.Fatalf("volatile update rewrote the file: %+v", rep)
	}
	if rep.PendingExports != 0 {
		t.Fatalf("dirty flag not cleared: %d", rep.PendingExports)
	}
}

func TestImport_EditAddDeleteFromFiles(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	mem := e.addMemory("original")
	e.reconcile()

	e.editFile("memories", mem, func(d map[string]any) { d["body"] = "from teammate" })
	rep := e.reconcile()
	if !contains(rep.Imported, "memories/"+mem+".json") || e.memoryBody(mem) != "from teammate" {
		t.Fatalf("edit not imported: %+v body=%q", rep, e.memoryBody(mem))
	}

	// a brand new file from a teammate
	doc := e.readDoc("memories", mem)
	newID := "mem_01a10237c9e87e4c843d8045973a65b2"
	doc["id"] = newID
	doc["body"] = "new from teammate"
	e.writeDoc("memories", newID, doc)
	e.reconcile()
	if e.memoryBody(newID) != "new from teammate" {
		t.Fatal("new file not imported")
	}
	// ent can read every column back (timestamps parse, enums valid)
	m, err := e.client.Memory.Get(context.Background(), newID)
	if err != nil || m.CreatedAt.IsZero() || m.Kind == "" {
		t.Fatalf("ent read-back failed: %+v %v", m, err)
	}

	// file deleted → row deleted, kept in trash
	if err := os.Remove(e.file("memories", newID)); err != nil {
		t.Fatal(err)
	}
	rep = e.reconcile()
	if e.memoryExists(newID) || !contains(rep.Deleted, "memories/"+newID+".json") {
		t.Fatalf("deletion not imported: %+v", rep)
	}
	trash, err := ListTrash(context.Background(), e.db)
	if err != nil || len(trash) != 1 || trash[0].RowID != newID {
		t.Fatalf("trash = %+v err %v", trash, err)
	}
	if err := RestoreTrash(context.Background(), e.db, trash[0].ID); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	if !e.memoryExists(newID) {
		t.Fatal("restore did not bring the row back")
	}
	if _, err := os.Stat(e.file("memories", newID)); err != nil {
		t.Fatal("restored row not re-exported")
	}
}

func TestBothChanged_DifferentFieldsMerge(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	tsk, err := e.client.Task.Create().SetProjectID(e.project).SetTitle("t").SetBody("b").Save(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	// DB side changes status; file side changes title.
	if _, err := e.db.Exec(`UPDATE tasks SET status = 'in_progress' WHERE id = ?`, tsk.ID); err != nil {
		t.Fatal(err)
	}
	e.editFile("tasks", tsk.ID, func(d map[string]any) { d["title"] = "renamed by teammate" })
	rep := e.reconcile()
	if len(rep.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", rep.Conflicts)
	}
	got, err := e.client.Task.Get(context.Background(), tsk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "renamed by teammate" || string(got.Status) != "in_progress" {
		t.Fatalf("merge lost a side: title=%q status=%q", got.Title, got.Status)
	}
	doc := e.readDoc("tasks", tsk.ID)
	if doc["title"] != "renamed by teammate" || doc["status"] != "in_progress" {
		t.Fatalf("file not merged: %v", doc)
	}
}

func TestBothChanged_SameTextFileWinsWithConflictCopy(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	mem := e.addMemory("base")
	e.reconcile()
	if _, err := e.db.Exec(`UPDATE memories SET body = 'local edit' WHERE id = ?`, mem); err != nil {
		t.Fatal(err)
	}
	e.editFile("memories", mem, func(d map[string]any) { d["body"] = "team edit" })
	rep := e.reconcile()
	if len(rep.Conflicts) != 1 || rep.Conflicts[0].Kept != "file" {
		t.Fatalf("conflicts = %+v", rep.Conflicts)
	}
	if e.memoryBody(mem) != "team edit" {
		t.Fatal("file must win a text clash")
	}
	cs, err := ListConflicts(context.Background(), e.db, false)
	if err != nil || len(cs) != 1 || !strings.Contains(cs[0].OtherDoc, "local edit") {
		t.Fatalf("conflict copy = %+v err %v", cs, err)
	}
	// Resolve by taking the local version: it flows back into the file.
	if err := ResolveConflict(context.Background(), e.db, cs[0].ID, TakeOther); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	if e.memoryBody(mem) != "local edit" || e.readDoc("memories", mem)["body"] != "local edit" {
		t.Fatal("resolve --take other did not apply")
	}
	if open, _ := ListConflicts(context.Background(), e.db, false); len(open) != 0 {
		t.Fatal("conflict still open")
	}
	if err := ResolveConflict(context.Background(), e.db, cs[0].ID, TakeKept); err == nil {
		t.Fatal("resolving twice must fail")
	}
}

func TestBothChanged_DeleteVersusEdit(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a := e.addMemory("a")
	b := e.addMemory("b")
	e.reconcile()
	// a: file deleted, row edited locally → keep edit, file comes back
	if err := os.Remove(e.file("memories", a)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE memories SET body = 'a2' WHERE id = ?`, a); err != nil {
		t.Fatal(err)
	}
	// b: row deleted locally, file edited → keep file, row comes back
	if _, err := e.db.Exec(`DELETE FROM memories WHERE id = ?`, b); err != nil {
		t.Fatal(err)
	}
	e.editFile("memories", b, func(d map[string]any) { d["body"] = "b2" })
	rep := e.reconcile()
	if len(rep.Conflicts) != 2 {
		t.Fatalf("conflicts = %+v", rep.Conflicts)
	}
	if e.readDoc("memories", a)["body"] != "a2" || e.memoryBody(b) != "b2" {
		t.Fatal("modify must beat delete on both sides")
	}
}

func TestInvalidFile_ReportedNeverImportedNeverOverwritten(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		content  string
		wantKind string
	}{
		{"broken json", "{not json", ErrKindInvalid},
		{"git conflict hunk", "<<<<<<< HEAD\n{}\n=======\n{}\n>>>>>>> feature\n", ErrKindConflict},
		{"newer format", `{"_v": 99, "_table": "memories"}`, ErrKindNewer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			mem := e.addMemory("keep me")
			e.reconcile()
			e.writeRaw("memories", mem, []byte(tc.content))
			// Also change the row locally: the export must NOT clobber the
			// half-resolved file.
			if _, err := e.db.Exec(`UPDATE memories SET body = 'local' WHERE id = ?`, mem); err != nil {
				t.Fatal(err)
			}
			rep := e.reconcile()
			if len(rep.Errors) == 0 || rep.Errors[0].Kind != tc.wantKind {
				t.Fatalf("errors = %+v", rep.Errors)
			}
			got, _ := os.ReadFile(e.file("memories", mem))
			if string(got) != tc.content {
				t.Fatal("unparseable file was overwritten")
			}
			if e.memoryBody(mem) != "local" {
				t.Fatal("DB must keep its own value")
			}
			if rep.PendingExports == 0 {
				t.Fatal("local change must stay pending until the file is fixed")
			}
			st, err := ReadStatus(context.Background(), e.db, e.data)
			if err != nil || len(st.Errors) != 1 {
				t.Fatalf("status = %+v err %v", st, err)
			}
		})
	}
}

func TestConflictMarkersInsideStringValue(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	mem := e.addMemory("x")
	e.reconcile()
	// The merge driver's output when both branches edited the same text: a
	// valid file whose other fields merged cleanly.
	e.editFile("memories", mem, func(d map[string]any) {
		d["body"] = merge3.MarkConflict("a", "b")
		d["confidence"] = 0.9
	})
	rep := e.reconcile()
	if len(rep.Errors) != 1 || rep.Errors[0].Kind != ErrKindConflict {
		t.Fatalf("unsettled text must be reported: %+v", rep)
	}
	if !merge3.ContainsConflictMarkers(e.memoryBody(mem)) || e.count(`SELECT COUNT(*) FROM memories WHERE id = ? AND confidence = 0.9`, mem) != 1 {
		t.Fatalf("file must be imported so cleanly merged fields land and both versions show: body=%q", e.memoryBody(mem))
	}
	// Still reported on the next pass (nothing changed, nothing settled).
	if n := e.count(`SELECT COUNT(*) FROM _lore_sync_errors WHERE path = ?`, "memories/"+mem+".json"); n != 1 {
		t.Fatalf("notice must persist until settled: %d", n)
	}
	// Settling it with a lore edit rewrites the file and clears the notice.
	if _, err := e.db.Exec(`UPDATE memories SET body = 'settled' WHERE id = ?`, mem); err != nil {
		t.Fatal(err)
	}
	rep = e.reconcile()
	if !contains(rep.Exported, "memories/"+mem+".json") || e.readDoc("memories", mem)["body"] != "settled" {
		t.Fatalf("settled edit not exported over the conflicted file: %+v", rep)
	}
	if n := e.count(`SELECT COUNT(*) FROM _lore_sync_errors`); n != 0 {
		t.Fatalf("notice not cleared after settling: %d", n)
	}
	if fmt.Sprint(e.readDoc("memories", mem)["confidence"]) != "0.9" {
		t.Fatalf("cleanly merged field lost when settling: %#v", e.readDoc("memories", mem)["confidence"])
	}
}

// Markers that break the JSON itself (a merge without lore's driver) stay
// blocked: the file is never overwritten and nothing is imported.
func TestRawConflictMarkersStayBlocked(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	mem := e.addMemory("x")
	e.reconcile()
	raw := "<<<<<<< ours\n{\"_v\":1}\n=======\n{\"_v\":1}\n>>>>>>> theirs\n"
	if err := os.WriteFile(e.file("memories", mem), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := e.reconcile()
	if len(rep.Errors) != 1 || rep.Errors[0].Kind != ErrKindConflict || e.memoryBody(mem) != "x" {
		t.Fatalf("raw markers must block: %+v", rep)
	}
	if _, err := e.db.Exec(`UPDATE memories SET body = 'y' WHERE id = ?`, mem); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	if got, _ := os.ReadFile(e.file("memories", mem)); string(got) != raw {
		t.Fatal("a file that cannot be read must never be overwritten")
	}
}

func TestFileIdentityMismatch(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	mem := e.addMemory("x")
	e.reconcile()
	e.editFile("memories", mem, func(d map[string]any) { d["id"] = "mem_01a10237c9e87e4c843d8045973a65ff" })
	if rep := e.reconcile(); len(rep.Errors) != 1 {
		t.Fatalf("id/name mismatch must be rejected: %+v", rep)
	}
	e.editFile("memories", mem, func(d map[string]any) { d["id"] = mem; d[keyTable] = "rules" })
	if rep := e.reconcile(); len(rep.Errors) != 1 {
		t.Fatalf("_table mismatch must be rejected: %+v", rep)
	}
}

func TestMissingDataDir_NeverDeletesRows(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	mem := e.addMemory("precious")
	e.reconcile()
	if err := os.RemoveAll(e.data); err != nil {
		t.Fatal(err)
	}
	rep := e.reconcile()
	if !e.memoryExists(mem) || len(rep.Warnings) == 0 {
		t.Fatalf("rows must survive a missing data dir: %+v", rep)
	}
	// export --all recreates everything
	if err := MarkAllForExport(context.Background(), e.db, e.data); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	if e.readDoc("memories", mem)["body"] != "precious" || !MetaExists(e.data) {
		t.Fatal("export --all did not recreate the data dir")
	}
}

func TestReadOnly_ImportsButNeverWrites(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	mem := e.addMemory("x")
	e.reconcile()
	e.editFile("memories", mem, func(d map[string]any) { d["body"] = "pulled" })
	other := e.addMemory("local only")
	rep := e.reconcile(func(o *Options) { o.ReadOnly = true })
	if e.memoryBody(mem) != "pulled" {
		t.Fatal("read-only must still refresh the cache")
	}
	if _, err := os.Stat(e.file("memories", other)); !os.IsNotExist(err) {
		t.Fatal("read-only wrote a file")
	}
	if rep.PendingExports == 0 {
		t.Fatal("local change must stay pending")
	}
	e.reconcile()
	if _, err := os.Stat(e.file("memories", other)); err != nil {
		t.Fatal("pending change not exported by the next writable pass")
	}
}

func TestSecretsBlockExport(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.reconcile()
	mem := e.addMemory("token AKIA-SECRET")
	check := func(o *Options) {
		o.CheckSecrets = func(b []byte) string {
			if strings.Contains(string(b), "AKIA") {
				return "aws key"
			}
			return ""
		}
	}
	rep := e.reconcile(check)
	if _, err := os.Stat(e.file("memories", mem)); !os.IsNotExist(err) {
		t.Fatal("secret was written to a git-tracked file")
	}
	if len(rep.Errors) != 1 || rep.Errors[0].Kind != ErrKindSecret || rep.PendingExports != 1 {
		t.Fatalf("secret refusal not reported: %+v", rep)
	}
	if _, err := e.db.Exec(`UPDATE memories SET body = 'clean' WHERE id = ?`, mem); err != nil {
		t.Fatal(err)
	}
	rep = e.reconcile(check)
	if len(rep.Errors) != 0 || e.readDoc("memories", mem)["body"] != "clean" {
		t.Fatalf("fixed row not exported: %+v", rep)
	}
}

func TestUnknownFieldsPreserved(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	mem := e.addMemory("x")
	e.reconcile()
	e.editFile("memories", mem, func(d map[string]any) { d["future_field"] = "from lore v9" })
	e.reconcile()
	// a local edit re-exports the row; the unknown field must survive
	if _, err := e.db.Exec(`UPDATE memories SET body = 'y' WHERE id = ?`, mem); err != nil {
		t.Fatal(err)
	}
	rep := e.reconcile()
	doc := e.readDoc("memories", mem)
	if doc["future_field"] != "from lore v9" || doc["body"] != "y" {
		t.Fatalf("unknown field lost: %v (%+v)", doc, rep)
	}
}

func TestOlderFileMissingColumnKeepsDBValue(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	mem := e.addMemory("x")
	if _, err := e.db.Exec(`UPDATE memories SET source_ref = 'kept' WHERE id = ?`, mem); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	e.editFile("memories", mem, func(d map[string]any) { delete(d, "source_ref"); d["body"] = "new" })
	e.reconcile()
	var ref string
	if err := e.db.QueryRow(`SELECT source_ref FROM memories WHERE id = ?`, mem).Scan(&ref); err != nil || ref != "kept" {
		t.Fatalf("column missing from an older file must not be cleared: %q %v", ref, err)
	}
}

func TestNaturalKeyCollisionMerges(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	local, err := e.client.Tag.Create().SetProjectID(e.project).SetName("auth").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mem := e.addMemory("tagged")
	if _, err := e.client.EntityTag.Create().SetEntityTable("memories").SetEntityID(mem).SetTagID(local.ID).Save(ctx); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	// A teammate created the same tag name with a different (smaller) id.
	doc := e.readDoc("tags", local.ID)
	remoteID := "tag_00000000000070008000000000000001"
	doc["id"] = remoteID
	e.writeDoc("tags", remoteID, doc)
	rep := e.reconcile()
	if len(rep.Merged) != 1 {
		t.Fatalf("expected a natural-key merge: %+v", rep)
	}
	if e.count(`SELECT COUNT(*) FROM tags WHERE name = 'auth'`) != 1 {
		t.Fatal("duplicate tag survived")
	}
	if e.count(`SELECT COUNT(*) FROM entity_tags WHERE tag_id = ?`, remoteID) != 1 {
		t.Fatal("binding not rewritten to the winning id")
	}
	if _, err := os.Stat(e.file("tags", local.ID)); !os.IsNotExist(err) {
		t.Fatal("loser tag file not removed")
	}
	again := e.reconcile()
	if again.Changed() {
		t.Fatalf("not converged: %+v", again)
	}
}

func TestNaturalKeyCollision_IncomingLoses(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	local, err := e.client.Tag.Create().SetProjectID(e.project).SetName("db").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	doc := e.readDoc("tags", local.ID)
	remoteID := "tag_ffffffffffff7fffbfffffffffffffff"
	doc["id"] = remoteID
	e.writeDoc("tags", remoteID, doc)
	e.reconcile()
	if e.count(`SELECT COUNT(*) FROM tags`) != 1 || e.count(`SELECT COUNT(*) FROM tags WHERE id = ?`, local.ID) != 1 {
		t.Fatal("smaller local id must win")
	}
	if _, err := os.Stat(e.file("tags", remoteID)); !os.IsNotExist(err) {
		t.Fatal("losing incoming file must be removed")
	}
}

func TestSymlinksAndOversizeIgnored(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.reconcile()
	target := filepath.Join(e.dir, "outside.txt")
	if err := os.WriteFile(target, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := e.file("memories", "mem_01a10237c9e87e4c843d8045973a65c3")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	big := "mem_01a10237c9e87e4c843d8045973a65c4"
	e.writeRaw("memories", big, []byte(strings.Repeat("x", MaxFileSize+1)))
	rep := e.reconcile()
	if !hasPrefixIn(rep.Warnings, "memories/mem_01a10237c9e87e4c843d8045973a65c3") {
		t.Fatalf("symlink not reported: %+v", rep.Warnings)
	}
	if len(rep.Errors) != 1 || rep.Errors[0].Path != "memories/"+big+".json" {
		t.Fatalf("oversize file not rejected: %+v", rep.Errors)
	}
	if b, _ := os.ReadFile(target); string(b) != "secret" {
		t.Fatal("symlink target modified")
	}
}

func TestStaleTmpRemoved(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.reconcile()
	tmp := filepath.Join(e.data, "memories", tmpPrefix+"deadbeef")
	if err := os.MkdirAll(filepath.Dir(tmp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(tmp, old, old); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatal("crash debris not cleaned")
	}
}

func TestCrashAfterFileWriteBeforeCommit(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	mem := e.addMemory("x")
	e.reconcile()
	// Simulate: a pass wrote the new file content, then crashed before the
	// DB transaction (base index) committed. DB and file now agree on the
	// content but the base is stale.
	if _, err := e.db.Exec(`UPDATE memories SET body = 'y' WHERE id = ?`, mem); err != nil {
		t.Fatal(err)
	}
	e.editFile("memories", mem, func(d map[string]any) { d["body"] = "y" })
	rep := e.reconcile()
	if len(rep.Conflicts) != 0 || e.memoryBody(mem) != "y" || rep.PendingExports != 0 {
		t.Fatalf("did not converge: %+v", rep)
	}
}

func TestLockTimeout(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	lockPath := filepath.Join(e.dir, "sync.lock")
	held, err := acquireLock(context.Background(), Options{LockPath: lockPath})
	if err != nil {
		t.Fatal(err)
	}
	defer held()
	_, err = Reconcile(context.Background(), e.opts(func(o *Options) { o.LockTimeout = 150 * time.Millisecond }))
	if err == nil || !strings.Contains(err.Error(), "sync lock") {
		t.Fatalf("expected lock timeout, got %v", err)
	}
}

func TestReconcileValidatesOptions(t *testing.T) {
	t.Parallel()
	if _, err := Reconcile(context.Background(), Options{}); err == nil {
		t.Fatal("missing DB must error")
	}
}

func TestPurgeArchived(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	old := e.addMemory("old archived")
	fresh := e.addMemory("fresh")
	if _, err := e.db.Exec(`UPDATE memories SET archived_at = ? WHERE id = ?`, time.Now().Add(-48*time.Hour), old); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	got, err := PurgeArchived(ctx, e.db, e.data, time.Now().Add(-24*time.Hour))
	if err != nil || len(got) != 1 || got[0] != "memories/"+old {
		t.Fatalf("purge = %v err %v", got, err)
	}
	e.reconcile()
	if _, err := os.Stat(e.file("memories", old)); !os.IsNotExist(err) {
		t.Fatal("purged file not removed")
	}
	if !e.memoryExists(fresh) {
		t.Fatal("unarchived row purged")
	}
	purged, err := readPurged(e.data)
	if err != nil || !purged[old] {
		t.Fatalf("_purged.json = %v err %v", purged, err)
	}
	if again, err := PurgeArchived(ctx, e.db, e.data, time.Now()); err != nil || len(again) != 0 {
		t.Fatalf("second purge = %v %v", again, err)
	}
}

// TestExportDoc_NeverOverwritesBlockedFile pins the second line of defence
// behind the deferred set: even if some path asks exportDoc to write a file
// that failed to parse in this pass, the bytes on disk stay untouched.
func TestExportDoc_NeverOverwritesBlockedFile(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	mem := e.addMemory("x")
	e.reconcile()
	e.writeRaw("memories", mem, []byte("half-resolved {"))
	ctx := context.Background()
	reg, _ := NewRegistry()
	present, err := existingColumns(ctx, e.db)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	rel := relPath("memories", mem)
	eng := &engine{o: e.opts(), reg: reg, present: present, tx: tx, now: time.Now().UTC(), rep: &Report{},
		bases: map[string]baseEntry{}, blocked: map[string]bool{rel: true}, deferred: map[rowKey]bool{}}
	tbl, _ := reg.Table("memories")
	doc, err := readRowDoc(ctx, tx, tbl, liveColumns(tbl, present), mem)
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.exportDoc(ctx, rowKey{"memories", mem}, tbl, doc); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(e.file("memories", mem)); string(b) != "half-resolved {" {
		t.Fatal("blocked file overwritten")
	}
	if !eng.deferred[rowKey{"memories", mem}] {
		t.Fatal("blocked export must defer the row")
	}
}

// A power loss after an un-fsynced export can leave a zero-length file;
// the next pass restores it from the DB instead of reporting it broken.
func TestZeroLengthFileRepairedFromDB(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	mem := e.addMemory("durable in the db")
	e.reconcile()
	e.writeRaw("memories", mem, nil)
	rep := e.reconcile()
	if len(rep.Errors) != 0 || e.readDoc("memories", mem)["body"] != "durable in the db" {
		t.Fatalf("not repaired: %+v", rep)
	}
	// Without a base (a file that never synced) an empty file is an error.
	ghost := "mem_01a10237c9e87e4c843d8045973a65d9"
	e.writeRaw("memories", ghost, nil)
	if rep := e.reconcile(); len(rep.Errors) != 1 {
		t.Fatalf("empty unknown file must be reported: %+v", rep)
	}
}

func TestDirtyOnlyPass(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a := e.addMemory("a")
	b := e.addMemory("b")
	e.reconcile()
	dirtyOnly := func(o *Options) { o.DirtyOnly = true }

	// A file edited by someone else is NOT seen by a dirty-only pass...
	e.editFile("memories", a, func(d map[string]any) { d["body"] = "edited in file" })
	if rep := e.reconcile(dirtyOnly); rep.Changed() {
		t.Fatalf("dirty-only must not walk the tree: %+v", rep)
	}
	// ...but a DB write is exported, and a concurrent edit of the SAME row's
	// file is merged rather than overwritten.
	if _, err := e.db.Exec(`UPDATE memories SET source_ref = 'db side' WHERE id = ?`, b); err != nil {
		t.Fatal(err)
	}
	e.editFile("memories", b, func(d map[string]any) { d["body"] = "file side" })
	e.reconcile(dirtyOnly)
	doc := e.readDoc("memories", b)
	if doc["body"] != "file side" || doc["source_ref"] != "db side" {
		t.Fatalf("dirty-only lost a side: %v", doc)
	}
	// New row with no file yet is exported.
	c := e.addMemory("c")
	e.reconcile(dirtyOnly)
	if e.readDoc("memories", c)["body"] != "c" {
		t.Fatal("new row not exported")
	}
	// The next full pass still catches the first edit.
	e.reconcile()
	if e.memoryBody(a) != "edited in file" {
		t.Fatal("full pass missed the file edit")
	}
}

// A natural-key merge must also fix references in rows imported LATER in
// the same pass: tables sync in name order, so "actors" merges before the
// "memories" file that still names the losing actor id is imported.
func TestNaturalKeyMerge_RewritesRowsImportedLater(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	// Template docs for a memory, then remove it again.
	mem := e.addMemory("teammate memory")
	e.reconcile()
	memDoc := e.readDoc("memories", mem)
	actorDoc := e.readDoc("actors", e.actor)
	if _, err := e.db.Exec(`DELETE FROM memories WHERE id = ?`, mem); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	// Both teammate files arrive in ONE pull: their actor row for the same
	// stable_key under a larger id, and a memory created by it.
	remoteActor := "act_ffffffffffff7fffbfffffffffffffff"
	actorDoc["id"] = remoteActor
	e.writeDoc("actors", remoteActor, actorDoc)
	remoteMem := "mem_01a10237c9e87e4c843d8045973a65e1"
	memDoc["id"] = remoteMem
	memDoc["created_by_actor_id"] = remoteActor
	e.writeDoc("memories", remoteMem, memDoc)
	rep := e.reconcile()
	if len(rep.Merged) == 0 {
		t.Fatalf("expected an actor merge: %+v", rep)
	}
	var by string
	if err := e.db.QueryRow(`SELECT created_by_actor_id FROM memories WHERE id = ?`, remoteMem).Scan(&by); err != nil {
		t.Fatal(err)
	}
	if by != e.actor {
		t.Fatalf("memory still references the merged-away actor: %s (want %s)", by, e.actor)
	}
	if e.readDoc("memories", remoteMem)["created_by_actor_id"] != e.actor {
		t.Fatal("file not re-exported with the winning actor id")
	}
	if refs, _ := DanglingReferences(context.Background(), e.db); len(refs) != 0 {
		t.Fatalf("dangling: %+v", refs)
	}
}
