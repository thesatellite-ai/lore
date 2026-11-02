package live

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)

func TestHLCRoundTripAndOrder(t *testing.T) {
	h := HLC{Wall: t0.Add(1500 * time.Microsecond).Truncate(hlcWallPrecision), Counter: 3, Actor: "act_3f9c"}
	s := h.String()
	if s != "2026-11-02T10:00:00.001Z/0003/act_3f9c" {
		t.Fatalf("string form %q", s)
	}
	back, err := ParseHLC(s)
	if err != nil || back.Compare(h) != 0 {
		t.Fatalf("round trip %v %v", back, err)
	}
	if z, err := ParseHLC(""); err != nil || !z.IsZero() || z.String() != "" {
		t.Fatalf("empty: %v %v", z, err)
	}
	for _, bad := range []string{"x", "2026-11-02T10:00:00Z/abc/a", "nope/0001/a"} {
		if _, err := ParseHLC(bad); err == nil {
			t.Fatalf("%q must not parse", bad)
		}
	}
	a := HLC{Wall: t0, Counter: 1, Actor: "a"}
	cases := []struct {
		b    HLC
		want int
	}{
		{HLC{Wall: t0.Add(time.Second)}, -1},
		{HLC{Wall: t0.Add(-time.Second), Counter: 9}, 1},
		{HLC{Wall: t0, Counter: 2}, -1},
		{HLC{Wall: t0, Counter: 1, Actor: "b"}, -1},
		{a, 0},
	}
	for _, c := range cases {
		if got := a.Compare(c.b); got != c.want {
			t.Fatalf("%v vs %v = %d, want %d", a, c.b, got, c.want)
		}
		// String order equals value order (fixed-width parts).
		if (a.String() < c.b.String()) != (c.want < 0) && c.want != 0 && c.b.Actor != "" {
			t.Fatalf("string order disagrees for %v vs %v", a, c.b)
		}
	}
}

// NextHLC must always exceed what it saw, whatever the local clock says
// (a laptop ten minutes behind edits after a correct one: F4 clock skew).
func TestNextHLCMonotonicUnderSkew(t *testing.T) {
	prev := HLC{Wall: t0.Add(10 * time.Minute), Counter: 4, Actor: "dev1"}
	for _, now := range []time.Time{t0, t0.Add(10 * time.Minute), t0.Add(11 * time.Minute)} {
		n := NextHLC(prev, now, "dev2")
		if n.Compare(prev) <= 0 {
			t.Fatalf("now=%v: %v not after %v", now, n, prev)
		}
	}
	if n := NextHLC(HLC{}, t0, "a"); n.Wall != t0 || n.Counter != 0 {
		t.Fatalf("fresh row: %v", n)
	}
	wrap := HLC{Wall: t0, Counter: ^uint32(0)}
	if n := NextHLC(wrap, t0, "a"); n.Compare(wrap) <= 0 {
		t.Fatalf("counter wrap must still move forward: %v", n)
	}
}

// F3 step 1 matrix: stamp age × kind × autopush × remote.
func TestShouldFetch(t *testing.T) {
	ttl := 60 * time.Second
	cases := []struct {
		name      string
		age       time.Duration
		never     bool
		kind      FetchKind
		autopush  bool
		hasRemote bool
		want      bool
	}{
		{"fresh 0s", 0, false, FetchCached, true, true, false},
		{"59s cached", 59 * time.Second, false, FetchCached, true, true, false},
		{"60s due", 60 * time.Second, false, FetchCached, true, true, true},
		{"61s due", 61 * time.Second, false, FetchCached, true, true, true},
		{"never fetched", 0, true, FetchCached, true, true, true},
		{"stamp in the future counts as stale", -time.Hour, false, FetchCached, true, true, true},
		{"sync always", 0, false, FetchAlways, true, true, true},
		{"sync always even with autopush off", 0, false, FetchAlways, false, true, true},
		{"autopush off never on its own", 61 * time.Second, false, FetchCached, false, true, false},
		{"no remote", 61 * time.Second, false, FetchAlways, true, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := FetchInputs{Now: t0, TTL: ttl, Kind: c.kind, Autopush: c.autopush, HasRemote: c.hasRemote}
			if !c.never {
				in.LastFetch = t0.Add(-c.age)
			}
			if got := ShouldFetch(in); got != c.want {
				t.Fatalf("ShouldFetch = %v, want %v", got, c.want)
			}
		})
	}
}

func TestFetchStampRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if !FetchStamp(dir).IsZero() {
		t.Fatal("no stamp yet")
	}
	if err := WriteFetchStamp(dir, t0); err != nil {
		t.Fatal(err)
	}
	if !FetchStamp(dir).Equal(t0) {
		t.Fatalf("stamp %v", FetchStamp(dir))
	}
}

func TestVersionBelow(t *testing.T) {
	cases := []struct {
		bin, floor string
		want       bool
	}{
		{"0.1.0-dev", "0.1.0", false},
		{"0.1.0", "0.1.1", true},
		{"1.2.3", "1.2", false},
		{"1.2", "1.2.1", true},
		{"v2.0.0", "1.9.9", false},
		{"0.1.0-dev", "999.0.0", true},
		{"garbage", "0.1.0", true}, // fail closed on our side
		{"0.1.0", "garbage", false},
		{"0.1.0", "", false},
	}
	for _, c := range cases {
		if got := VersionBelow(c.bin, c.floor); got != c.want {
			t.Fatalf("VersionBelow(%q, %q) = %v", c.bin, c.floor, got)
		}
	}
}

func TestFolderAndPaths(t *testing.T) {
	for in, want := range map[string]string{"": RootFolder, ".": RootFolder, "web": "web", "apps/web/": "apps/web", `apps\web`: "apps/web"} {
		if got := FolderFor(in); got != want {
			t.Fatalf("FolderFor(%q) = %q", in, got)
		}
	}
	for _, f := range []string{RootFolder, "web", "apps/web"} {
		if err := ValidateFolder(f); err != nil {
			t.Fatalf("%q: %v", f, err)
		}
	}
	for _, f := range []string{"", "_live", "_x/y"} {
		if err := ValidateFolder(f); err == nil {
			t.Fatalf("%q must be refused", f)
		}
	}
	p := RowPath("apps/web", "tasks", "tsk_1")
	folder, k, ok := ParseRowPath(p)
	if !ok || folder != "apps/web" || k != (RowKey{"tasks", "tsk_1"}) {
		t.Fatalf("parse %q: %q %v %v", p, folder, k, ok)
	}
	for _, bad := range []string{EpochPath, MetaPath, "tasks/x.json", "a/b/c.txt", "a//.json"} {
		if _, _, ok := ParseRowPath(bad); ok {
			t.Fatalf("%q must not parse as a row", bad)
		}
	}
}

func TestFileEncodeDecodeAndTombstone(t *testing.T) {
	body := map[string]any{"id": "tsk_1", "title": "fix", "_v": "1"}
	f := NewVersion(nil, body, t0, "act_1")
	b, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeFile(b)
	if err != nil {
		t.Fatal(err)
	}
	if back.Block.LiveSince != t0 || back.Block.HLC.Actor != "act_1" || back.Deleted() || back.ID() != "tsk_1" {
		t.Fatalf("round trip: %+v", back)
	}
	if _, has := back.Body[BlockKey]; has {
		t.Fatal("decoded body must not carry the block")
	}
	edit := NewVersion(&back, map[string]any{"id": "tsk_1", "title": "fixed"}, t0.Add(-time.Hour), "act_2")
	if edit.Block.LiveSince != t0 || edit.Block.HLC.Compare(back.Block.HLC) <= 0 {
		t.Fatalf("an edit keeps live_since and moves hlc forward: %+v", edit.Block)
	}
	tomb := NewTombstone(&edit, "tsk_1", t0.Add(time.Minute), "act_1")
	tb, _ := tomb.Encode()
	tback, _ := DecodeFile(tb)
	if !tback.Deleted() || len(tback.Body) != 1 || tback.ID() != "tsk_1" {
		t.Fatalf("a tombstone keeps only the id (§3.3): %+v", tback)
	}
	if _, err := DecodeFile([]byte(`{"id":"x","_live":"nope"}`)); err == nil {
		t.Fatal("a non-object block must be refused")
	}
	plain, err := DecodeFile([]byte(`{"id":"x"}`))
	if err != nil || plain.Deleted() || !plain.Block.HLC.IsZero() {
		t.Fatalf("a file without a block decodes with a zero block: %+v %v", plain, err)
	}
}
