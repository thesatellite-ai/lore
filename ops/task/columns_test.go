package task_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// listTable is a task.list result's JSON columns and per-task cells.
type listTable struct {
	Columns []string `json:"columns"`
	Data    []struct {
		ID    string   `json:"id"`
		Cells []string `json:"cells"`
	} `json:"data"`
}

// tableOf runs task.list with in and decodes the column table parts.
func tableOf(h *opstest.H, in string) (listTable, string) {
	h.T.Helper()
	res := h.Must(opstask.OpList, in)
	var out listTable
	if err := json.Unmarshal([]byte(h.JSON(res)), &out); err != nil {
		h.T.Fatal(err)
	}
	return out, opstest.Text(res)
}

// cellsFor is the cells of task id in t (nil when it is not listed).
func cellsFor(t listTable, id string) []string {
	for _, d := range t.Data {
		if d.ID == id {
			return d.Cells
		}
	}
	return nil
}

// TestListColumnsTable: task list --columns (TASK 16.2) prints an upper-case
// header and rows padded to the widest cell (runes, not bytes), in the
// order given; bare and +name mix into one table; cells are in --json; a
// missing value prints "-"; dates print as a day, datetimes as a UTC minute;
// the computed columns (tracked/time, elapsed, progress) are filled.
func TestListColumnsTable(t *testing.T) {
	h := opstest.New(t)
	a := add(h, "Café ünïcode", `"priority":"high","due":"2026-12-05","estimate":"3"`)
	b := add(h, "b", `"priority":"low"`)
	h.Must(opstask.OpTimeLog, `{"id":"`+a+`","duration":"1h30m","at":"2026-11-02"}`)
	child := add(h, "child", `"parent":"`+a+`"`)
	add(h, "child two", `"parent":"`+a+`"`)
	h.Must(opstask.OpDone, `{"id":"`+child+`","no_lesson":true}`)

	tab, txt := tableOf(h, `{"columns":["title,priority","+due_at"],"where":["id=`+a+`,`+b+`"],"sort":["priority:desc"]}`)
	if want := []string{"title", "priority", "due_at"}; !slices.Equal(tab.Columns, want) {
		t.Fatalf("columns %v want %v", tab.Columns, want)
	}
	if got, want := cellsFor(tab, a), []string{"Café ünïcode", "high", "2026-12-05"}; !slices.Equal(got, want) {
		t.Errorf("cells a %v want %v", got, want)
	}
	if got, want := cellsFor(tab, b), []string{"b", "low", "-"}; !slices.Equal(got, want) {
		t.Errorf("cells b %v want %v", got, want)
	}
	// Header and padding: "Café ünïcode" is 12 runes (more bytes); the
	// TITLE column is 12 wide plus the two-space separator.
	want := "TITLE         PRIORITY  DUE_AT\n" +
		"Café ünïcode  high      2026-12-05\n" +
		"b             low       -\n"
	if txt != want {
		t.Errorf("table text\n%q\nwant\n%q", txt, want)
	}

	// Computed columns and the estimate (stored as typed: "3", TASK 9.3).
	tab, _ = tableOf(h, `{"columns":["id,estimate,tracked,time,progress,created_at"],"where":["id=`+a+`"]}`)
	got := cellsFor(tab, a)
	if len(got) != 6 || got[0] != a || got[1] != "3" || got[2] != "1h 30m" || got[3] != "1h 30m" || got[4] != "1/2" {
		t.Errorf("computed cells %v", got)
	}
	// created_at is a datetime: a UTC minute, never the full RFC 3339 text.
	if c := got[5]; !strings.HasSuffix(c, "Z") || len(c) != len("2006-01-02 15:04Z") {
		t.Errorf("datetime cell %q", c)
	}

	// Duplicates collapse; a +name already named is the same column.
	tab, _ = tableOf(h, `{"columns":["title","title","+title"],"where":["id=`+b+`"]}`)
	if !slices.Equal(tab.Columns, []string{"title"}) {
		t.Errorf("duplicate columns %v", tab.Columns)
	}

	// Refusals: +foo alone (only +time/+elapsed extend the line), an
	// unknown column (with the column list as a hint), an internal field.
	refusals := []struct{ in, msg, hint string }{
		{`{"columns":["+priority"]}`, "only +time and +elapsed extend the list line", "--columns id,title,priority"},
		{`{"columns":["nope"]}`, `--columns "nope" is not a column`, "task list columns: "},
		{`{"columns":["title,claim_epoch"]}`, `"claim_epoch" is not a column`, "progress"},
	}
	for _, c := range refusals {
		msg := h.Fails(opstask.OpList, c.in, errs.InvalidInput)
		if !strings.Contains(msg, c.msg) {
			t.Errorf("%s: %q lacks %q", c.in, msg, c.msg)
		}
		_, err := h.Run(opstask.OpList, c.in)
		if !strings.Contains(hintOf(err), c.hint) {
			t.Errorf("%s: hint %q lacks %q", c.in, hintOf(err), c.hint)
		}
	}

	// +time alone keeps the v0.1.17 line (no table, no cells).
	tab, txt = tableOf(h, `{"columns":["+time"],"where":["id=`+a+`"]}`)
	if len(tab.Columns) != 0 || len(cellsFor(tab, a)) != 0 {
		t.Errorf("+time alone made a table: %v %v", tab.Columns, cellsFor(tab, a))
	}
	if strings.HasPrefix(txt, "TITLE") || !strings.Contains(txt, "time:1h 30m") {
		t.Errorf("+time line %q", txt)
	}

	// No task: the empty marker, not a bare header.
	if _, txt = tableOf(h, `{"columns":["title"],"where":["title=zzz"]}`); txt != "(no tasks)\n" {
		t.Errorf("empty table %q", txt)
	}
}

