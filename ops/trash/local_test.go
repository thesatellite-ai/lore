package trash_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/trash"
)

// plantSyncTrash writes a v0.1.17 lore.db holding the given safety copies
// (table, row id, reason) into the harness repo, as v0.1.17's lsync left
// it (saas/pkg/aicoder/lsync/infra.go).
func plantSyncTrash(t *testing.T, h *opstest.H, copies [][3]string) {
	t.Helper()
	dir := filepath.Join(h.Repo, ".lore")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "lore.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE _lore_sync_trash (id INTEGER PRIMARY KEY AUTOINCREMENT, table_name TEXT NOT NULL, row_id TEXT NOT NULL, doc TEXT NOT NULL, reason TEXT NOT NULL, at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, c := range copies {
		if _, err := db.Exec(`INSERT INTO _lore_sync_trash (table_name, row_id, doc, reason, at) VALUES (?, ?, '{"body":"sk-secret-doc"}', ?, '2026-10-01T10:00:00Z')`, c[0], c[1], c[2]); err != nil {
			t.Fatal(err)
		}
	}
}

// TestTrashLocalList: --local lists this machine's v0.1.17 safety copies
// newest first, never their document; no lore.db (or no table) is an empty
// list, and --local does not combine with --from-history / --overruled
// (CLI_REFERENCE 3.9, DQ1, P3-TRASHLOCAL-1).
func TestTrashLocalList(t *testing.T) {
	h := opstest.New(t)
	if out := listOf(t, h, `{"local":true}`); !out.Local || out.Count != 0 || len(out.Copies) != 0 {
		t.Fatalf("no lore.db: %+v", out)
	}
	if got := opstest.Text(h.Must(trash.OpList, `{"local":true}`)); got != "· trash is empty\n" {
		t.Fatalf("empty text: %q", got)
	}
	plantSyncTrash(t, h, [][3]string{{"memories", "mem_a", "file deleted"}, {"tasks", "tsk_b", "reverted"}})
	out := listOf(t, h, `{"local":true}`)
	if out.Count != 2 || out.Copies[0].N != 2 || out.Copies[0].RowID != "tsk_b" || out.Copies[1].Table != "memories" || len(out.Data) != 0 {
		t.Fatalf("copies: %+v", out)
	}
	if js := h.JSON(h.Must(trash.OpList, `{"local":true}`)); strings.Contains(js, "sk-secret-doc") || !strings.Contains(js, `"row_id":"tsk_b"`) {
		t.Fatalf("json: %s", js)
	}
	want := "#2 tasks/tsk_b  2026-10-01T10:00:00Z  (reverted)\n#1 memories/mem_a  2026-10-01T10:00:00Z  (file deleted)\n"
	if got := opstest.Text(h.Must(trash.OpList, `{"local":true}`)); got != want {
		t.Fatalf("text:\n%s\nwant:\n%s", got, want)
	}
	for _, in := range []string{`{"local":true,"from_history":true}`, `{"local":true,"overruled":true}`} {
		h.Fails(trash.OpList, in, errs.InvalidInput)
	}
	// The row trash itself is unchanged by the copies.
	if out := listOf(t, h, `{}`); out.Local || len(out.Copies) != 0 {
		t.Fatalf("plain list: %+v", out)
	}
}

// TestTrashLocalRestore: restore --local <n> finds the copy and refuses
// with the git restore hint (every table is owned by the row files, as
// v0.1.17's ErrCoreOwned), an unknown number is E_NOT_FOUND and a
// non-number E_INVALID_INPUT; nothing is written.
func TestTrashLocalRestore(t *testing.T) {
	h := opstest.New(t)
	plantSyncTrash(t, h, [][3]string{{"memories", "mem_a", "file deleted"}})
	cases := []struct {
		target string
		code   errs.Code
		want   string
	}{
		{"1", errs.InvalidInput, "git checkout HEAD -- .lore/data/memories/mem_a.json"},
		{"#1", errs.InvalidInput, "git log --diff-filter=D -- .lore/data/memories/mem_a.json"},
		{"7", errs.NotFound, "lore trash list --local"},
		{"x", errs.InvalidInput, "not a copy number"},
	}
	for _, c := range cases {
		in := `{"target":` + opstest.Quote(c.target) + `,"local":true}`
		h.Fails(trash.OpRestore, in, c.code)
		_, err := h.Run(trash.OpRestore, in)
		env := errs.EnvelopeOf(err)
		if got := env.Error.Message + " " + env.Error.Hint; !strings.Contains(got, c.want) {
			t.Fatalf("%s: %s", c.target, got)
		}
	}
}
