package task_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// importRun runs task.import of file (with extra JSON input fields) and
// decodes its output.
func importRun(t *testing.T, h *opstest.H, file, extra string) (opstask.ImportOut, error) {
	t.Helper()
	res, err := runImport(h, `{"file":`+opstest.Quote(file)+extra+`}`, true)
	if err != nil {
		return opstask.ImportOut{}, err
	}
	var out opstask.ImportOut
	if err = json.Unmarshal([]byte(h.JSON(res)), &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

// mustImport is importRun that fails the test on an error.
func mustImport(t *testing.T, h *opstest.H, file, extra string) opstask.ImportOut {
	t.Helper()
	out, err := importRun(t, h, file, extra)
	if err != nil {
		t.Fatalf("import %s: %v", file, err)
	}
	return out
}

// rowByExt is the import row of ext.
func rowByExt(t *testing.T, out opstask.ImportOut, ext string) opstask.ImportRow {
	t.Helper()
	for _, r := range out.Rows {
		if r.ExtID == ext {
			return r
		}
	}
	t.Fatalf("no row %s in %+v", ext, out.Rows)
	return opstask.ImportRow{}
}

// taskCount is how many tasks every attached store lists.
func taskCount(h *opstest.H) int {
	return strings.Count(h.JSON(h.Must(opstask.OpList, `{"all":true}`)), `"id":"tsk_`)
}

// TestImportDedupeKeys (G-TASK-3, TSQ8): the dedupe key is (source,
// external id) in the target store: the same ids imported from CSV and
// then JSON are two imports; a second CSV import adds nothing and lists
// every row as existing with the first ids; an import into the org store
// does not match the repo's rows and the other way round.
func TestImportDedupeKeys(t *testing.T) {
	h := opstest.New(t, repoClient(false), withOrg(t))
	csvFile := writeFile(t, "t.csv", "id,title\nK-1,One\nK-2,Two\n")
	jsonFile := writeFile(t, "t.json", `[{"id":"K-1","title":"One"},{"id":"K-2","title":"Two"}]`)

	first := mustImport(t, h, csvFile, "")
	if first.Count != 2 || first.Rows[0].Existing || first.Rows[1].Existing {
		t.Fatalf("first csv import: %+v", first)
	}
	if again := mustImport(t, h, csvFile, ""); rowByExt(t, again, "K-1").ID != rowByExt(t, first, "K-1").ID ||
		!rowByExt(t, again, "K-2").Existing || again.Batch != "" {
		t.Fatalf("second csv import: %+v", again)
	}
	if js := mustImport(t, h, jsonFile, ""); rowByExt(t, js, "K-1").Existing {
		t.Fatalf("json source matched the csv import: %+v", js)
	}
	if n := taskCount(h); n != 4 {
		t.Fatalf("after csv + json: %d tasks, want 4", n)
	}
	org := mustImport(t, h, csvFile, `,"to":"`+orgStore+`"`)
	if rowByExt(t, org, "K-1").Existing {
		t.Fatalf("the org store matched the repo's import: %+v", org)
	}
	if src, _ := get(h, schema.TableTasks, rowByExt(t, org, "K-1").ID); string(src) != orgStore {
		t.Fatalf("org import landed in %s", src)
	}
	if orgAgain := mustImport(t, h, csvFile, `,"to":"`+orgStore+`"`); !rowByExt(t, orgAgain, "K-1").Existing ||
		rowByExt(t, orgAgain, "K-1").ID != rowByExt(t, org, "K-1").ID {
		t.Fatalf("second org import: %+v", orgAgain)
	}
	if n := taskCount(h); n != 6 {
		t.Fatalf("final: %d tasks, want 6", n)
	}
}

// TestImportDiffersPerField: a re-import whose record changed one field
// names exactly that field as differing and writes nothing; an unchanged
// record and a record without the column name none.
func TestImportDiffersPerField(t *testing.T) {
	base := map[string]string{"title": "T", "body": "B", "priority": "low", "commitment": "accepted", "status": "in_progress", "estimate": "3"}
	enc := func(m map[string]string) string {
		b, err := json.Marshal([]map[string]string{m})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	with := func(k, v string) map[string]string {
		m := map[string]string{"id": "D-1"}
		for bk, bv := range base {
			m[bk] = bv
		}
		m[k] = v
		return m
	}
	changes := map[string]string{"title": "T2", "body": "B2", "priority": "high", "commitment": "someday", "status": "blocked", "estimate": "5"}
	for col, v := range changes {
		t.Run(col, func(t *testing.T) {
			h := opstest.New(t, repoClient(false))
			first := mustImport(t, h, writeFile(t, "d.json", enc(with("title", "T"))), "")
			id := first.Rows[0].ID
			_, before := get(h, schema.TableTasks, id)
			out := mustImport(t, h, writeFile(t, "d.json", enc(with(col, v))), "")
			if r := out.Rows[0]; !r.Existing || r.ID != id || !slices.Equal(r.Differs, []string{col}) {
				t.Fatalf("re-import with %s changed: %+v", col, r)
			}
			if _, after := get(h, schema.TableTasks, id); field(after, col) != field(before, col) {
				t.Fatalf("%s was written: %q -> %q", col, field(before, col), field(after, col))
			}
			if !strings.Contains(out.Text(), "differs, not written: "+col) {
				t.Fatalf("text: %s", out.Text())
			}
			same := mustImport(t, h, writeFile(t, "d.json", enc(with("title", "T"))), "")
			if len(same.Rows[0].Differs) != 0 {
				t.Fatalf("unchanged re-import differs: %+v", same.Rows[0])
			}
			bare := mustImport(t, h, writeFile(t, "d.json", `[{"id":"D-1","title":"T"}]`), "")
			if len(bare.Rows[0].Differs) != 0 {
				t.Fatalf("columns left out differ: %+v", bare.Rows[0])
			}
		})
	}
}

// TestImportStatusAndRefusal: statuses are set children first (a done
// parent over a done child, blocked, in_progress, todo and an empty
// status stay as written); a file whose done or cancelled task has an
// open sub-task, by nesting or by the parent column, is refused before
// any write; an unknown status is refused too.
func TestImportStatusAndRefusal(t *testing.T) {
	h := opstest.New(t, repoClient(false))
	out := mustImport(t, h, writeFile(t, "s.json", `[
		{"id":"P","title":"parent","status":"done","children":[{"id":"C","title":"child","status":"done"}]},
		{"id":"X","title":"blocked one","status":"blocked"},
		{"id":"Y","title":"started","status":"in_progress"},
		{"id":"Z","title":"todo","status":"todo"},
		{"id":"E","title":"no status"}]`), "")
	want := map[string]string{"P": "done", "C": "done", "X": "blocked", "Y": "in_progress", "Z": "todo", "E": "todo"}
	for ext, st := range want {
		if _, r := get(h, schema.TableTasks, rowByExt(t, out, ext).ID); field(r, "status") != st {
			t.Errorf("%s: status %q, want %s", ext, field(r, "status"), st)
		}
	}
	before := taskCount(h)
	bad := map[string]string{
		"done parent, open nested child":      `[{"id":"A","title":"a","status":"done","children":[{"id":"B","title":"b"}]}]`,
		"cancelled parent, in_progress child": `[{"id":"A","title":"a","status":"cancelled","children":[{"id":"B","title":"b","status":"in_progress"}]}]`,
		"done parent by column, later child":  `[{"id":"A","title":"a","status":"done"},{"id":"B","title":"b","parent":"A","status":"blocked"}]`,
		"unknown status":                      `[{"id":"A","title":"a","status":"finished"}]`,
	}
	for name, body := range bad {
		if _, err := importRun(t, h, writeFile(t, "bad.json", body), ""); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("%s: %v, want E_INVALID_INPUT", name, err)
		}
	}
	if n := taskCount(h); n != before {
		t.Fatalf("a refused import wrote %d tasks", n-before)
	}
}

// TestImportParentColumn: a parent column naming a record before or
// after it links like a nesting (parents are added first); a live task
// of the target is used; a trashed task, a task of another store, an
// unknown id and a value that is no id are dropped with
// W_IMPORT_DROPPED_REF; a cycle in the file is refused, nothing written;
// a re-import whose parent record already exists links the new child to
// the existing task.
func TestImportParentColumn(t *testing.T) {
	h := opstest.New(t, repoClient(false), withOrg(t))
	live := add(h, "live parent", "")
	trashed := add(h, "trashed parent", "")
	h.MustYes(opstask.OpDelete, `{"id":"`+trashed+`"}`)
	other := addID(h, h.Must(opstask.OpAdd, `{"title":"org parent","to":"`+orgStore+`"}`))
	csv := "id,title,parent\n" +
		"C1,child of later,P1\n" +
		"P1,later parent,\n" +
		"C2,child of earlier,P1\n" +
		"L,on a live task," + live + "\n" +
		"T,on a trashed task," + trashed + "\n" +
		"O,on another store," + other + "\n" +
		"U,on nothing,tsk_01936f0000007a1b8c2d3e4f5a6b7c8d\n" +
		"N,on not an id,not-an-id\n"
	out := mustImport(t, h, writeFile(t, "p.csv", csv), "")
	p1 := rowByExt(t, out, "P1").ID
	wantParent := map[string]string{"C1": p1, "C2": p1, "L": live, "T": "", "O": "", "U": "", "N": "", "P1": ""}
	for ext, want := range wantParent {
		if _, r := get(h, schema.TableTasks, rowByExt(t, out, ext).ID); field(r, schema.FieldTaskParent) != want {
			t.Errorf("%s: parent %q, want %q", ext, field(r, schema.FieldTaskParent), want)
		}
	}
	dropped := 0
	for _, w := range out.Warnings {
		if w.Code == opstask.WarnImportDroppedRef && strings.Contains(w.Message, "parent_id") {
			dropped++
		}
	}
	if dropped != 4 {
		t.Errorf("dropped parent warnings: %d, want 4 (%+v)", dropped, out.Warnings)
	}
	before := taskCount(h)
	if _, err := importRun(t, h, writeFile(t, "cycle.csv", "id,title,parent\nA,a,B\nB,b,A\n"), ""); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("cycle: %v", err)
	}
	if n := taskCount(h); n != before {
		t.Fatal("a refused cycle wrote tasks")
	}
	more := mustImport(t, h, writeFile(t, "p.csv", csv+"C3,new child,P1\n"), "")
	if c3 := rowByExt(t, more, "C3"); c3.Existing {
		t.Fatalf("C3 existing: %+v", c3)
	} else if _, r := get(h, schema.TableTasks, c3.ID); field(r, schema.FieldTaskParent) != p1 {
		t.Fatalf("new child under an existing parent: %q, want %s", field(r, schema.FieldTaskParent), p1)
	}
}

// TestImportEstimateColumn: the estimate column is read in the target's
// unit (points keep the number, hours read 90m as 1.5); a value the unit
// refuses ("2d", no day unit; "90m" under points) refuses the whole file
// before any write.
func TestImportEstimateColumn(t *testing.T) {
	h := opstest.New(t, repoClient(false))
	out := mustImport(t, h, writeFile(t, "e.csv", "id,title,estimate\nA,a,3\nB,b,0.5\nC,c,\n"), "")
	for ext, want := range map[string]string{"A": "3", "B": "0.5", "C": ""} {
		if _, r := get(h, schema.TableTasks, rowByExt(t, out, ext).ID); field(r, "estimate") != want {
			t.Errorf("points %s: %q, want %q", ext, field(r, "estimate"), want)
		}
	}
	before := taskCount(h)
	for _, bad := range []string{"2d", "90m", "-1", "1,5"} {
		if _, err := importRun(t, h, writeFile(t, "bad.csv", "id,title,estimate\nOK,fine,1\nBAD,bad,"+bad+"\n"), ""); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("estimate %q under points: %v", bad, err)
		}
	}
	if n := taskCount(h); n != before {
		t.Fatalf("a refused estimate left %d tasks", n-before)
	}
	h.MustYes("policy.set", `{"key":"tasks.estimate_unit","value":"hours"}`)
	hrs := mustImport(t, h, writeFile(t, "h.csv", "id,title,estimate\nH1,h1,90m\nH2,h2,2\n"), "")
	for ext, want := range map[string]string{"H1": "1.5", "H2": "2"} {
		if _, r := get(h, schema.TableTasks, rowByExt(t, hrs, ext).ID); field(r, "estimate") != want {
			t.Errorf("hours %s: %q, want %q", ext, field(r, "estimate"), want)
		}
	}
	if _, err := importRun(t, h, writeFile(t, "d.csv", "id,title,estimate\nD,d,2d\n"), ""); !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "no day unit") {
		t.Fatalf("2d under hours: %v", err)
	}
}

