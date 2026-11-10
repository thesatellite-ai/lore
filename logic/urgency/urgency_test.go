package urgency

import (
	"testing"
	"time"
)

var now = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func pts(s Score, term Term) int {
	for _, p := range s.Terms {
		if p.Term == term {
			return p.Points
		}
	}
	return 0
}

func TestDueCurveEdges(t *testing.T) {
	w := Default()
	cases := []struct {
		name string
		due  time.Time
		want int
	}{
		{"overdue", now.Add(-48 * time.Hour), 1200},
		{"due now", now, 1200},
		{"in 14 days", now.Add(14 * day), 20},
		{"in 15 days", now.Add(15 * day), 0},
		{"in 7 days", now.Add(7 * day), 610},
	}
	for _, c := range cases {
		s := Compute([]Task{{ID: "a", Due: c.due}}, now, w)["a"]
		if got := pts(s, TermDue); got != c.want {
			t.Errorf("%s: due points %d, want %d", c.name, got, c.want)
		}
	}
}

func TestAgeEdges(t *testing.T) {
	for _, c := range []struct {
		days int
		want int
	}{{0, 0}, {30, 100}, {60, 200}, {600, 200}} {
		s := Compute([]Task{{ID: "a", CreatedAt: now.Add(-time.Duration(c.days) * day)}}, now, Default())["a"]
		if got := pts(s, TermAge); got != c.want {
			t.Errorf("age %d days: %d, want %d", c.days, got, c.want)
		}
	}
}

func TestPropagationAncestorsAndWaiters(t *testing.T) {
	tasks := []Task{
		{ID: "p", Priority: PriorityHigh},
		{ID: "c", Priority: PriorityLow, Parent: "p"},
		{ID: "g", Priority: PriorityLow, Parent: "c"},
		{ID: "u", Priority: PriorityLow},
		{ID: "w", Priority: PriorityUrgent, After: []string{"u"}},
		// a dependency cycle must terminate
		{ID: "x", Priority: PriorityLow, After: []string{"y"}},
		{ID: "y", Priority: PriorityMedium, After: []string{"x"}},
	}
	s := Compute(tasks, now, Default())
	if s["g"].Effective != PriorityHigh || pts(s["g"], TermPriority) != 600 {
		t.Fatalf("grandchild effective %v", s["g"])
	}
	if s["g"].Terms[0].Detail != "priority high (inherited from ancestor p)" {
		t.Fatalf("detail %q", s["g"].Terms[0].Detail)
	}
	if s["u"].Effective != PriorityUrgent || pts(s["u"], TermBlocking) != 400 {
		t.Fatalf("waited-on %v", s["u"])
	}
	if s["x"].Effective != PriorityMedium {
		t.Fatalf("cycle %v", s["x"])
	}
}

func TestTermsAndOverride(t *testing.T) {
	w := Default()
	if err := w.Set(TermNextTag, "", 100); err != nil {
		t.Fatal(err)
	}
	if err := w.Set("nope", "", 1); err == nil {
		t.Fatal("unknown term accepted")
	}
	s := Compute([]Task{{ID: "a", Status: StatusInProgress, NextTag: true, Proposed: true, SLARisk: true}}, now, w)["a"]
	if pts(s, TermInProgress) != 400 || pts(s, TermNextTag) != 100 || pts(s, TermProposed) != -300 || pts(s, TermSLARisk) != 500 {
		t.Fatalf("terms %+v", s.Terms)
	}
	if s.Total != 390+400+100-300+500 {
		t.Fatalf("total %d", s.Total)
	}
	if Format(-300) != "-3.00" || Format(1740) != "17.40" {
		t.Fatal("format")
	}
}

func TestRankDeterministic(t *testing.T) {
	base := []Task{
		{ID: "a", Priority: PriorityHigh, CreatedAt: now.Add(-2 * day)},
		{ID: "b", Priority: PriorityHigh, CreatedAt: now.Add(-2 * day)},
		{ID: "c", Priority: PriorityLow, Due: now},
		{ID: "d", Priority: PriorityMedium, CreatedAt: now.Add(-90 * day)},
		{ID: "e", Status: StatusDone},
	}
	want, _ := Rank(base, now, Default())
	// 100 deterministic shuffles (a fixed LCG: reproducible, no RNG needed).
	seed := uint64(7)
	for range 100 {
		in := append([]Task(nil), base...)
		for i := len(in) - 1; i > 0; i-- {
			seed = seed*6364136223846793005 + 1442695040888963407
			j := (seed >> 33) % uint64(i+1)
			in[i], in[j] = in[j], in[i]
		}
		got, _ := Rank(in, now, Default())
		for i := range want {
			if got[i].ID != want[i].ID {
				t.Fatalf("order changed: %v vs %v", got, want)
			}
		}
	}
	if len(want) != 4 || want[0].ID != "c" {
		t.Fatalf("rank %v", want)
	}
}
