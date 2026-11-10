package timeentry

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

var (
	start = time.Date(2026, 11, 4, 23, 59, 30, 0, time.FixedZone("IST", 5*3600+1800))
	actor = mustActor("act_01936f0000007a1b8c2d3e4f5a6b7c8d")
)

func mustActor(s string) ids.ID[ids.Actor] {
	a, err := ids.Parse[ids.Actor](s)
	if err != nil {
		panic(err)
	}
	return a
}

// TestNewSpan builds an entry: UTC TimeLayout stamps, Day from the start in
// UTC, whole seconds, created/updated by the actor, Now defaulting to End.
func TestNewSpan(t *testing.T) {
	t.Parallel()
	end := start.Add(90*time.Minute + 1500*time.Millisecond)
	e, err := NewSpan(Span{Kind: KindAgent, Source: SourceRun, Actor: actor, Start: start, End: end, LocalRef: "run_x", Note: "ノート"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.ID.String(), "tme_") {
		t.Errorf("id %s", e.ID)
	}
	if e.Start != "2026-11-04T18:29:30.000Z" || e.End != "2026-11-04T19:59:31.500Z" || e.Day != "2026-11-04" {
		t.Errorf("times %s %s %s", e.Start, e.End, e.Day)
	}
	if e.Seconds != 90*60+1 {
		t.Errorf("seconds %d", e.Seconds)
	}
	if e.CreatedAt != e.End || e.UpdatedAt != e.End || e.CreatedBy != actor.String() || e.UpdatedBy != actor.String() {
		t.Errorf("stamps %+v", e)
	}
	if e.LocalRef != "run_x" || e.Note != "ノート" || e.Deleted() {
		t.Errorf("fields %+v", e)
	}
	now := end.Add(time.Hour)
	e2, err := NewSpan(Span{Kind: KindManual, Source: SourceManual, Actor: actor, Start: start, End: start, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if e2.Seconds != 0 || e2.CreatedAt != now.UTC().Format(TimeLayout) {
		t.Errorf("zero span %+v", e2)
	}
	if e2.ID == e.ID {
		t.Error("ids reused")
	}
}

// TestNewSpanRefuses: unknown kind or source (incl. empty and case
// variants), no actor, zero start, end before start.
func TestNewSpanRefuses(t *testing.T) {
	t.Parallel()
	ok := Span{Kind: KindPerson, Source: SourceStatus, Actor: actor, Start: start, End: start.Add(time.Second)}
	cases := map[string]func(*Span){
		"empty kind":   func(s *Span) { s.Kind = "" },
		"upper kind":   func(s *Span) { s.Kind = "Agent" },
		"bad kind":     func(s *Span) { s.Kind = "robot" },
		"empty source": func(s *Span) { s.Source = "" },
		"bad source":   func(s *Span) { s.Source = "cron" },
		"no actor":     func(s *Span) { s.Actor = ids.ID[ids.Actor]{} },
		"zero start":   func(s *Span) { s.Start = time.Time{} },
		"end first":    func(s *Span) { s.End = s.Start.Add(-time.Nanosecond) },
	}
	for name, mut := range cases {
		s := ok
		mut(&s)
		if _, err := NewSpan(s); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := NewSpan(ok); err != nil {
		t.Fatal(err)
	}
}

// TestDecodeEncode: round trip keeps every field; empty, blank and null
// decode to no entries; nil encodes as []; non-lists are refused.
func TestDecodeEncode(t *testing.T) {
	t.Parallel()
	e, err := NewSpan(Span{Kind: KindAgent, Source: SourceClaim, Actor: actor, Start: start, End: start.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	e.Flags = []string{"overlap"}
	e.DeletedAt, e.DeletedBy = e.UpdatedAt, actor.String()
	raw, err := Encode([]Entry{e})
	if err != nil {
		t.Fatal(err)
	}
	back, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 1 || back[0].ID != e.ID || back[0].Seconds != e.Seconds || back[0].Flags[0] != "overlap" || !back[0].Deleted() {
		t.Errorf("round trip %+v", back)
	}
	again, err := Encode(back)
	if err != nil || string(again) != string(raw) {
		t.Errorf("not byte-stable:\n%s\n%s", raw, again)
	}
	for _, in := range []string{"", "  \n", "null"} {
		if got, err := Decode([]byte(in)); err != nil || got != nil {
			t.Errorf("Decode(%q) = %v %v", in, got, err)
		}
	}
	if got, err := Encode(nil); err != nil || string(got) != "[]" {
		t.Errorf("Encode(nil) = %s %v", got, err)
	}
	for _, in := range []string{`{}`, `"x"`, `[1]`, `[{"id":"mem_01936f3300007d4e8f5a6b7c8d9e0f1a"}]`, `[`} {
		if _, err := Decode([]byte(in)); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("Decode(%q) accepted: %v", in, err)
		}
	}
}

// TestSum skips tombstones and breaks the total down by kind.
func TestSum(t *testing.T) {
	t.Parallel()
	list := []Entry{
		{Kind: KindAgent, Seconds: 60},
		{Kind: KindAgent, Seconds: 30},
		{Kind: KindPerson, Seconds: 600},
		{Kind: KindManual, Seconds: 1000, DeletedAt: "2026-11-04T10:00:00.000Z"},
	}
	got := Sum(list)
	if got.All != 690 || got.ByKind[KindAgent] != 90 || got.ByKind[KindPerson] != 600 || got.ByKind[KindManual] != 0 {
		t.Errorf("sum %+v", got)
	}
	if empty := Sum(nil); empty.All != 0 || empty.ByKind == nil {
		t.Errorf("empty %+v", empty)
	}
}

// TestFormatSeconds is the length table.
func TestFormatSeconds(t *testing.T) {
	t.Parallel()
	cases := map[int64]string{
		-5: "0m", 0: "0m", 1: "1s", 59: "59s", 60: "1m", 119: "1m", 3599: "59m",
		3600: "1h 0m", 3660: "1h 1m", 5025: "1h 23m", 86400 * 3: "72h 0m",
	}
	for in, want := range cases {
		if got := FormatSeconds(in); got != want {
			t.Errorf("FormatSeconds(%d) = %q, want %q", in, got, want)
		}
	}
}
