package live

import (
	"testing"
	"time"
)

// TestBackingOffAfterFailedFetch (devco:U-6): a fetch that could not reach
// the remote holds the next FetchCached commands back for Backoff (the
// read TTL, also for a write whose own TTL is shorter); a later good fetch,
// a failure stamp in the future, or `lore sync` (FetchAlways) never holds
// one back. Before, a failed fetch left no stamp and every command waited
// for the network's timeout again.
func TestBackingOffAfterFailedFetch(t *testing.T) {
	ttl, readTTL := 60*time.Second, 5*time.Minute
	old := t0.Add(-time.Hour) // the last good fetch, long past the TTL
	cases := []struct {
		name    string
		failAgo time.Duration // 0 = no failure stamp
		last    time.Time
		kind    FetchKind
		want    bool // ShouldFetch
	}{
		{"no failure: due", 0, old, FetchCached, true},
		{"failed 10s ago: back off", 10 * time.Second, old, FetchCached, false},
		{"failed 2m ago: write still backs off for the read TTL", 2 * time.Minute, old, FetchCached, false},
		{"failed 6m ago: try again", 6 * time.Minute, old, FetchCached, true},
		{"sync always tries", 10 * time.Second, old, FetchAlways, true},
		{"failure stamp in the future never holds back", -time.Hour, old, FetchCached, true},
		{"a good fetch after the failure wins", 10 * time.Second, t0.Add(-5 * time.Second), FetchCached, false},
		{"a good fetch after the failure, now due", 10 * time.Minute, t0.Add(-2 * time.Minute), FetchCached, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := FetchInputs{Now: t0, LastFetch: c.last, TTL: ttl, Backoff: readTTL, Kind: c.kind, Autopush: true, HasRemote: true}
			if c.failAgo != 0 {
				in.LastFailure = t0.Add(-c.failAgo)
			}
			if got := ShouldFetch(in); got != c.want {
				t.Fatalf("ShouldFetch = %v, want %v", got, c.want)
			}
		})
	}
}

// TestFetchFailedStampClearedByGoodFetch: the failure stamp round-trips and
// a good fetch's stamp removes it.
func TestFetchFailedStampClearedByGoodFetch(t *testing.T) {
	dir := t.TempDir()
	if !FetchFailed(dir).IsZero() {
		t.Fatal("no failure stamp yet")
	}
	if err := WriteFetchFailed(dir, t0); err != nil {
		t.Fatal(err)
	}
	if got := FetchFailed(dir); !got.Equal(t0) {
		t.Fatalf("FetchFailed = %v, want %v", got, t0)
	}
	if err := WriteFetchStamp(dir, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !FetchFailed(dir).IsZero() {
		t.Fatal("a good fetch left the failure stamp")
	}
}
