package wire

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
)

// TestListLimitAndPage (T-4): task list and memory list take --limit and
// --page through the real App (they were refused as an unknown flag): the
// rows are cut, count follows the rows kept, the human text names the next
// page, and a negative number is E_INVALID_INPUT before anything is read.
func TestListLimitAndPage(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	for _, b := range []string{"first note", "second note", "third note"} {
		if _, err := run(t, a, opsmemory.OpAdd, `{"body":"`+b+`"}`, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, title := range []string{"task one", "task two", "task three"} {
		if _, err := run(t, a, "task.add", `{"title":"`+title+`"}`, false); err != nil {
			t.Fatal(err)
		}
	}
	type envelope struct {
		Count int               `json:"count"`
		Data  []json.RawMessage `json:"data"`
	}
	for _, name := range []string{opsmemory.OpList, "task.list"} {
		for _, c := range []struct {
			in   string
			want int
		}{{`{}`, 3}, {`{"limit":2}`, 2}, {`{"limit":2,"page":2}`, 1}, {`{"limit":2,"page":3}`, 0}, {`{"page":1}`, 3}} {
			out, err := run(t, a, name, c.in, false)
			if err != nil {
				t.Fatalf("%s %s: %v", name, c.in, err)
			}
			var env envelope
			if err := json.Unmarshal([]byte(out), &env); err != nil {
				t.Fatal(err)
			}
			if len(env.Data) != c.want || (c.want > 0 && env.Count != c.want) {
				t.Errorf("%s %s: %d rows, count %d; want %d", name, c.in, len(env.Data), env.Count, c.want)
			}
		}
		e, _ := a.Registry().Lookup(name)
		res, err := a.Run(background(), e, op.JSONDecoder{Body: []byte(`{"limit":2}`)}, op.Caller{})
		if err != nil {
			t.Fatal(err)
		}
		if text, _ := res.Text(); !strings.Contains(text, "rows 1–2 of 3; next: --limit 2 --page 2") {
			t.Errorf("%s --limit 2: text does not name the next page:\n%s", name, text)
		}
		if _, err := run(t, a, name, `{"limit":-1}`, false); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("%s --limit -1: want E_INVALID_INPUT, got %v", name, err)
		}
	}
}
