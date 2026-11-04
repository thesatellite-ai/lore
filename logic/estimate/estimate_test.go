package estimate

import "testing"

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
		{"2d", Days, "2", true},
		{"4h", Days, "", false},
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
