package task

import (
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/timeentry"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// span builds a closed entry of kind lasting d.
func span(t *testing.T, kind timeentry.Kind, d time.Duration) timeentry.Entry {
	t.Helper()
	actor, err := ids.Parse[ids.Actor]("act_0e45506e64357c94a87e70ef1c6bbba9")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	src := timeentry.SourceRun
	if kind == timeentry.KindManual {
		src = timeentry.SourceManual
	}
	e, err := timeentry.NewSpan(timeentry.Span{Kind: kind, Source: src, Actor: actor, Start: start, End: start.Add(d)})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// TestTimeEntriesReadsEveryShape: absent, null, value.JSON (engine-built),
// a decoded generic list (row file) and garbage (no entries, never an
// error).
func TestTimeEntriesReadsEveryShape(t *testing.T) {
	e := span(t, timeentry.KindAgent, time.Hour)
	raw, err := timeentry.Encode([]timeentry.Entry{e})
	if err != nil {
		t.Fatal(err)
	}
	j, err := value.JSONOf(raw)
	if err != nil {
		t.Fatal(err)
	}
	inferred, err := value.Infer(raw)
	if err != nil {
		t.Fatal(err)
	}
	notList, err := value.TextOf("not a list")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		v    value.Value
		want int
	}{
		{"absent", nil, 0},
		{"null", value.Null{}, 0},
		{"json", j, 1},
		{"decoded list", inferred, 1},
		{"text", notList, 0},
	}
	for _, c := range cases {
		r := row.Row{Fields: map[string]value.Value{}}
		if c.v != nil {
			r.Fields[FieldTimeEntries] = c.v
		}
		got := TimeEntries(r)
		if len(got) != c.want {
			t.Errorf("%s: %d entries, want %d", c.name, len(got), c.want)
		}
		if c.want == 1 && (got[0].ID != e.ID || got[0].Seconds != 3600) {
			t.Errorf("%s: %+v", c.name, got[0])
		}
	}
}

// TestWithTimeEntryAppends: the patch value is the old list plus the new
// entry, ids kept, on a row with none and on a row with one.
func TestWithTimeEntryAppends(t *testing.T) {
	a, b := span(t, timeentry.KindAgent, time.Minute), span(t, timeentry.KindPerson, 2*time.Minute)
	r := row.Row{Fields: map[string]value.Value{}}
	v, err := WithTimeEntry(r, a)
	if err != nil {
		t.Fatal(err)
	}
	r.Fields[FieldTimeEntries] = v
	v, err = WithTimeEntry(r, b)
	if err != nil {
		t.Fatal(err)
	}
	r.Fields[FieldTimeEntries] = v
	got := TimeEntries(r)
	if len(got) != 2 || got[0].ID != a.ID || got[1].ID != b.ID {
		t.Errorf("append: %+v", got)
	}
}

// TestTimeLine: the show line per kind mix; tombstoned entries do not
// count and a list of only tombstones prints nothing.
func TestTimeLine(t *testing.T) {
	agent, person, manual := span(t, timeentry.KindAgent, 90*time.Minute), span(t, timeentry.KindPerson, 30*time.Second), span(t, timeentry.KindManual, 5*time.Minute)
	dead := span(t, timeentry.KindAgent, 10*time.Hour)
	dead.DeletedAt = "2026-11-04T12:00:00.000Z"
	cases := []struct {
		name string
		list []timeentry.Entry
		want string
	}{
		{"none", nil, ""},
		{"only tombstones", []timeentry.Entry{dead}, ""},
		{"agent", []timeentry.Entry{agent}, "1h 30m (agent 1h 30m, person 0m)"},
		{"agent + person", []timeentry.Entry{agent, person}, "1h 30m (agent 1h 30m, person 30s)"},
		{"with manual", []timeentry.Entry{agent, manual}, "1h 35m (agent 1h 30m, person 0m, manual 5m)"},
		{"tombstone skipped", []timeentry.Entry{agent, dead}, "1h 30m (agent 1h 30m, person 0m)"},
	}
	for _, c := range cases {
		if got := TimeLine(c.list); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// TestBriefLine: the v0.1.17 list line over status × due × commitment ×
// deferral × store label, and the STATE column placement.
func TestBriefLine(t *testing.T) {
	id, err := ids.ParseAny("tsk_01a1a5aca7f57659be7320c5f4b70011")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(kv map[string]string) row.Row {
		r := row.Row{ID: id, Fields: map[string]value.Value{}}
		for k, s := range kv {
			var v value.Value
			switch k {
			case fieldDue, fieldDeferred:
				v, err = value.DateTimeOf(s + "T00:00:00Z")
			default:
				v, err = value.TextOf(s)
			}
			if err != nil {
				t.Fatal(err)
			}
			r.Fields[k] = v
		}
		return r
	}
	const tid = "tsk_01a1a5aca7f57659be7320c5f4b70011"
	cases := []struct {
		name  string
		src   store.Source
		kv    map[string]string
		state string
		want  string
	}{
		{"bare (defaults)", store.SourceRepo, map[string]string{"title": "T"}, "", "○ " + tid + " T [medium]"},
		{"done", store.SourceRepo, map[string]string{"title": "T", "status": "done", "priority": "high"}, "", "✓ " + tid + " T [high]"},
		{"in progress", store.SourceRepo, map[string]string{"title": "T", "status": "in_progress"}, "", "▶ " + tid + " T [medium]"},
		{"cancelled", store.SourceRepo, map[string]string{"title": "T", "status": "cancelled"}, "", "✗ " + tid + " T [medium]"},
		{"blocked", store.SourceRepo, map[string]string{"title": "T", "status": "blocked"}, "", "⛔ " + tid + " T [medium]"},
		{"unknown status", store.SourceRepo, map[string]string{"title": "T", "status": "weird"}, "", "○ " + tid + " T [medium]"},
		{"everything", "agency", map[string]string{"title": "T", "due_at": "2026-12-01", "commitment": "someday", "deferred_until": "2027-01-05"}, "",
			"○ " + tid + " T [medium] due:2026-12-01 (someday) ⏾2027-01-05  [agency]"},
		{"accepted chip hidden", store.SourceRepo, map[string]string{"title": "T", "commitment": "accepted"}, "", "○ " + tid + " T [medium]"},
		{"state + label", "agency", map[string]string{"title": "T"}, "EXPIRED", "○ " + tid + " T [medium]  EXPIRED  [agency]"},
		{"empty state", store.SourceRepo, map[string]string{"title": "T"}, "", "○ " + tid + " T [medium]"},
		{"unicode title", store.SourceRepo, map[string]string{"title": "日本語 🚀"}, "", "○ " + tid + " 日本語 🚀 [medium]"},
	}
	for _, c := range cases {
		b := withState(BriefOf(c.src, mk(c.kv)), c.state)
		if b.Line != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, b.Line, c.want)
		}
	}
}
