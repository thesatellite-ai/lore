package task_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// TestSearchColumns restores v0.1.17's `task search --column`: each full-
// text field of tasks (title, body) restricts the match to that field,
// several columns OR together, no column searches both, a duplicate is
// harmless, and an unknown column is E_INVALID_INPUT naming the real ones
// (v0.1.17 failed with an E_INTERNAL sqlite error).
func TestSearchColumns(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	inTitle := add(h, "zebra crossing", `"body":"plain words"`)
	inBody := add(h, "plain title", `"body":"a zebra appears"`)
	ids := func(in string) []string {
		t.Helper()
		var out struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(h.JSON(h.Must(opstask.OpSearch, in))), &out); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, d := range out.Data {
			got = append(got, d.ID)
		}
		slices.Sort(got)
		return got
	}
	both := []string{inTitle, inBody}
	slices.Sort(both)
	cases := map[string][]string{
		`{"query":"zebra"}`:                             both,
		`{"query":"zebra","columns":["title"]}`:         {inTitle},
		`{"query":"zebra","columns":["body"]}`:          {inBody},
		`{"query":"zebra","columns":["title","body"]}`:  both,
		`{"query":"zebra","columns":["title","title"]}`: {inTitle},
		`{"query":"plain","columns":["title"]}`:         {inBody},
		`{"query":"crossing","columns":["body"]}`:       nil,
		`{"query":"zebra","columns":[" title "]}`:       {inTitle},
	}
	for in, want := range cases {
		if got := ids(in); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", in, got, want)
		}
	}
	for _, bad := range []string{`["nope"]`, `["status"]`, `[""]`, `["title","nope"]`} {
		h.Fails(opstask.OpSearch, `{"query":"zebra","columns":`+bad+`}`, errs.InvalidInput)
	}
}
