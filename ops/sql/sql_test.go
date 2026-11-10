package sql

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
)

// fakeEnv records the call and answers with res / err.
type fakeEnv struct {
	res     Result
	err     error
	query   string
	show    bool
	timeout time.Duration
	calls   int
}

func (f *fakeEnv) SQL(_ context.Context, r Request) (Result, error) {
	f.calls++
	f.query, f.show, f.timeout = r.Query, r.ShowSensitive, r.Timeout
	return f.res, f.err
}

func txt(t *testing.T, s string) value.Value {
	t.Helper()
	v, err := value.TextOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func num(t *testing.T, s string) value.Value {
	t.Helper()
	v, err := value.Infer([]byte(s))
	if err != nil || v.Kind() != value.KindNumber {
		t.Fatalf("number %s: %v %v", s, v, err)
	}
	return v
}

// TestRunPassesQueryAndFlag: the trimmed query and show_sensitive reach
// the env; an empty or blank query is E_INVALID_INPUT without a call.
func TestRunPassesQueryAndFlag(t *testing.T) {
	for _, show := range []bool{false, true} {
		f := &fakeEnv{res: Result{Columns: []string{"a"}, Rows: [][]value.Value{{num(t, "1")}}}}
		out, err := run(context.Background(), f, RunIn{Query: "  SELECT 1 AS a \n", ShowSensitive: show})
		if err != nil {
			t.Fatal(err)
		}
		if f.query != "SELECT 1 AS a" || f.show != show || out.Count != 1 || out.Rows[0].Cells[0] != (Cell{Kind: "number", Value: "1"}) {
			t.Fatalf("show=%v: env got %q/%v, out %+v", show, f.query, f.show, out)
		}
	}
	for _, q := range []string{"", "   ", "\n\t"} {
		f := &fakeEnv{}
		if _, err := run(context.Background(), f, RunIn{Query: q}); !errs.Has(err, errs.InvalidInput) || f.calls != 0 {
			t.Fatalf("%q: %v (calls %d)", q, err, f.calls)
		}
	}
	boom := errs.New(errs.SQLRefused, "refused")
	if _, err := run(context.Background(), &fakeEnv{err: boom}, RunIn{Query: "DELETE FROM x"}); !errs.Has(err, errs.SQLRefused) {
		t.Fatalf("env error lost: %v", err)
	}
}

// TestRunCells: every value kind becomes a typed cell; NULL (and a nil
// value) is kind null with an empty value; an empty result has non-nil
// columns and rows (JSON [] not null).
func TestRunCells(t *testing.T) {
	f := &fakeEnv{res: Result{Columns: []string{"t", "n", "r", "z", "nil"}, Rows: [][]value.Value{{txt(t, "x"), num(t, "10"), num(t, "2.5"), value.Null{}, nil}}}}
	out, err := run(context.Background(), f, RunIn{Query: "SELECT"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Cell{{"text", "x"}, {"number", "10"}, {"number", "2.5"}, {"null", ""}, {"null", ""}}
	for i, c := range out.Rows[0].Cells {
		if c != want[i] {
			t.Errorf("cell %d = %+v, want %+v", i, c, want[i])
		}
	}
	out, err = run(context.Background(), &fakeEnv{}, RunIn{Query: "SELECT 1 WHERE 0"})
	if err != nil || out.Columns == nil || out.Rows == nil || out.Count != 0 {
		t.Fatalf("empty = %+v, %v", out, err)
	}
}

// TestText: aligned columns by character (unicode counts once), NULL
// printed, line breaks flattened, long cells capped, row-count line, and
// the empty form.
func TestText(t *testing.T) {
	out := RunOut{
		Columns: []string{"name", "ip", "note"},
		Rows: []Row{
			{Cells: []Cell{{"text", "hub_prod"}, {"text", "203.0.113.10"}, {"text", "two\nlines\tand tab"}}},
			{Cells: []Cell{{"text", "héé"}, {"null", ""}, {"text", strings.Repeat("x", maxCellRunes+5)}}},
		},
		Count: 2,
	}
	got := out.Text()
	want := "name      ip            note\n" +
		"hub_prod  203.0.113.10  two lines and tab\n" +
		"héé       NULL          " + strings.Repeat("x", cutKeepRunes) + ellipsis + "\n" +
		"(2 rows)\n"
	if got != want {
		t.Fatalf("Text =\n%s\nwant\n%s", got, want)
	}
	one := RunOut{Columns: []string{"n"}, Rows: []Row{{Cells: []Cell{{"number", "1"}}}}, Count: 1}
	if got := one.Text(); got != "n\n1\n(1 row)\n" {
		t.Fatalf("one row = %q", got)
	}
	if got := (RunOut{Columns: []string{"a", "b"}}).Text(); got != "a  b\n(no rows)\n" {
		t.Fatalf("empty = %q", got)
	}
}

// TestSpec: the op is a read, asks only for show_sensitive, and routes to
// POST /v1/sql/run.
func TestSpec(t *testing.T) {
	es := Ops(&fakeEnv{})
	if len(es) != 1 {
		t.Fatalf("ops = %d", len(es))
	}
	s := es[0].Spec()
	if s.Name != OpRun || s.Access != op.AccessRead || s.Permission != op.PermRead || s.Confirm.When != showSensitiveField || s.Confirm.Always {
		t.Fatalf("spec = %+v", s)
	}
	r, ok := op.RouteOf(s, es[0].InputFields())
	if !ok || r.Method != "POST" || r.Path != "/v1/sql/run" {
		t.Fatalf("route = %+v", r)
	}
}

// TestRunFormatsAndTimeout: --format csv/json print the same typed rows,
// an unknown format is E_INVALID_INPUT, and --timeout defaults to 10s.
func TestRunFormatsAndTimeout(t *testing.T) {
	res := Result{Columns: []string{"name", "port", "note"}, Rows: [][]value.Value{
		{txt(t, "hub_api"), num(t, "8080"), value.Null{}},
		{txt(t, `a,"b"`), num(t, "2.5"), txt(t, "x")},
	}}
	f := &fakeEnv{res: res}
	out, err := run(context.Background(), f, RunIn{Query: "SELECT 1", Format: FormatCSV})
	if err != nil {
		t.Fatal(err)
	}
	if f.timeout != DefaultTimeout {
		t.Fatalf("timeout = %v, want %v", f.timeout, DefaultTimeout)
	}
	if got, want := out.Text(), "name,port,note\nhub_api,8080,\n\"a,\"\"b\"\"\",2.5,x\n"; got != want {
		t.Fatalf("csv = %q, want %q", got, want)
	}
	out, err = run(context.Background(), f, RunIn{Query: "SELECT 1", Format: FormatJSON, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if f.timeout != time.Second {
		t.Fatalf("timeout = %v", f.timeout)
	}
	if got, want := out.Text(), "[\n  {\"name\": \"hub_api\", \"port\": 8080, \"note\": null},\n  {\"name\": \"a,\\\"b\\\"\", \"port\": 2.5, \"note\": \"x\"}\n]\n"; got != want {
		t.Fatalf("json = %q, want %q", got, want)
	}
	if _, err := run(context.Background(), f, RunIn{Query: "SELECT 1", Format: "xml"}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("format xml: %v", err)
	}
}
