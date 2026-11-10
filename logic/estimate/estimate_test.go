package estimate

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestParse is the 9.4 parser table.
func TestParse(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		unit Unit
		want string
		ok   bool
	}{
		{"3", Points, "3", true},
		{"0.5", Points, "0.5", true},
		{"3.00", Points, "3.00", true},
		{"1,5", Points, "", false},
		{"-1", Points, "", false},
		{"1e3", Points, "", false},
		{"1.234", Points, "", false},
		{"90m", Points, "", false},
		{"90m", Hours, "1.5", true},
		{"1h30m", Hours, "1.5", true},
		{"2", Hours, "2", true},
		{"", Points, "", false},
		{".5", Points, "", false},
	}
	for _, c := range cases {
		got, err := Parse(c.raw, c.unit)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("Parse(%q, %s) = %q, %v; want %q ok=%v", c.raw, c.unit, got, err, c.want, c.ok)
		}
	}
}

// TestSum: mixed estimated / unestimated leaves, exact decimals.
func TestSum(t *testing.T) {
	t.Parallel()
	tot := Sum([]Leaf{{"0.1", true}, {"0.2", false}, {"", false}, {"3", false}, {"bad", true}})
	if Format(tot.Done) != "0.1" || Format(tot.Open) != "3.2" || tot.Unestimated != 2 || Format(tot.All()) != "3.3" {
		t.Errorf("sum %+v", tot)
	}
	if Label(150, Hours) != "1.5h" || Label(300, Points) != "3 points" || Label(5, Days) != "0.05d" {
		t.Errorf("labels")
	}
}

// TestNoDayUnit (OWNERQ-21): a day amount is refused in every unit with a
// reason that names the missing day unit, and every value (a plain number
// included) is refused under the legacy unit days, whose hint names the
// policy command; nothing is ever returned to store. Stored literals of a
// days project still read and print in days.
func TestNoDayUnit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		unit Unit
		hint string
	}{
		{"2d", Points, "unit is points"},
		{"2d", Hours, "duration like 90m"},
		{"0.5d", Hours, "unit is hours"},
		{" 3d ", Points, "unit is points"},
		{"2d", Days, "tasks.estimate_unit hours"},
		{"2", Days, "tasks.estimate_unit hours"},
		{"16h", Days, "tasks.estimate_unit hours"},
		{"", Days, "tasks.estimate_unit hours"},
	}
	for _, c := range cases {
		got, err := Parse(c.raw, c.unit)
		if got != "" || !errs.Has(err, errs.InvalidInput) {
			t.Fatalf("Parse(%q, %s) = %q, %v; want E_INVALID_INPUT", c.raw, c.unit, got, err)
		}
		env := errs.EnvelopeOf(err).Error
		if !strings.Contains(env.Message, "no day unit") || !strings.Contains(env.Hint, c.hint) {
			t.Errorf("Parse(%q, %s): message %q hint %q; want no day unit and %q", c.raw, c.unit, env.Message, env.Hint, c.hint)
		}
	}
	// "2dd", "d" and "2days" are not a day amount: the plain refusal.
	for _, raw := range []string{"2dd", "d", "2days"} {
		if _, err := Parse(raw, Points); err == nil || strings.Contains(err.Error(), "no day unit") {
			t.Errorf("Parse(%q, points): %v", raw, err)
		}
	}
	if h, ok := Hundredths("2.5"); !ok || Label(h, Days) != "2.5d" || UnitOf("days") != Days {
		t.Errorf("a stored days estimate no longer reads: %d %v %q", h, ok, UnitOf("days"))
	}
}
