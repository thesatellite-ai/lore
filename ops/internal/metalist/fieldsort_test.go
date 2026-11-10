package metalist

import (
	"context"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// sortRow is a row named by its test label (kept in "label") with fields.
func sortRow(t *testing.T, label string, created time.Time, fields map[string]value.Value) row.Row {
	t.Helper()
	id, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	lv, err := value.TextOf(label)
	if err != nil {
		t.Fatal(err)
	}
	f := map[string]value.Value{"label": lv}
	for k, v := range fields {
		f[k] = v
	}
	return row.Row{Table: "memories", ID: id.Any(), CreatedAt: created, UpdatedAt: created.Add(time.Hour), Fields: f}
}

// must turns a value constructor's result into its Value, failing t.
func must[T value.Value](t *testing.T) func(T, error) value.Value {
	return func(v T, err error) value.Value {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

// order runs Select with only --sort and returns the labels in output order.
func order(t *testing.T, rows []row.Row, sort string) []string {
	t.Helper()
	res, err := Select(context.Background(), fakeKeys{}, Flags{Sort: sort}, rows)
	if err != nil {
		t.Fatalf("--sort %s: %v", sort, err)
	}
	out := make([]string, 0, len(res.Order))
	for _, i := range res.Order {
		out = append(out, rows[i].Fields["label"].String())
	}
	return out
}

// TestFieldSort is the field half of --sort (G-CONTENT-1, CLI_REFERENCE
// 2.2/4.5): the envelope times, text, numbers (numerically, not as text),
// datetime values and RFC 3339 texts by instant, a list by its first item,
// rows without the value last in both directions, ties in list order.
func TestFieldSort(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	text := func(s string) value.Value { return must[value.Text](t)(value.TextOf(s)) }
	num := func(s string) value.Value { return must[value.Number](t)(value.NumberOf(s)) }
	dt := func(s string) value.Value { return must[value.DateTime](t)(value.DateTimeOf(s)) }
	list := func(items ...string) value.Value {
		vs := make([]value.Value, 0, len(items))
		for _, s := range items {
			vs = append(vs, text(s))
		}
		return must[value.List](t)(value.ListOf(value.KindText, vs...))
	}
	rows := []row.Row{
		sortRow(t, "b", t0.Add(2*time.Minute), map[string]value.Value{"title": text("beta"), "n": num("10"), "due": dt("2026-11-03T00:00:00Z"), "at": text("2026-11-03T00:00:00Z"), "tags": list("m", "a")}),
		sortRow(t, "a", t0, map[string]value.Value{"title": text("alpha"), "n": num("9"), "due": dt("2026-11-05T00:00:00Z"), "at": text("2026-11-05T09:00:00+09:00"), "tags": list("b")}),
		sortRow(t, "none", t0.Add(time.Minute), map[string]value.Value{}),
		sortRow(t, "c", t0.Add(3*time.Minute), map[string]value.Value{"title": text("gamma"), "n": num("-1.5"), "due": dt("2026-11-04T00:00:00Z"), "at": text("2026-11-04T00:00:00Z"), "tags": list("z")}),
	}
	cases := []struct {
		sort string
		want []string
	}{
		{"created_at", []string{"a", "none", "b", "c"}},
		{"created_at:desc", []string{"c", "b", "none", "a"}},
		{"updated_at:desc", []string{"c", "b", "none", "a"}},
		{"title", []string{"a", "b", "c", "none"}},
		{"title:desc", []string{"c", "b", "a", "none"}},
		{"n", []string{"c", "a", "b", "none"}},
		{"n:desc", []string{"b", "a", "c", "none"}},
		{"due", []string{"b", "c", "a", "none"}},
		// 2026-11-05T09:00+09:00 is 2026-11-05T00:00Z: compared by instant
		{"at:desc", []string{"a", "c", "b", "none"}},
		// first items: a "b", b "m" (not its smaller "a"), c "z"
		{"tags", []string{"a", "b", "c", "none"}},
		{"tags:desc", []string{"c", "b", "a", "none"}},
	}
	for _, c := range cases {
		t.Run(c.sort, func(t *testing.T) {
			got := order(t, rows, c.sort)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("--sort %s: got %v, want %v", c.sort, got, c.want)
				}
			}
		})
	}
}

// TestFieldSortRouting: which spelling sorts by a field and which by a
// metadata key; `-key` is refused for fields and keys alike (XQ10).
func TestFieldSortRouting(t *testing.T) {
	rows := []row.Row{sortRow(t, "x", time.Now(), map[string]value.Value{"title": must[value.Text](t)(value.TextOf("t"))})}
	cases := []struct {
		sort    string
		field   bool
		wantErr bool
	}{
		{"", false, false},
		{"title", true, false},
		{"title:asc", true, false},
		{"created_at", true, false},
		{"updated_at:desc", true, false},
		{"meta.title", false, false}, // meta. forces the metadata key even when a field has the name
		{"jira", false, false},       // no row carries it: a metadata key
		{"-title", false, true},
		{"-jira", false, true},
		{"title:sideways", false, true},
	}
	for _, c := range cases {
		t.Run(c.sort, func(t *testing.T) {
			_, isField, err := fieldSort(c.sort, rows)
			if c.wantErr {
				if !errs.Has(err, errs.InvalidInput) {
					t.Fatalf("--sort %q: %v, want E_INVALID_INPUT", c.sort, err)
				}
				return
			}
			if err != nil || isField != c.field {
				t.Fatalf("--sort %q: field=%v err=%v, want field=%v", c.sort, isField, err, c.field)
			}
		})
	}
	if _, err := Select(context.Background(), fakeKeys{}, Flags{Sort: "-title"}, rows); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("Select --sort -title: %v, want E_INVALID_INPUT use title:desc", err)
	}
}
