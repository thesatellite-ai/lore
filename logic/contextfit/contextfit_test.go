package contextfit

import (
	"strings"
	"testing"
	"time"
)

func entry(id string, full, line int) Entry {
	return Entry{ID: id, Full: strings.Repeat("f", full), Line: strings.Repeat("l", line)}
}

func TestFitMatrix(t *testing.T) {
	cases := []struct {
		name      string
		p         Plan
		forms     []Form
		collapsed bool
		over      bool
	}{
		{"no budget keeps all full", Plan{Must: []Entry{entry("a", 500, 10), entry("b", 500, 10)}, Index: 50}, []Form{FormFull, FormFull}, false, false},
		{"everything fits", Plan{Budget: 2000, Fixed: 100, Index: 50, Must: []Entry{entry("a", 500, 10), entry("b", 500, 10)}}, []Form{FormFull, FormFull}, false, false},
		{"second becomes a line", Plan{Budget: 700, Fixed: 100, Index: 50, Must: []Entry{entry("a", 500, 10), entry("b", 500, 10)}}, []Form{FormFull, FormLine}, false, false},
		{"smaller later entry still full", Plan{Budget: 760, Fixed: 100, Index: 50, Must: []Entry{entry("a", 500, 10), entry("b", 900, 10), entry("c", 100, 20)}}, []Form{FormFull, FormLine, FormFull}, false, false},
		{"entry_max forces a line", Plan{Budget: 5000, EntryMax: 400, Must: []Entry{entry("a", 500, 10)}}, []Form{FormLine}, false, false},
		{"index collapses", Plan{Budget: 150, Fixed: 100, Index: 80, IndexCollapsed: 20, Must: []Entry{entry("a", 500, 10)}}, []Form{FormLine}, true, false},
		{"over budget writes everything", Plan{Budget: 50, Fixed: 100, Index: 80, IndexCollapsed: 20, Must: []Entry{entry("a", 500, 10)}}, []Form{FormLine}, true, true},
		{"full shorter than line always full", Plan{Budget: 50, Fixed: 100, Must: []Entry{entry("a", 5, 10)}}, []Form{FormFull}, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Fit(c.p)
			for i, f := range c.forms {
				if r.Forms[i] != f {
					t.Fatalf("entry %d form %s, want %s", i, r.Forms[i], f)
				}
			}
			if r.IndexCollapsed != c.collapsed || r.Over != c.over {
				t.Fatalf("collapsed %v over %v, want %v %v (total %d)", r.IndexCollapsed, r.Over, c.collapsed, c.over, r.Total)
			}
			if c.p.Budget > 0 && !r.Over && r.Total > c.p.Budget {
				t.Fatalf("total %d over budget %d without Over", r.Total, c.p.Budget)
			}
		})
	}
}

func TestRankOrder(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	es := []Entry{
		{ID: "e", Updated: t0},
		{ID: "d", Updated: t0.Add(time.Hour)},
		{ID: "c", HasSummary: true, Updated: t0},
		{ID: "b", Always: true, Updated: t0},
		{ID: "a", Updated: t0},
	}
	Rank(es)
	got := ""
	for _, e := range es {
		got += e.ID
	}
	if got != "bcdae" {
		t.Fatalf("rank %s, want bcdae", got)
	}
}

func TestShortForm(t *testing.T) {
	long := strings.Repeat("word ", 60) + "end."
	cases := []struct{ summary, title, body, want string }{
		{"Use cents.", "", "Money is ints. More.", "Use cents."},
		{"", "Flaky CI", "Rerun", "Flaky CI"},
		{"", "", "Money is ints. More text.", "Money is ints."},
		{"", "", "# Heading line\nbody", "Heading line"},
		{"", "", "Why? Because.", "Why?"},
		{"", "", long, "(no short form; run `lore show --id x`)"},
		{"", "", "", "(no short form; run `lore show --id x`)"},
		{"two\nlines", "", "", "two lines"},
	}
	for _, c := range cases {
		if got := ShortForm("x", c.summary, c.title, c.body); got != c.want {
			t.Errorf("ShortForm(%q,%q,%q) = %q, want %q", c.summary, c.title, c.body, got, c.want)
		}
	}
}
