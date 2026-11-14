package task_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestImportWritesOneBatch (D-214): task import stages every record that
// is not part of a task tree and writes them as ONE store batch (one live
// commit, see journey TestJourneyImportOneCommit), where each record was
// once its own commit (2,000 rows: 19 minutes). So a record the engine
// refuses (here a secret in the last body) leaves no task at all. The
// tasks read as before: the file's status through the same transition
// (completed_at, close_reason), a dropped mission's note, and a --key
// repeated in the file adds one task.
func TestImportWritesOneBatch(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(false))
	before := dataFiles(t, h, schema.TableTasks)
	secret := "AKIA" + strings.Repeat("Q", 4) + "7XRT2MNB5KZ3" // built at run time
	bad := writeFile(t, "bad.csv", "id,title,body\nA-1,one,x\nA-2,two,y\nA-3,three,key "+secret+"\n")
	if _, err := runImport(h, `{"file":`+opstest.Quote(bad)+`}`, true); err == nil {
		t.Fatal("an import with a secret in its last body: want a refusal")
	}
	if got := dataFiles(t, h, schema.TableTasks) - before; got != 0 {
		t.Fatalf("a refused import wrote %d tasks, want 0 (one batch, all or none)", got)
	}

	good := writeFile(t, "good.csv", "id,title,status,mission,key\n"+
		"B-1,shipped,done,,\n"+
		"B-2,dropped,cancelled,,\n"+
		"B-3,lost mission,todo,msn_01a1da59a1d87771a5d16e3e44f31267,\n"+
		"B-4,keyed,,,k1\n"+
		"B-5,keyed again,,,k1\n")
	res, err := runImport(h, `{"file":`+opstest.Quote(good)+`}`, true)
	if err != nil {
		t.Fatal(err)
	}
	got := importIDs(h, res)
	if len(got) != 5 || got[3] != got[4] {
		t.Fatalf("import ids %v: want 5, the two keyed rows on one task", got)
	}
	_, done := get(h, schema.TableTasks, got[0])
	if field(done, "status") != "done" || field(done, "completed_at") == "" || field(done, "close_reason") != "completed" {
		t.Errorf("done row: status %q completed_at %q close_reason %q", field(done, "status"), field(done, "completed_at"), field(done, "close_reason"))
	}
	_, cx := get(h, schema.TableTasks, got[1])
	if field(cx, "status") != "cancelled" || field(cx, "close_reason") != "cancelled" {
		t.Errorf("cancelled row: status %q close_reason %q", field(cx, "status"), field(cx, "close_reason"))
	}
	if n := dataFiles(t, h, schema.TableTasks) - before; n != 4 {
		t.Errorf("import wrote %d tasks, want 4", n)
	}
	notes := 0
	rows, err := h.App.List(h.T.Context(), store.SourceRepo, schema.TableComments)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if field(r, schema.FieldEntityID) == got[2] && strings.Contains(field(r, "body"), "msn_") {
			notes++
		}
	}
	if notes != 1 {
		t.Errorf("the row that lost its mission has %d notes, want 1", notes)
	}
}
