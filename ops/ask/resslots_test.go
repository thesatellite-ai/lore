package ask

// resslots_test.go — devco D-968 (Sync benchmark set 5, ask 1): code_repo
// resources named after the product ("shop_import_worker" for a "shop
// import" question) still took four to six of the answer's items next to
// the rows after D-963, and stood before the rows when the best row had
// changed (the resources then lead), so the hotfix that answered was left
// out. Now a resource the question only touches never stands before a
// matching row of any kind; since H-245 (devco D-998) no card is a full
// item while a row answers: the cards are lines of the candidate list.

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

// touchedLines counts the code_repo lines of an answer's text printed as
// full items (a candidate line is "  <id> (code_repo) <name>: …").
func touchedLines(text string) int {
	n := 0
	for l := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(l, "code_repo ") {
			n++
		}
	}
	return n
}

// cardLines counts the candidate lines of code_repo cards.
func cardLines(o Out) int {
	n := 0
	for _, c := range o.Candidates {
		if c.Kind == KindResource && c.Table == "code_repo" {
			n++
		}
	}
	return n
}

// TestTouchedResourcesTakeOneSlot: on product questions the resources the
// question only touches are never a full item (H-245): they are lines of
// the candidate list after the rows, the list within --max-chars; a
// resource the question names whole is the list's first line.
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
		if n, c := touchedLines(text), cardLines(o); n != 0 || c == 0 || len(o.Candidates) > maxCandidates || len(o.body()) > DefaultMaxChars {
			t.Errorf("%q (changed %v): %d code_repo full items, %d card candidate lines; want none full, cards in the list within the cap:\n%s", tc.q, tc.changed, n, c, text)
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
		if more := build(q, d, 0, 2*DefaultMaxChars); touchedLines(more.Text()) != 0 {
			t.Errorf("%q: --max-chars %d prints %d code_repo full items, want none:\n%s", tc.q, 2*DefaultMaxChars, touchedLines(more.Text()), more.Text())
		}
	}
	// named whole: the question is about it: it leads as a full item when
	// no row answers, else it is the first candidate line
	named := build(parseQuestion("what does shop_import_worker import", now), shopFixture(), 0, 0)
	leads := len(named.Ranked) > 0 && named.Ranked[0] == (Ranked{KindResource, "res_1"})
	first := len(named.Candidates) > 0 && named.Candidates[0].ID == "res_1" && strings.HasPrefix(named.Candidates[0].Line, "purpose: ")
	if !leads && !first {
		t.Errorf("a resource the question names whole lost its place: %+v %+v\n%s", named.Ranked, named.Candidates, named.Text())
	}
}
