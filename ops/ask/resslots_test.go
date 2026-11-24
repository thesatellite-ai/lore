package ask

// resslots_test.go — devco D-968 (Sync benchmark set 5, ask 1): code_repo
// resources named after the product ("shop_import_worker" for a "shop
// import" question) still took four to six of the answer's items next to
// the rows after D-963, and stood before the rows when the best row had
// changed (the resources then lead), so the hotfix that answered was left
// out. Now a resource the question only touches never stands before a
// matching row of any kind, and such resources take one item at the
// default cap (one more per default cap above it); a resource the
// question names keeps its place.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// shopFixture is the set-5 shape: the repo's folder kb/app holds a rule,
// a long as-built note, the hotfix that answers and a decision; kb/org
// holds eight code_repo resources whose names share the question's words
// (shop, import, workflow, workspace).
func shopFixture() data {
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	var rs []resourceRow
	for i, n := range []string{"shop_app", "shop_import_worker", "sync_shop", "pim_shop_bridge", "workflow_engine", "workspace_api", "shop_variant_mapper", "import_scheduler"} {
		rs = append(rs, resourceRow{ID: fmt.Sprintf("res_%d", i), Place: "kb/org", Kind: "code_repo", Name: n, Fields: []fieldValue{
			{"purpose", "Shop and PIM integration: imports products, variants and options from a shop workspace into the catalog, per workflow"},
			{"url", "https://git.example.test/" + n},
		}})
	}
	var note strings.Builder
	note.WriteString("As built: the catalog import reads a shop workspace in pages of 250 products and writes them through the one writer.")
	for _, s := range []string{"Fetching", "Mapping", "Writing", "Progress", "Retries"} {
		fmt.Fprintf(&note, "\n\n## %s\n\nThe %s step of the catalog import runs inside the import workflow for one workspace and logs each page with the run id.", s, s)
	}
	return data{
		resources: rs,
		rows: []textRow{
			{Table: "rules", ID: "rul_1", Place: "kb/app", Body: "Every shop import stamps the caller (workflow and user) on the run row.", At: at},
			{Table: "architecture_notes", ID: "anr_1", Place: "kb/app", Title: "As built: catalog import", Body: note.String(), At: at},
			{Table: "hotfixes", ID: "hfx_1", Place: "kb/app", Title: "One shop import per workflow and workspace", Body: "Only one shop import may run per workflow and workspace at a time: a second start returns ErrImportRunning.", At: at},
			{Table: "decisions", ID: "dec_1", Place: "kb/app", Title: "is_choice_axis backfill", Body: "Backfill is_choice_axis for existing products: options of a shop product with more than one variant are variant options.", At: at},
		},
	}
}

// touchedLines counts the code_repo lines of an answer's text.
func touchedLines(text string) int {
	n := 0
	for l := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(l, "code_repo ") {
			n++
		}
	}
	return n
}

// TestTouchedResourcesTakeOneSlot: on product questions the resources the
// question only touches take one item, after every row; --max-chars twice
// the cap prints one more (the printed next step works); a resource the
// question names whole keeps its full line before the rows it is about.
func TestTouchedResourcesTakeOneSlot(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		q, want string
		changed bool // every row has changed: the resources used to lead
	}{
		{"can two shop imports run at the same time for the same workflow and workspace", "hfx_1", false},
		{"can two shop imports run at the same time for the same workflow and workspace", "hfx_1", true},
		{"how are variant options flagged for existing shop products in the catalog", "dec_1", false},
		{"which workflow stamps the caller on a shop import run", "rul_1", false},
	} {
		d := shopFixture()
		if tc.changed {
			for i := range d.rows {
				d.rows[i].SupersededBy = "row_9"
			}
		}
		q := parseQuestion(tc.q, now)
		q.home = "kb/app"
		o := build(q, d, 0, 0)
		text := o.Text()
		if n := touchedLines(text); n != 1 || o.leftBrief == 0 {
			t.Errorf("%q (changed %v): %d code_repo lines (left %d); want one, the rest left out:\n%s", tc.q, tc.changed, n, o.leftBrief, text)
		}
		if !strings.Contains(text, "--max-chars 8000 prints more") {
			t.Errorf("%q: the left-out line does not say how to print more:\n%s", tc.q, text)
		}
		row := false
		for i := len(o.Ranked) - 1; i >= 0; i-- {
			if o.Ranked[i].Kind == KindRow {
				row = true
			} else if o.Ranked[i].Kind == KindResource && row {
				t.Errorf("%q (changed %v): a resource the question only touches stands before a row: %+v\n%s", tc.q, tc.changed, o.Ranked, text)
				break
			}
		}
		if !strings.Contains(text, tc.want) && !strings.Contains(strings.Join(o.Cited, " "), tc.want) {
			t.Errorf("%q (changed %v): the answering row %s is not shown:\n%s", tc.q, tc.changed, tc.want, text)
		}
		if more := build(q, d, 0, 2*DefaultMaxChars); touchedLines(more.Text()) != 2 {
			t.Errorf("%q: --max-chars %d prints %d code_repo lines, want 2:\n%s", tc.q, 2*DefaultMaxChars, touchedLines(more.Text()), more.Text())
		}
	}
	// named whole: the question is about it, its full line leads
	named := build(parseQuestion("what does shop_import_worker import", now), shopFixture(), 0, 0)
	if len(named.Ranked) == 0 || named.Ranked[0] != (Ranked{KindResource, "res_1"}) && !strings.Contains(named.Text(), "code_repo shop_import_worker: purpose") {
		t.Errorf("a resource the question names whole lost its place or fields: %+v\n%s", named.Ranked, named.Text())
	}
}
