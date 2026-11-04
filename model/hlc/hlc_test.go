package hlc

import (
	"sync"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
)

const specActor = "act_01936f0000007a1b8c2d3e4f5a6b7c8d"

func actor(t testing.TB) ids.ID[ids.Actor] {
	t.Helper()
	a, err := ids.Parse[ids.Actor](specActor)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestParseSpecExample(t *testing.T) {
	t.Parallel()
	s := "2026-11-04T10:42:07.123Z/0003/" + specActor
	h, err := Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	if h.Counter != 3 || h.Wall.Nanosecond() != 123_000_000 || h.Actor != actor(t) {
		t.Fatalf("parsed %+v", h)
	}
	if h.String() != s {
		t.Fatalf("round trip %q", h.String())
	}
	if z, err := Parse(""); err != nil || !z.IsZero() || z.String() != "" {
		t.Fatal("empty must be the zero value")
	}
}

func TestParseRejectsNonCanonical(t *testing.T) {
	t.Parallel()
	for _, s := range []string{
		"2026-11-04T10:42:07.123Z/3/" + specActor,                            // short counter
		"2026-11-04T10:42:07.1234Z/0003/" + specActor,                        // extra digits
		"2026-11-04T10:42:07.123+01:00/0003/" + specActor,                    // zone
		"2026-11-04T10:42:07.123Z/0003/act_3f9c",                             // bad actor
		"2026-11-04T10:42:07.123Z/0003/" + specActor + "/x",                  // four parts
		"2026-11-04T10:42:07.123Z/-001/" + specActor,                         // sign
		"2026-11-04T10:42:07.123Z/4294967296/" + specActor,                   // overflow
		"2026-11-04 10:42:07.123Z/0003/" + specActor,                         // space
		"2026-11-04T10:42:07.123Z/0003/mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f00", // not an actor
	} {
		if _, err := Parse(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}

func TestCompareTotal(t *testing.T) {
	t.Parallel()
	a := actor(t)
	w := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	vals := []HLC{{}, {Wall: w, Actor: a}, {Wall: w, Counter: 1}, {Wall: w, Counter: 1, Actor: a}, {Wall: w.Add(time.Millisecond)}}
	for i, x := range vals {
		for j, y := range vals {
			got := x.Compare(y)
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got != want {
				t.Errorf("Compare(%d,%d)=%d want %d", i, j, got, want)
			}
			if i < j && x.String() >= y.String() && !x.IsZero() {
				t.Errorf("string order disagrees for %d,%d", i, j)
			}
		}
	}
}

func TestNextAlwaysAbove(t *testing.T) {
	t.Parallel()
	a := actor(t)
	w := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	prev := HLC{Wall: w, Counter: 7, Actor: a}
	for _, now := range []time.Time{w.Add(-time.Hour), w, w.Add(time.Microsecond), w.Add(time.Second)} {
		if n := Next(prev, now, ids.ID[ids.Actor]{}); n.Compare(prev) <= 0 {
			t.Errorf("Next at %v = %v not above %v", now, n, prev)
		}
	}
	wrap := HLC{Wall: w, Counter: ^uint32(0)}
	if n := Next(wrap, w, a); n.Compare(wrap) <= 0 || n.Counter != 0 {
		t.Errorf("wrap = %v", n)
	}
}

func TestClockStampsAboveSeenAndItself(t *testing.T) {
	t.Parallel()
	a := actor(t)
	frozen := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewClock(a, func() time.Time { return frozen })
	remote := HLC{Wall: frozen.Add(time.Hour), Counter: 2}
	h1 := c.Now(remote)
	if h1.Compare(remote) <= 0 {
		t.Fatal("not above seen")
	}
	var mu sync.Mutex
	seen := map[string]bool{}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				s := c.Now().String()
				mu.Lock()
				if seen[s] {
					t.Errorf("duplicate stamp %s", s)
				}
				seen[s] = true
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if c.Now().Compare(h1) <= 0 {
		t.Fatal("clock went backwards")
	}
}

func TestFromUpdate(t *testing.T) {
	t.Parallel()
	a := actor(t)
	u := time.Date(2026, 11, 4, 10, 42, 7, 123456789, time.UTC)
	h := FromUpdate(u, a)
	if h.String() != "2026-11-04T10:42:07.123Z/0000/"+specActor {
		t.Fatalf("upcast %q", h.String())
	}
}

func FuzzParse(f *testing.F) {
	f.Add("2026-11-04T10:42:07.123Z/0003/" + specActor)
	f.Add("2026-11-04T10:42:07.123Z/0000/")
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		h, err := Parse(s)
		if err != nil {
			return
		}
		if h.String() != s {
			t.Fatalf("Parse(%q).String() = %q", s, h.String())
		}
		if same := h; h.Compare(same) != 0 {
			t.Fatal("not reflexive")
		}
	})
}
