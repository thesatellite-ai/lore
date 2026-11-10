package timecalc

import (
	"testing"
	"time"
)

func at(hm string) time.Time {
	t, err := time.Parse("15:04", hm)
	if err != nil {
		panic(err)
	}
	return t.AddDate(2026, 10, 17)
}

func auto(actor, session, owner, s, e string) Span {
	return Span{Worker: Worker{actor, session}, Owner: owner, Start: at(s), End: at(e)}
}

const h, m = int64(3600), int64(60)

// TestComputeExamples runs the worked examples of design 5.2.
func TestComputeExamples(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		spans           []Span
		effort, elapsed int64
	}{
		"A run inside claim":   {[]Span{auto("codex", "s1", "tomas", "10:00", "11:30"), auto("codex", "s1", "tomas", "10:05", "10:50")}, 90 * m, 90 * m},
		"B run sticks out":     {[]Span{auto("codex", "s1", "tomas", "10:00", "11:30"), auto("codex", "s1", "tomas", "11:20", "11:45")}, 105 * m, 105 * m},
		"C two sessions":       {[]Span{auto("codex", "s1", "tomas", "10:00", "11:30"), auto("codex", "s2", "tomas", "10:30", "11:00")}, 2 * h, 90 * m},
		"D person and agent":   {[]Span{auto("mira", "", "mira", "09:00", "12:00"), auto("miracodex", "s1", "mira", "09:30", "11:00")}, 4*h + 30*m, 3 * h},
		"F reopened":           {[]Span{auto("mira", "", "mira", "09:00", "11:00"), auto("mira", "", "mira", "12:00", "12:30")}, 150 * m, 150 * m},
		"G manual on top":      {[]Span{auto("mira", "", "mira", "09:00", "10:00"), {Worker: Worker{Actor: "mira"}, Owner: "mira", Manual: true, Seconds: 30 * m}}, 90 * m, 60 * m},
		"skew counts zero":     {[]Span{auto("mira", "", "mira", "10:00", "09:00")}, 0, 0},
		"touching counts once": {[]Span{auto("mira", "", "mira", "09:00", "10:00"), auto("mira", "", "mira", "10:00", "11:00")}, 2 * h, 2 * h},
		"empty":                {nil, 0, 0},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := Compute(c.spans)
			if r.Effort != c.effort || r.Elapsed != c.elapsed {
				t.Fatalf("effort %d elapsed %d, want %d %d", r.Effort, r.Elapsed, c.effort, c.elapsed)
			}
			// order of input never matters
			rev := make([]Span, len(c.spans))
			for i, s := range c.spans {
				rev[len(c.spans)-1-i] = s
			}
			if r2 := Compute(rev); r2.Effort != r.Effort || r2.Elapsed != r.Elapsed {
				t.Fatalf("order changed the result")
			}
		})
	}
}

// TestOwnerSubtotal is TQ1: the incl. agents subtotal counts overlap once.
func TestOwnerSubtotal(t *testing.T) {
	t.Parallel()
	r := Compute([]Span{auto("mira", "", "mira", "09:00", "12:00"), auto("miracodex", "s1", "mira", "09:30", "11:00")})
	if len(r.Owners) != 1 {
		t.Fatalf("owners %v", r.Owners)
	}
	o := r.Owners[0]
	if o.Own != 3*h || o.Agents != 90*m || o.Incl != 3*h {
		t.Fatalf("owner %+v", o)
	}
}

func TestCoalesceAndFlags(t *testing.T) {
	t.Parallel()
	gap := 2 * time.Minute
	if !Coalesce(at("10:00"), at("10:30"), at("10:31"), at("11:00"), gap) {
		t.Fatal("1m apart should coalesce")
	}
	if Coalesce(at("10:00"), at("10:30"), at("10:33"), at("11:00"), gap) {
		t.Fatal("3m apart should not coalesce")
	}
	if !Coalesce(at("10:31"), at("11:00"), at("10:00"), at("10:30"), gap) {
		t.Fatal("order must not matter")
	}
	if !LongSpan(11*h, 10*time.Hour) || LongSpan(10*h, 10*time.Hour) || LongSpan(99*h, 0) {
		t.Fatal("long span")
	}
	if !Overlaps(at("09:00"), at("10:00"), at("09:30"), at("10:30")) || Overlaps(at("09:00"), at("10:00"), at("10:00"), at("11:00")) {
		t.Fatal("overlaps")
	}
}
