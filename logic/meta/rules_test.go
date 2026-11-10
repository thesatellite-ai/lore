package meta

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
)

// TestRules pins the 3.19 checks over the kinds × rules matrix: pattern on
// text and on each list item, bounds on numbers, dates and datetimes, and
// that a value of another type is left to the type check.
func TestRules(t *testing.T) {
	num, date, dt := Type{Kind: value.KindNumber}, Type{Kind: value.KindDate}, Type{Kind: value.KindDateTime}
	txt, list := Type{Kind: value.KindText}, Type{Kind: value.KindList, Elem: value.KindText}
	cases := []struct {
		name string
		t    Type
		raw  string
		r    Rules
		bad  bool
	}{
		{"pattern ok", txt, `"HUB-1"`, Rules{Pattern: `^HUB-\d+$`}, false},
		{"pattern bad", txt, `"X-1"`, Rules{Pattern: `^HUB-\d+$`}, true},
		{"pattern list item", list, `["HUB-1","x"]`, Rules{Pattern: `^HUB-\d+$`}, true},
		{"min ok", num, `0`, Rules{Min: "0", Max: "200"}, false},
		{"below min", num, `-0.5`, Rules{Min: "0"}, true},
		{"above max", num, `200.5`, Rules{Max: "200"}, true},
		{"date in range", date, `"2026-11-04"`, Rules{Min: "2026-01-01", Max: "2026-12-31"}, false},
		{"date late", date, `"2027-01-01"`, Rules{Max: "2026-12-31"}, true},
		{"datetime early", dt, `"2026-01-01T00:00:00Z"`, Rules{Min: "2026-06-01T00:00:00Z"}, true},
		{"other type ignored", num, `"abc"`, Rules{Min: "0"}, false},
	}
	for _, c := range cases {
		why, err := Violation("k", c.t, []byte(c.raw), c.r)
		if err != nil || (why != "") != c.bad {
			t.Errorf("%s: why=%q err=%v want bad=%v", c.name, why, err, c.bad)
		}
	}
	for _, bad := range []struct {
		t Type
		r Rules
	}{{txt, Rules{Pattern: "("}}, {txt, Rules{Min: "1"}}, {num, Rules{Min: "x"}}, {num, Rules{Min: "5", Max: "1"}}, {date, Rules{Max: "2026-13-01"}}} {
		if CheckRuleDefs(bad.t, bad.r) == nil {
			t.Errorf("CheckRuleDefs(%v, %+v) passed", bad.t, bad.r)
		}
	}
	if got := MissingRequired("tasks", map[string][]string{"jira": {"tasks"}, "x": {"memories"}}, nil); len(got) != 1 || got[0] != "jira" {
		t.Errorf("MissingRequired %v", got)
	}
	if DeprecatedNote("jira", Rules{Deprecated: true, ReplacedBy: "ticket"}) != "jira is deprecated; use ticket" {
		t.Error("deprecated note")
	}
}