// TestImportIgnoredColumns: the CSV export's lead, created_at and
// completed_at are read past with one W_IMPORT_IGNORED_COLUMN each,
// however many rows carry a value; a column that is empty in every row
// gives no line; the rows are imported.
func TestImportIgnoredColumns(t *testing.T) {
	h := opstest.New(t, repoClient(false))
	csv := "id,title,lead,created_at,completed_at\n" +
		"A,a,act_01936f0000007a1b8c2d3e4f5a6b7c8d,2026-11-01T10:00:00Z,\n" +
		"B,b,act_01936f0000007a1b8c2d3e4f5a6b7c8d,2026-11-02T10:00:00Z,\n"
	out := mustImport(t, h, writeFile(t, "i.csv", csv), "")
	var got []string
	for _, w := range out.Warnings {
		if w.Code == opstask.WarnImportIgnoredColumn {
			got = append(got, w.Field)
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"created_at", "lead"}) || out.Count != 2 {
		t.Fatalf("ignored columns %v (count %d)", got, out.Count)
	}
	if !strings.Contains(out.Text(), opstask.WarnImportIgnoredColumn+": column \"lead\"") {
		t.Fatalf("text: %s", out.Text())
	}
}

// TestImportMapStatus: --map-status renames file values (pairs in one
// value or repeated), unmapped values pass through and are checked as
// statuses; a pair without "=", with an empty file value or with a
// target that is not a status is refused before any write.
func TestImportMapStatus(t *testing.T) {
	h := opstest.New(t, repoClient(false))
	file := writeFile(t, "j.csv", "Key,Summary,Status\nJ-1,review,In Review\nJ-2,closed,Closed\nJ-3,plain,todo\n")
	out := mustImport(t, h, file, `,"map":["Key=id,Summary=title,Status=status"],"map_status":["In Review=in_progress","Closed=done"]`)
	for ext, want := range map[string]string{"J-1": "in_progress", "J-2": "done", "J-3": "todo"} {
		if _, r := get(h, schema.TableTasks, rowByExt(t, out, ext).ID); field(r, "status") != want {
			t.Errorf("%s: %q, want %s", ext, field(r, "status"), want)
		}
	}
	before := taskCount(h)
	other := writeFile(t, "k.csv", "id,title,status\nK-1,k,Closed\n")
	for _, ms := range []string{`["Closed"]`, `["=done"]`, `["Closed=finished"]`, `["Closed=done,Open"]`, `[]`} {
		if _, err := importRun(t, h, other, `,"map_status":`+ms); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("--map-status %s: %v", ms, err)
		}
	}
	if n := taskCount(h); n != before {
		t.Fatal("a refused --map-status wrote tasks")
	}
	if got := mustImport(t, h, other, `,"map_status":["Closed=cancelled,Open=todo"]`); got.Count != 1 {
		t.Fatalf("two pairs in one value: %+v", got)
	}
}

