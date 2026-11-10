package timecalc

import (
	"testing"
	"time"
)

// rng is a seeded splitmix64 sequence: reproducible test input (a failure
// names its seed), not randomness anyone relies on.
type rng struct{ s uint64 }

func (r *rng) next() uint64 {
	r.s += 0x9e3779b97f4a7c15
	z := r.s
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// IntN is a value in [0, n) (n > 0; the tiny modulo bias does not matter
// here). The top 31 bits always fit an int.
func (r *rng) IntN(n int) int {
	v := int(r.next() >> 33 & 0x7fffffff)
	return v % n
}

// Shuffle is a Fisher-Yates shuffle of n items.
func (r *rng) Shuffle(n int, swap func(i, j int)) {
	for i := n - 1; i > 0; i-- {
		swap(i, r.IntN(i+1))
	}
}

// randomSpans makes n spans over a few workers and owners from a seeded
// source (deterministic: a failure names its seed). Some spans are
// manual, some skewed (end before start), some zero length.
func randomSpans(r *rng, n int) []Span {
	base := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	workers := []struct {
		w     Worker
		owner string
	}{
		{Worker{Actor: "mira"}, "mira"},
		{Worker{Actor: "miracodex", Session: "s1"}, "mira"},
		{Worker{Actor: "miracodex", Session: "s2"}, "mira"},
		{Worker{Actor: "tomas"}, "tomas"},
		{Worker{Actor: "tomascodex", Session: "s1"}, "tomas"},
	}
	out := make([]Span, 0, n)
	for range n {
		wk := workers[r.IntN(len(workers))]
		s := Span{Worker: wk.w, Owner: wk.owner}
		if r.IntN(5) == 0 {
			s.Manual, s.Seconds = true, int64(r.IntN(7200))
		} else {
			start := base.Add(time.Duration(r.IntN(600)) * time.Minute)
			s.Start, s.End = start, start.Add(time.Duration(r.IntN(240)-20)*time.Minute)
		}
		out = append(out, s)
	}
	return out
}

// TestComputeProperties checks the invariants of design 5.1 over random
// span sets (beyond order invariance): effort is the sum of the worker
// totals; elapsed never exceeds auto effort and is at least the longest
// single auto span; without manual time effort >= elapsed; adding a span
// never lowers effort or elapsed; each owner's subtotal (incl. agents) is
// at most own + agents and at least own and every single worker of the owner; every shuffle
// gives the same result.
func TestComputeProperties(t *testing.T) {
	for seed := uint64(1); seed <= 300; seed++ {
		r := &rng{s: seed}
		spans := randomSpans(r, 1+r.IntN(12))
		res := Compute(spans)

		var sumWorkers, autoEffort, longest int64
		for _, w := range res.Workers {
			sumWorkers += w.Total()
			autoEffort += w.Auto
		}
		for _, s := range spans {
			if !s.Manual {
				iv := autoInterval(s)
				longest = max(longest, iv.e-iv.s)
			}
		}
		if sumWorkers != res.Effort {
			t.Fatalf("seed %d: effort %d != Σ workers %d", seed, res.Effort, sumWorkers)
		}
		if res.Elapsed > autoEffort || res.Elapsed < longest {
			t.Fatalf("seed %d: elapsed %d outside [%d, %d]", seed, res.Elapsed, longest, autoEffort)
		}
		if res.Effort != autoEffort+res.Manual {
			t.Fatalf("seed %d: effort %d != auto %d + manual %d", seed, res.Effort, autoEffort, res.Manual)
		}
		if res.Manual == 0 && res.Effort < res.Elapsed {
			t.Fatalf("seed %d: effort %d < elapsed %d without manual time", seed, res.Effort, res.Elapsed)
		}
		for _, o := range res.Owners {
			// Agents sums several workers that may overlap each other, so
			// the lower bound is the largest single worker of the owner.
			var biggest int64
			for _, w := range res.Workers {
				if w.Owner == o.Owner {
					biggest = max(biggest, w.Total())
				}
			}
			if o.Incl > o.Own+o.Agents || o.Incl < o.Own || o.Incl < biggest {
				t.Fatalf("seed %d: owner %s incl %d own %d agents %d biggest %d", seed, o.Owner, o.Incl, o.Own, o.Agents, biggest)
			}
		}
		// Monotonic: one more span never lowers a total.
		more := append(append([]Span{}, spans...), randomSpans(r, 1)...)
		if r2 := Compute(more); r2.Effort < res.Effort || r2.Elapsed < res.Elapsed {
			t.Fatalf("seed %d: adding a span lowered effort/elapsed (%d,%d → %d,%d)", seed, res.Effort, res.Elapsed, r2.Effort, r2.Elapsed)
		}
		// Shuffle invariance, including the sorted worker and owner lists.
		sh := append([]Span{}, spans...)
		r.Shuffle(len(sh), func(i, j int) { sh[i], sh[j] = sh[j], sh[i] })
		r3 := Compute(sh)
		if r3.Effort != res.Effort || r3.Elapsed != res.Elapsed || r3.Manual != res.Manual || len(r3.Workers) != len(res.Workers) {
			t.Fatalf("seed %d: shuffle changed the totals", seed)
		}
		for i := range res.Workers {
			if r3.Workers[i].Worker != res.Workers[i].Worker || r3.Workers[i].Total() != res.Workers[i].Total() {
				t.Fatalf("seed %d: shuffle changed worker %d", seed, i)
			}
		}
		for i := range res.Owners {
			if r3.Owners[i] != res.Owners[i] {
				t.Fatalf("seed %d: shuffle changed owner %d", seed, i)
			}
		}
	}
}

// TestCoalesceSymmetric: Coalesce does not depend on which span is "prev"
// (the writer closing the second span decides, and either machine may be
// that writer), and a gap of exactly the limit does not coalesce.
func TestCoalesceSymmetric(t *testing.T) {
	gap := 5 * time.Minute
	cases := []struct {
		name           string
		as, ae, bs, be string
		want           bool
	}{
		{"overlap", "09:00", "10:00", "09:30", "10:30", true},
		{"touch", "09:00", "10:00", "10:00", "10:30", true},
		{"inside", "09:00", "11:00", "09:30", "10:00", true},
		{"gap under limit", "09:00", "10:00", "10:04", "10:30", true},
		{"gap at limit", "09:00", "10:00", "10:05", "10:30", false},
		{"gap over limit", "09:00", "10:00", "11:00", "11:30", false},
	}
	for _, c := range cases {
		ab := Coalesce(at(c.as), at(c.ae), at(c.bs), at(c.be), gap)
		ba := Coalesce(at(c.bs), at(c.be), at(c.as), at(c.ae), gap)
		if ab != c.want || ba != c.want {
			t.Errorf("%s: a,b=%v b,a=%v want %v", c.name, ab, ba, c.want)
		}
	}
}
