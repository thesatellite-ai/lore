package wire

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestV017EditsReadsBlobCells pins devco:M-1: a 0.1.18-dev build wrote
// tasks.blocked_by into the checkout's git-ignored lore.db as a BLOB
// holding JSON text, and json_object refuses a BLOB, so lore migrate run
// failed with a bare "E_INTERNAL read .lore/lore.db" while lore doctor
// pointed back at the same command. The BLOB cell is now read as its text
// (the edit is found, blocked_by and all), and a read that still fails
// names the table, SQLite's reason and a step other than the failed run.
func TestV017EditsReadsBlobCells(t *testing.T) {
	t.Parallel()
	ctx := background()
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withProject(t, a)
	fields := map[string]value.Value{}
	var err error
	for k, v := range map[string]string{"project_id": projectIn(t, a, store.SourceRepo), "title": "ship the dock", "status": "todo", "priority": "medium", "commitment": "accepted"} {
		if fields[k], err = value.TextOf(v); err != nil {
			t.Fatal(err)
		}
	}
	if fields["blocked_by"], err = value.JSONOf([]byte(`[]`)); err != nil {
		t.Fatal(err)
	}
	w, err := a.Create(ctx, store.SourceRepo, schema.TableTasks, fields)
	if err != nil {
		t.Fatal(err)
	}
	id := w.Row.ID.String()
	db, err := sql.Open(sqliteDriver, fileuri.URL(filepath.Join(repo, ".lore", "lore.db"), ""))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }() // test fixture
	const blocker = "tsk_01a1af104f597542ba6533344ceb3cb4"
	for _, q := range []string{
		`CREATE TABLE tasks (id TEXT PRIMARY KEY, updated_at datetime NOT NULL, title TEXT NOT NULL, blocked_by json)`,
		// the 0.1.18-dev shape: the same JSON text, stored as a BLOB, and
		// an update time after the row file's
		`INSERT INTO tasks VALUES ('` + id + `', '2099-01-02 03:04:05.000000000 +0000 UTC', 'ship the dock', CAST('[{"task_id":"` + blocker + `","until":"done"}]' AS BLOB))`,
	} {
		if _, err = db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	edits, err := a.v017Edits(ctx)
	if err != nil {
		t.Fatalf("v017Edits over a BLOB cell: %v", err)
	}
	if len(edits) != 1 || edits[0].ref.ID.String() != id {
		t.Fatalf("edits = %+v, want the one edit of %s", edits, id)
	}
	if bb, ok := edits[0].patch["blocked_by"]; !ok || !strings.Contains(bb.String(), blocker) {
		t.Fatalf("blocked_by of the edit = %v (%v), want the BLOB's JSON naming %s", bb, ok, blocker)
	}

	e := v017ReadError("tasks", errors.New("SQL logic error"))
	if !strings.Contains(e.Message, "tasks table") || !strings.Contains(e.Message, "SQL logic error") || !strings.HasPrefix(e.Hint, "the team's rows are in .lore/data") || !strings.Contains(e.Hint, "mv .lore/lore.db") {
		t.Fatalf("a lore.db read error names no table, cause or next step: %q / %q", e.Message, e.Hint)
	}
}
