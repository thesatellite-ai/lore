package work

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

const goodTask = "tsk_01a1a5aca7f57659be7320c5f4b70011"

// TestQuote: short values verbatim; long ones cut at maxQuotedID bytes
// plus "..." (v0.1.17 bytes for ASCII) and never split a character.
func TestQuote(t *testing.T) {
	ascii := strings.Repeat("a", 60)
	cases := map[string]string{
		"":                      `""`,
		"xyz":                   `"xyz"`,
		strings.Repeat("b", 48): `"` + strings.Repeat("b", 48) + `"`,
		ascii:                   `"` + strings.Repeat("a", 48) + `..."`,
	}
	for in, want := range cases {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{strings.Repeat("é", 40), "a" + strings.Repeat("日", 30), strings.Repeat("🚀", 20)} {
		got := Quote(in)
		if !utf8.ValidString(got) || !strings.HasSuffix(got, `..."`) || len(got) > maxQuotedID+5 {
			t.Errorf("Quote(%q) = %q: cut mid-character or too long", in, got)
		}
	}
}

// TestParseIDAndRefTo: the id flags accept a trimmed id of the right
// prefix only; RefTo is nil for an empty flag.
func TestParseIDAndRefTo(t *testing.T) {
	for _, in := range []string{goodTask, "  " + goodTask + "\t"} {
		if id, err := ParseID("task", ids.PrefixTask, in); err != nil || id.String() != goodTask {
			t.Errorf("ParseID(%q) = %v %v", in, id, err)
		}
		if v, err := RefTo("task", "after", ids.PrefixTask, in); err != nil || v == nil || v.String() != goodTask {
			t.Errorf("RefTo(%q) = %v %v", in, v, err)
		}
	}
	bad := []string{"", "xyz", "mem_01a1a5aca7f57659be7320c5f4b70011", "tsk_01a1a5", "TSK_01a1a5aca7f57659be7320c5f4b70011", strings.Repeat("x", 200)}
	for _, in := range bad {
		if _, err := ParseID("task", ids.PrefixTask, in); !errs.Has(err, errs.InvalidID) {
			t.Errorf("ParseID(%q): %v", in, err)
		}
		if in == "" {
			continue
		}
		if _, err := RefTo("task", "after", ids.PrefixTask, in); !errs.Has(err, errs.InvalidID) || !strings.Contains(err.Error(), "--after") {
			t.Errorf("RefTo(%q): %v", in, err)
		}
	}
	if v, err := RefTo("task", "mission", ids.PrefixMission, "   "); v != nil || err != nil {
		t.Errorf("blank RefTo = %v %v", v, err)
	}
}

// TestOneOfTextsDate covers the closed-set, text and date helpers.
func TestOneOfTextsDate(t *testing.T) {
	set := []string{"low", "high"}
	if err := OneOf("task", "priority", "low", set); err != nil {
		t.Error(err)
	}
	for _, v := range []string{"", "LOW", "huge", " low"} {
		if err := OneOf("task", "priority", v, set); !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "low | high") {
			t.Errorf("OneOf(%q): %v", v, err)
		}
	}
	m, err := Texts("task", map[string]string{"a": "x", "b": "", "c": "日本"})
	if err != nil || len(m) != 2 || m["a"].String() != "x" || m["c"].String() != "日本" {
		t.Errorf("Texts = %v %v", m, err)
	}
	if v, err := Date("due", " 2026-12-01 "); err != nil || v.(value.DateTime).Time() != time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC) {
		t.Errorf("Date = %v %v", v, err)
	}
	for _, in := range []string{"", "2026-13-01", "2026-02-30", "01/12/2026", "2026-12-01T00:00:00Z"} {
		if _, err := Date("due", in); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("Date(%q): %v", in, err)
		}
	}
}

