package fromfile

import (
	"encoding/csv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// readErr is the first parse error of raw.
func readErr(t *testing.T, raw string) error {
	t.Helper()
	cr := csv.NewReader(strings.NewReader(raw))
	for {
		if _, err := cr.Read(); err != nil {
			return err
		}
	}
}

// TestCSVProblemNamesTheCause: each CSV parse error becomes its file line
// with the reason in words and a fix, never the bare "line 2".
func TestCSVProblemNamesTheCause(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ raw, at, reason string }{
		{"a,b\n\"x,y\nz,w\n", "line 2", "never closed"},
		{"a,b\nok,fine\n\"x,y\n", "line 3", "never closed"},
		{"a,b\n\"x\"y,z\n", "line 2", "text after the closing quote"},
		{"a,b\nx\"y,z\n", "line 2", "inside a cell that is not quoted"},
		{"a,b\nx,y,z\n", "line 2", "different number of cells"},
	} {
		p, ok := CSVProblem([]byte(c.raw), readErr(t, c.raw))
		if !ok || p.At != c.at || !strings.Contains(p.Reason, c.reason) || p.Fix == "" {
			t.Errorf("%q: got %+v", c.raw, p)
		}
	}
	e := CSV("x import", "f.csv", []byte("a\n\"b\n"), readErr(t, "a\n\"b\n"), "lore x import --file f.csv --dry-run")
	if e.Code != errs.InvalidInput || !strings.Contains(e.Message, "f.csv: 1 line needs a fix, nothing written: line 2: a quote") ||
		!strings.Contains(e.Hint, "in f.csv fix line 2: close the quote") || !strings.HasSuffix(e.Hint, "lore x import --file f.csv --dry-run") {
		t.Errorf("refusal: %s / %s", e.Message, e.Hint)
	}
}

// TestRefuseListsEveryLine: every problem is listed, lines counted once,
// and a shared code is kept while mixed codes fall back to invalid input.
func TestRefuseListsEveryLine(t *testing.T) {
	t.Parallel()
	if Refuse("x", "f.csv", nil, "") != nil {
		t.Fatal("no problem must be no refusal")
	}
	e := Refuse("x --from", "dir/f.csv", []Problem{
		{At: "line 3", Reason: "bad a", Fix: "fix a"},
		{At: "line 3", Reason: "bad b"},
		{At: "line 9", Reason: "bad c", Fix: "fix c"},
	}, "")
	if e.Message != "x --from dir/f.csv: 2 lines need a fix, nothing written: line 3: bad a; line 3: bad b; line 9: bad c" ||
		e.Hint != "in f.csv fix line 3: fix a; line 9: fix c" {
		t.Errorf("got %q / %q", e.Message, e.Hint)
	}
	if e := Refuse("x", "", []Problem{{Reason: "r", Code: errs.RoleUnknown}}, ""); e.Code != errs.RoleUnknown || e.Message != "x (nothing written): r" {
		t.Errorf("flag refusal: %s %q", e.Code, e.Message)
	}
	if e := Refuse("x", "", []Problem{{Reason: "r", Code: errs.RoleUnknown}, {Reason: "s"}}, ""); e.Code != errs.InvalidInput {
		t.Errorf("mixed codes: %s", e.Code)
	}
}
