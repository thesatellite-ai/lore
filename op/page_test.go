package op_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

type pageIn struct {
	Kind string `json:"kind,omitempty" flag:"kind" desc:"a filter"`
	op.PageFlags
}

type pageOut struct {
	Count int      `json:"count"`
	Data  []string `json:"data"`
	Notes []string `json:"notes,omitempty"`
}

func (o pageOut) Text() string { return strings.Join(o.Data, ",") + "\n" }

// taggedOut has two slices: the `page:"rows"` one is cut, inside an
// envelope struct (task.checklist.list's data.items).
type taggedOut struct {
	Data struct {
		Total int      `json:"total"`
		Items []string `json:"items" page:"rows"`
	} `json:"data"`
	Warnings []string `json:"warnings,omitempty"`
}

// noRows has two slices and no tag: Define cannot tell which to page.
type noRows struct {
	A []string `json:"a"`
	B []string `json:"b"`
}

func rows(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = string(rune('a' + i))
	}
	return out
}

func invokePage(t *testing.T, body string) (pageOut, string, error) {
	t.Helper()
	list := op.Define(op.Spec{Name: "thing.list"}, func(context.Context, pageIn) (pageOut, error) {
		r := rows(5)
		return pageOut{Count: len(r), Data: r}, nil
	})
	in, err := list.Decode(op.JSONDecoder{Body: []byte(body)})
	if err != nil {
		t.Fatal(err)
	}
	res, err := list.Invoke(context.Background(), in)
	if err != nil {
		return pageOut{}, "", err
	}
	b, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var out pageOut
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	text, _ := res.Text()
	return out, text, nil
}

// TestPageFlagsCut: an input embedding op.PageFlags gets --limit/--page,
// and Invoke cuts the output's rows (count follows; the text names the
// next page); page alone is 50 rows; no flags = every row; a page past
// the end is empty with a line saying so; a negative number is refused.
func TestPageFlagsCut(t *testing.T) {
	for _, c := range []struct {
		body, rows, line string
	}{
		{`{}`, "a,b,c,d,e", ""},
		{`{"limit":2}`, "a,b", "rows 1–2 of 5; next: --limit 2 --page 2"},
		{`{"limit":2,"page":2}`, "c,d", "rows 3–4 of 5; next: --limit 2 --page 3"},
		{`{"limit":2,"page":3}`, "e", "rows 5–5 of 5\n"},
		{`{"limit":2,"page":9}`, "", "page 9 is past the last row (5 rows, 2 per page)"},
		{`{"page":1}`, "a,b,c,d,e", ""},
		{`{"limit":10}`, "a,b,c,d,e", ""},
	} {
		out, text, err := invokePage(t, c.body)
		if err != nil {
			t.Fatalf("%s: %v", c.body, err)
		}
		if got := strings.Join(out.Data, ","); got != c.rows || out.Count != len(out.Data) {
			t.Errorf("%s: rows %q count %d, want %q", c.body, got, out.Count, c.rows)
		}
		if c.line == "" && strings.Contains(text, "rows ") || !strings.Contains(text, c.line) {
			t.Errorf("%s: text %q, want line %q", c.body, text, c.line)
		}
	}
	for _, body := range []string{`{"limit":-1}`, `{"page":-2}`} {
		if _, _, err := invokePage(t, body); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("%s: want E_INVALID_INPUT, got %v", body, err)
		}
	}
}

// TestPageFlagsRowsField: a `page:"rows"` slice is cut wherever it sits
// (one struct down), never the other slices; an output Define cannot page
// panics at startup, as every tag error does.
func TestPageFlagsRowsField(t *testing.T) {
	list := op.Define(op.Spec{Name: "thing.list"}, func(context.Context, pageIn) (taggedOut, error) {
		var o taggedOut
		o.Data.Items, o.Data.Total, o.Warnings = rows(4), 4, rows(3)
		return o, nil
	})
	in, err := list.Decode(op.JSONDecoder{Body: []byte(`{"limit":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	res, err := list.Invoke(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := res.JSON()
	if want := `{"data":{"total":4,"items":["a"]},"warnings":["a","b","c"]}`; string(b) != want {
		t.Fatalf("got %s, want %s", b, want)
	}
	defer func() {
		if r := recover(); r == nil || !strings.Contains(strings.ToLower(errText(r)), "no rows to page") {
			t.Fatalf("Define of an unpageable output: recover() = %v", r)
		}
	}()
	op.Define(op.Spec{Name: "thing.list"}, func(context.Context, pageIn) (noRows, error) { return noRows{}, nil })
}

func errText(r any) string {
	if e, ok := r.(error); ok {
		return e.Error()
	}
	return ""
}
