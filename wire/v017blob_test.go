package wire

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestV017BlobCellsAreRead (devco N-5): lore 0.1.17 left some text and JSON
// cells of lore.db as BLOBs. SQLite's json_object refuses a BLOB ("JSON
// cannot hold BLOB values"), so every read of such a lore.db failed: lore
// fix's 0.1.17 files step kept the old database forever with an E_INTERNAL
// SQLite text and no next step. devco:M-1 fixed the per-column reads
// (edits, machine copy); the export the rows check runs (v017DocQuery)
// reads a BLOB cell as the text it holds too.
func TestV017BlobCellsAreRead(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "lore.db")
	v017SharedDB(t, path)
	db, err := sql.Open(sqliteDriver, fileuri.URL(path, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }() // test fixture
	if _, err = db.Exec(`UPDATE tasks SET body = CAST('ship it on monday' AS BLOB), blocked_by = CAST('["tsk_01a1af104f597542ba6533344ceb3cb4"]' AS BLOB) WHERE id = ?`, v17TaskA); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err = db.QueryRow(`SELECT typeof(blocked_by) FROM tasks WHERE id = ?`, v17TaskA).Scan(&kind); err != nil || kind != "blob" {
		t.Fatalf("fixture: blocked_by is %q (%v), want a blob", kind, err)
	}

	var cell string
	if err = db.QueryRow(`SELECT json_object('body', `+v017CellText("body")+`, 'blocked_by', `+v017CellText("blocked_by")+`) FROM tasks WHERE id = ?`, v17TaskA).Scan(&cell); err != nil {
		t.Fatalf("a column read of a BLOB cell fails: %v", err)
	}
	if !strings.Contains(cell, `"body":"ship it on monday"`) {
		t.Errorf("the BLOB body did not read as its text: %s", cell)
	}

	exp, err := V017ModeB{}.ExportRepoDB(context.Background(), path, v17ProjA)
	if err != nil {
		t.Fatalf("lore 0.1.17's database with BLOB cells cannot be read: %v", err)
	}
	task, ok := exportDocs(t, exp)[v17TaskA]
	if !ok {
		t.Fatal("the task with BLOB cells is missing from the export")
	}
	var body string
	if err = json.Unmarshal(task["body"], &body); err != nil || body != "ship it on monday" {
		t.Errorf("body = %s (%v), want the BLOB's text", task["body"], err)
	}
	if !strings.Contains(string(task["blocked_by"]), "tsk_01a1af104f597542ba6533344ceb3cb4") {
		t.Errorf("blocked_by = %s, want the BLOB's JSON list", task["blocked_by"])
	}
}

// TestV017UnreadableIsPlain (devco N-5): when lore.db cannot be read, the
// 0.1.17 files step says why in plain words and names the next step: no
// error code, and the step counts as failed (never "--" followed by
// "Everything is current.").
func TestV017UnreadableIsPlain(t *testing.T) {
	t.Parallel()
	why := v017Unreadable(errInternalForTest())
	if strings.Contains(why, "[E_") {
		t.Errorf("the reason carries an error code: %s", why)
	}
	for _, want := range []string{"could not read", "lore fix --migrate", "delete .lore/lore.db and .lore/backups"} {
		if !strings.Contains(why, want) {
			t.Errorf("the reason lacks %q: %s", want, why)
		}
	}
}

// TestGitWiringStepNamesWrites (devco N-5): files the hooks wrote to the
// tracked wiring during a lore fix run are a changed step with the commit
// line, so the run never ends with "Everything is current."; none, no step.
func TestGitWiringStepNamesWrites(t *testing.T) {
	t.Parallel()
	if _, ok := gitWiringStep("/r", nil); ok {
		t.Fatal("a run that wrote no wiring file got a git wiring step")
	}
	st, ok := gitWiringStep("/r", []string{".gitignore", ".gitattributes"})
	if !ok || !st.Changed || st.Failed {
		t.Fatalf("step = %+v, want a changed step", st)
	}
	if !strings.Contains(st.Next, "git add .gitignore .gitattributes") {
		t.Errorf("Next does not say how to commit them: %q", st.Next)
	}
	if !strings.Contains(st.Result, "(lore's local files git must ignore and the merge rules)") {
		t.Errorf("Result does not say what each file's lines are for: %q", st.Result)
	}
	// devco:D-772: a run that wrote only .gitattributes says it wrote the
	// merge rules, never the ignore lines it did not write.
	only, _ := gitWiringStep("/r", []string{".gitattributes"})
	if want := "wrote lore's lines to .gitattributes (the merge rules)"; only.Result != want {
		t.Errorf("Result = %q, want %q", only.Result, want)
	}
	var hw *HostWiring
	hw.Note(".gitignore") // nil-safe: a run without the host's hooks
	if hw.Wrote() != nil {
		t.Error("a nil HostWiring reports writes")
	}
}

// errInternalForTest is the shape of the failure the step met: an
// E_INTERNAL wrapping SQLite's text.
func errInternalForTest() error {
	return errs.Wrap(errs.Internal, "migrate: read tasks", errors.New("SQL logic error: JSON cannot hold BLOB values (1)"))
}
