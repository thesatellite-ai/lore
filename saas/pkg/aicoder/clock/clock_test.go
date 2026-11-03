package clock

import (
	"sync"
	"testing"
	"time"
)

func TestFakeMovesOnlyWhenTold(t *testing.T) {
	start := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	f := NewFake(start)
	first, second := f.Now(), f.Now()
	if !first.Equal(start) || !second.Equal(start) {
		t.Fatalf("fake moved on its own: %v then %v", first, second)
	}
	if got := f.Advance(90 * time.Second); !got.Equal(start.Add(90 * time.Second)) {
		t.Fatalf("Advance = %v", got)
	}
	f.Advance(-time.Hour)
	if want := start.Add(90*time.Second - time.Hour); !f.Now().Equal(want) {
		t.Fatalf("backwards Advance = %v want %v", f.Now(), want)
	}
	f.Set(start)
	if !f.Now().Equal(start) {
		t.Fatalf("Set = %v", f.Now())
	}
}

func TestSkewed(t *testing.T) {
	start := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	f := NewFake(start)
	for _, off := range []time.Duration{-5 * time.Minute, 0, 5 * time.Minute} {
		s := Skewed{Base: f, Offset: off}
		if want := start.Add(off); !s.Now().Equal(want) {
			t.Fatalf("offset %v: %v want %v", off, s.Now(), want)
		}
	}
	f.Advance(time.Minute)
	if want := start.Add(6 * time.Minute); !(Skewed{Base: f, Offset: 5 * time.Minute}).Now().Equal(want) {
		t.Fatal("skewed clock does not follow its base")
	}
}

func TestFakeConcurrent(t *testing.T) {
	f := NewFake(time.Unix(0, 0))
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 100 {
				f.Advance(time.Second)
				_ = f.Now()
			}
		})
	}
	wg.Wait()
	if want := time.Unix(1600, 0); !f.Now().Equal(want) {
		t.Fatalf("lost updates: %v want %v", f.Now(), want)
	}
}

func TestRealIsNow(t *testing.T) {
	before := time.Now()
	got := Real{}.Now()
	if got.Before(before) || got.After(time.Now()) {
		t.Fatalf("Real.Now out of range: %v", got)
	}
}
