package memory_test

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
)

// TestSearchFindsVarByValue (OV8, SECRETS_AND_REFERENCES 2.6 second
// column): a memory that holds ${var:db_host} is found by the variable's
// value through both `lore memory search` and `lore search`, as well as by
// its own words; a word in neither the text nor the value finds nothing;
// after the variable changes, the old value no longer finds the row and the
// new one does.
func TestSearchFindsVarByValue(t *testing.T) {
	h := opstest.New(t)
	h.Must("vars.set", `{"name":"db_host","value":"pgmain.internal.example"}`)
	id := h.ID(h.Must("memory.add", `{"body":"connect to ${var:db_host} for reports"}`))

	finds := func(op, q string) bool {
		t.Helper()
		in := `{"query":` + opstest.Quote(q) + `}`
		return strings.Contains(h.JSON(h.Must(op, in)), id)
	}
	for _, op := range []string{"memory.search", "search.run"} {
		for _, c := range []struct {
			q    string
			want bool
		}{
			{"reports", true},                   // its own text
			{"pgmain", true},                    // the variable's value
			{`"pgmain.internal.example"`, true}, // the value as a phrase
			{"zebra", false},
		} {
			if got := finds(op, c.q); got != c.want {
				t.Errorf("%s %q: found %v, want %v", op, c.q, got, c.want)
			}
		}
	}

	h.Must("vars.set", `{"name":"db_host","value":"pgreplica.internal.example"}`)
	for _, op := range []string{"memory.search", "search.run"} {
		if finds(op, "pgmain") || !finds(op, "pgreplica") {
			t.Errorf("%s after the value changed: old %v new %v", op, finds(op, "pgmain"), finds(op, "pgreplica"))
		}
	}
}