// TestFieldReaders: Field/Time/DateText/TimeText/Int/List over absent,
// null, typed and text-encoded values.
func TestFieldReaders(t *testing.T) {
	dt := value.DateTimeFrom(time.Date(2026, 12, 1, 10, 30, 0, 0, time.UTC))
	txt, err := value.TextOf("2026-12-01T10:30:00Z")
	if err != nil {
		t.Fatal(err)
	}
	junk, err := value.TextOf("soon")
	if err != nil {
		t.Fatal(err)
	}
	half, err := value.NumberOf("1.5")
	if err != nil {
		t.Fatal(err)
	}
	a, err := value.TextOf("a")
	if err != nil {
		t.Fatal(err)
	}
	list, err := value.ListOf(value.KindText, a, a)
	if err != nil {
		t.Fatal(err)
	}
	r := row.Row{Fields: map[string]value.Value{"dt": dt, "txt": txt, "junk": junk, "null": value.Null{}, "n": value.Int(7), "half": half, "l": list}}
	if Field(r, "null") != "" || Field(r, "absent") != "" || Field(r, "n") != "7" {
		t.Error("Field")
	}
	for _, f := range []string{"dt", "txt"} {
		if DateText(r, f) != "2026-12-01" || TimeText(r, f) != "2026-12-01T10:30:00Z" {
			t.Errorf("%s: %q %q", f, DateText(r, f), TimeText(r, f))
		}
	}
	for _, f := range []string{"junk", "null", "absent", "n"} {
		if !Time(r, f).IsZero() || DateText(r, f) != "" {
			t.Errorf("%s should read as no time", f)
		}
	}
	if Int(r, "n") != 7 || Int(r, "half") != 0 || Int(r, "absent") != 0 || Int(r, "junk") != 0 {
		t.Error("Int")
	}
	if got := List(r, "l"); len(got) != 2 || got[0] != "a" || List(r, "absent") != nil || List(r, "null") != nil {
		t.Errorf("List = %v", got)
	}
	// a scalar is a one-item list (value.ItemStrings: a list field written
	// as a single value by hand or by an old writer keeps its value).
	if got := List(r, "n"); len(got) != 1 || got[0] != "7" {
		t.Errorf("List(scalar) = %v", got)
	}
}

// TestInScope is v0.1.17's repo scope matrix: row repo (master, X, Y) ×
// flags (none, --repo X, --repo X --no-inherit, --master-only,
// --all-repos).
func TestInScope(t *testing.T) {
	type flags struct {
		want                         string
		noInherit, masterOnly, allRs bool
	}
	cases := []struct {
		name string
		f    flags
		keep map[string]bool // row repo → kept
	}{
		{"none", flags{}, map[string]bool{"": true, "X": false, "Y": false}},
		{"repo X", flags{want: "X"}, map[string]bool{"": true, "X": true, "Y": false}},
		{"repo X no-inherit", flags{want: "X", noInherit: true}, map[string]bool{"": false, "X": true, "Y": false}},
		{"no-inherit without repo", flags{noInherit: true}, map[string]bool{"": true, "X": false, "Y": false}},
		{"master-only", flags{masterOnly: true, want: "X"}, map[string]bool{"": true, "X": false, "Y": false}},
		{"all-repos", flags{allRs: true, want: "X", noInherit: true}, map[string]bool{"": true, "X": true, "Y": true}},
	}
	for _, c := range cases {
		for got, want := range c.keep {
			if InScope(got, c.f.want, c.f.noInherit, c.f.masterOnly, c.f.allRs) != want {
				t.Errorf("%s: row repo %q kept=%v, want %v", c.name, got, !want, want)
			}
		}
	}
}

// TestLimit: 0 is the default, 1..1000 kept, anything else refused.
func TestLimit(t *testing.T) {
	for in, want := range map[int]int{0: DefaultSearchLimit, 1: 1, 1000: 1000} {
		if got, err := Limit(in); err != nil || got != want {
			t.Errorf("Limit(%d) = %d %v", in, got, err)
		}
	}
	for _, in := range []int{-1, 1001, 1 << 30} {
		if _, err := Limit(in); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("Limit(%d): %v", in, err)
		}
	}
}

// fakeT is a Team for the context helpers.
type fakeT struct{}

func (fakeT) ResolveHandle(context.Context, string, bool) (Assignee, error) { return Assignee{}, nil }
func (fakeT) Claims(context.Context) (ClaimBoard, error)                    { return nil, nil }

// TestTeamContextAndHandles: TeamFrom finds only a Team put there by
// WithTeam (a nil Team counts as none); IsHandle trims.
func TestTeamContextAndHandles(t *testing.T) {
	if _, ok := TeamFrom(context.Background()); ok {
		t.Error("team in an empty context")
	}
	if _, ok := TeamFrom(WithTeam(context.Background(), nil)); ok {
		t.Error("nil team counted")
	}
	if _, ok := TeamFrom(WithTeam(context.Background(), fakeT{})); !ok {
		t.Error("team lost")
	}
	for in, want := range map[string]bool{"@ann": true, "  @ann": true, "ann": false, "": false, "a@b": false, "@": true} {
		if IsHandle(in) != want {
			t.Errorf("IsHandle(%q) != %v", in, want)
		}
	}
}

// TestSourceName: the repo store has no _source; others name themselves.
func TestSourceName(t *testing.T) {
	if SourceName(store.SourceRepo) != "" || SourceName("agency") != "agency" || SourceName("") != "" {
		t.Error("SourceName")
	}
}