// TestViewColumns: task view add/edit --columns stores the columns; a list
// with --view uses them only when it gives no --columns of its own.
func TestViewColumns(t *testing.T) {
	h := opstest.New(t)
	a := add(h, "alpha", `"priority":"high"`)
	h.Must(opstask.OpViewAdd, `{"name":"hot","where":["priority=high"],"columns":["id","title"]}`)

	tab, _ := tableOf(h, `{"view":"hot"}`)
	if !slices.Equal(tab.Columns, []string{"id", "title"}) || !slices.Equal(cellsFor(tab, a), []string{a, "alpha"}) {
		t.Errorf("view columns %v %v", tab.Columns, cellsFor(tab, a))
	}
	// --columns on the list wins over the view's.
	tab, _ = tableOf(h, `{"view":"hot","columns":["priority"]}`)
	if !slices.Equal(tab.Columns, []string{"priority"}) {
		t.Errorf("list --columns did not win: %v", tab.Columns)
	}
	// edit replaces the stored columns.
	h.Must(opstask.OpViewEdit, `{"name":"hot","columns":["title,priority"]}`)
	tab, _ = tableOf(h, `{"view":"hot"}`)
	if !slices.Equal(tab.Columns, []string{"title", "priority"}) {
		t.Errorf("edited view columns %v", tab.Columns)
	}
	// A bad column is refused when the view is saved, not when it runs.
	h.Fails(opstask.OpViewAdd, `{"name":"bad","where":["priority=high"],"columns":["nope"]}`, errs.InvalidInput)
	// A view without columns lists the usual lines.
	h.Must(opstask.OpViewAdd, `{"name":"plain","where":["priority=high"]}`)
	if tab, _ = tableOf(h, `{"view":"plain"}`); len(tab.Columns) != 0 {
		t.Errorf("plain view made a table %v", tab.Columns)
	}
}

// TestListWhereMeta: task list --where meta.<key> filters on real task
// metadata with every --meta operator, ANDed with field terms and --meta;
// a view saves and runs a meta term; task edit --where meta.<key> narrows
// the bulk preview to the matching tasks only.
func TestListWhereMeta(t *testing.T) {
	h := opstest.New(t)
	// points is a declared number key, so > compares numbers (METADATA 3.7).
	h.MustYes(settings.OpMetaAdd, `{"name":"points","type":"number"}`)
	acme := add(h, "acme one", `"priority":"high","meta":["client=acme","points=5"]`)
	acme2 := add(h, "acme two", `"priority":"low","meta":["client=acme","points=1"]`)
	beta := add(h, "beta", `"priority":"high","meta":["client=beta-corp"]`)
	none := add(h, "no meta", `"priority":"high"`)

	ids := func(in string) []string {
		t.Helper()
		tab, _ := tableOf(h, in)
		var got []string
		for _, d := range tab.Data {
			got = append(got, d.ID)
		}
		slices.Sort(got)
		return got
	}
	set := func(s ...string) []string { slices.Sort(s); return s }
	cases := []struct {
		in   string
		want []string
	}{
		{`{"where":["meta.client=acme"]}`, set(acme, acme2)},
		{`{"where":["meta.client!=acme"]}`, set(beta, none)},
		{`{"where":["meta.client~=CORP"]}`, set(beta)},
		{`{"where":["meta.points>2"]}`, set(acme)},
		{`{"where":["meta.points?"]}`, set(acme, acme2)},
		{`{"where":["meta.points!?"]}`, set(beta, none)},
		// ANDed with a field term (one value or two values) and with --meta.
		{`{"where":["meta.client=acme","priority=high"]}`, set(acme)},
		{`{"where":["meta.client=acme priority=high"]}`, set(acme)},
		{`{"where":["meta.client=acme"],"meta_where":["points>2"]}`, set(acme)},
		{`{"where":["meta.client=nobody"]}`, nil},
	}
	for _, c := range cases {
		if got := ids(c.in); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v want %v", c.in, got, c.want)
		}
	}
	// OR-ed values have no --meta form: refused, never half-applied.
	h.Fails(opstask.OpList, `{"where":["meta.client=acme,beta-corp"]}`, errs.InvalidInput)

	// A saved view with a meta term runs it.
	h.Must(opstask.OpViewAdd, `{"name":"acme_hot","where":["meta.client=acme","priority=high"]}`)
	if got := ids(`{"view":"acme_hot"}`); !slices.Equal(got, set(acme)) {
		t.Errorf("view with meta term: %v", got)
	}

	// Bulk edit preview narrows by the meta term; nothing is written.
	res := h.Must(opstask.OpEdit, `{"where":["meta.client=acme"],"priority":"urgent","dry_run":true}`)
	js := opstest.Text(res)
	if !strings.Contains(js, "would change 2 tasks") || !strings.Contains(js, acme) || !strings.Contains(js, acme2) || strings.Contains(js, beta) || strings.Contains(js, none) {
		t.Errorf("bulk preview %s", js)
	}
	_, r := get(h, "tasks", acme)
	if field(r, "priority") != "high" {
		t.Errorf("dry run wrote priority %q", field(r, "priority"))
	}
}
