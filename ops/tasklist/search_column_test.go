package tasklist_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestSearchColumns: v0.1.17's `tasklist search --column` restricts the match
// to one full-text field (title or body here), several OR together, and an
// unknown column is E_INVALID_INPUT (v0.1.17: an E_INTERNAL sqlite error).
func TestSearchColumns(t *testing.T) {
	h := opstest.New(t)
	addOne := func(in string) string {
		t.Helper()
		var w struct{ Data struct{ ID string } }
		if err := json.Unmarshal([]byte(h.JSON(h.Must("tasklist.add", in))), &w); err != nil {
			t.Fatal(err)
		}
		return w.Data.ID
	}
	inTitle := addOne(`{"title":"zebra crossing","body":"plain words"}`)
	inBody := addOne(`{"title":"plain title","body":"a zebra appears"}`)
	search := func(in string) []string {
		t.Helper()
		var out struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(h.JSON(h.Must("tasklist.search", in))), &out); err != nil {
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
	for in, want := range map[string][]string{
		`{"query":"zebra"}`:                            both,
		`{"query":"zebra","columns":["title"]}`:        {inTitle},
		`{"query":"zebra","columns":["body"]}`:         {inBody},
		`{"query":"zebra","columns":["title","body"]}`: both,
		`{"query":"crossing","columns":["body"]}`:      nil,
	} {
		if got := search(in); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", in, got, want)
		}
	}
	for _, bad := range []string{`["nope"]`, `[""]`, `["title","nope"]`} {
		h.Fails("tasklist.search", `{"query":"zebra","columns":`+bad+`}`, errs.InvalidInput)
	}
}
