package orgcfg

import (
	"context"
	"testing"
	"time"
)

// TestClonesFetchStampClock (b-flow4 note, f53950ef): Doctor and Open
// stamp the clone's last fetch with Clones.Now (the app clock: LORE_NOW in
// a test build), so `lore store show`'s "last fetch" is reproducible; with
// no Now the stamp is the wall clock. Each Doctor/Open fetch moves the
// stamp to the clock's current value.
func TestClonesFetchStampClock(t *testing.T) {
	ctx := context.Background()
	origin := storeOrigin(t)
	at := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	c := Clones{Home: t.TempDir(), Now: func() time.Time { return at }}
	if d := c.Doctor(ctx, testNorm, origin, []string{"main"}); !d.LastFetch.Equal(at) {
		t.Fatalf("doctor stamp %v, want %v", d.LastFetch, at)
	}
	at = at.Add(time.Hour)
	if _, _, err := c.Open(ctx, testNorm, origin, "main"); err != nil {
		t.Fatal(err)
	}
	if got := c.Inspect(ctx, testNorm).LastFetch; !got.Equal(at) {
		t.Fatalf("open stamp %v, want %v", got, at)
	}
	before := time.Now().Add(-time.Minute)
	wall := Clones{Home: t.TempDir()}
	if d := wall.Doctor(ctx, testNorm, origin, []string{"main"}); d.LastFetch.Before(before) || d.LastFetch.After(time.Now().Add(time.Minute)) {
		t.Fatalf("no Now: stamp %v is not the wall clock", d.LastFetch)
	}
}
