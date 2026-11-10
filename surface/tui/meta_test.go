package tui

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// TestTUINeverPrintsMeta (OWNERQ-2 read-only browser, SECURITY): a row's
// metadata values (sensitive or not) are never printed by `lore tui`: not
// on the rows screen, not on the row screen, and the /text filter does
// not find a row by a metadata value (a match count would reveal it).
func TestTUINeverPrintsMeta(t *testing.T) {
	const (
		sensVal  = "BILLINGRATE9137"
		plainVal = "JIRAHUB4711"
	)
	r := memRow(t, "visible body text", "s3cr3t")
	sens, err := ids.New[ids.MetaKey]()
	if err != nil {
		t.Fatal(err)
	}
	plain, err := ids.New[ids.MetaKey]()
	if err != nil {
		t.Fatal(err)
	}
	mj := func(raw string) value.JSON {
		j, jerr := value.JSONOf([]byte(raw))
		if jerr != nil {
			t.Fatal(jerr)
		}
		return j
	}
	r.Meta = map[ids.ID[ids.MetaKey]]value.JSON{
		sens:  mj(`{"name":"billing_rate","sensitive":true,"v":"` + sensVal + `"}`),
		plain: mj(`{"name":"jira","v":"` + plainVal + `"}`),
	}
	src := fakeSource{rows: map[row.Table][]row.Row{"memories": {r}}}

	for _, tc := range []struct {
		name string
		keys []string
	}{
		{"rows and row screen", []string{"1", "q"}},
		{"filter by sensitive meta value", []string{"/" + sensVal, "q"}},
		{"filter by plain meta value", []string{"/" + plainVal, "q"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := run(t, src, Options{Kind: "memory"}, tc.keys...)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range []string{sensVal, plainVal} {
				// the typed filter echo is the only place the word may appear
				if strings.Count(out, v) > strings.Count(strings.Join(tc.keys, "\n"), v) {
					t.Errorf("metadata value %s printed:\n%s", v, out)
				}
			}
			if strings.HasPrefix(tc.keys[0], "/") && !strings.Contains(out, "0–0 of 0") {
				t.Errorf("a metadata value found the row:\n%s", out)
			}
			if tc.keys[0] == "1" && !strings.Contains(out, "visible body text") {
				t.Errorf("row screen lacks the body:\n%s", out)
			}
		})
	}
}