// TestExportImportRoundTripStatus: a JSON export of a tree with statuses
// and estimates imported into another repo keeps status, estimate and the
// parent link.
func TestExportImportRoundTripStatus(t *testing.T) {
	a := opstest.New(t, repoClient(false))
	p := add(a, "parent", `"estimate":"5"`)
	c := add(a, "child", `"parent":"`+p+`","estimate":"2"`)
	a.Must(opstask.OpDone, `{"id":"`+c+`","no_lesson":true}`)
	a.Must(opstask.OpStart, `{"id":"`+p+`"}`)
	var exp opstask.ExportOut
	if err := json.Unmarshal([]byte(a.JSON(a.Must(opstask.OpExport, `{"format":"json"}`))), &exp); err != nil {
		t.Fatal(err)
	}
	b := opstest.New(t, repoClient(false))
	out := mustImport(t, b, writeFile(t, "rt.json", exp.Content), "")
	np, nc := rowByExt(t, out, p).ID, rowByExt(t, out, c).ID
	_, pr := get(b, schema.TableTasks, np)
	_, cr := get(b, schema.TableTasks, nc)
	if field(pr, "status") != "in_progress" || field(pr, "estimate") != "5" ||
		field(cr, "status") != "done" || field(cr, "estimate") != "2" || field(cr, schema.FieldTaskParent) != np {
		t.Fatalf("round trip: parent %s/%s, child %s/%s under %q (want %s)",
			field(pr, "status"), field(pr, "estimate"), field(cr, "status"), field(cr, "estimate"), field(cr, schema.FieldTaskParent), np)
	}
}
