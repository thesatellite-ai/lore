package lint

// later 442: long_span, time_entries_many (ManyTimeEntries = 300) and
// meta_unknown_key, each over the rows lint reads.

import (
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/timeentry"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// taskWithEntries is a task row holding `live` live entries, `dead`
// deleted ones and, per flag, one more live (or deleted) entry flagged long_span.
func taskWithEntries(t *testing.T, n byte, live, dead int, longLive, longDead bool) sourced {
	t.Helper()
	actor, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	entry := func(deleted bool, flags ...string) timeentry.Entry {
		eid, eerr := ids.New[timeentry.Tag]()
		if eerr != nil {
			t.Fatal(eerr)
		}
		e := timeentry.Entry{ID: eid, Kind: timeentry.KindPerson, Source: timeentry.SourceManual, ActorID: actor,
			Seconds: 60, Flags: flags, CreatedAt: "2026-11-05T10:00:00Z"}
		if deleted {
			e.DeletedAt = "2026-11-05T11:00:00Z"
		}
		return e
	}
	var list []timeentry.Entry
	for range live {
		list = append(list, entry(false))
	}
	for range dead {
		list = append(list, entry(true))
	}
	if longLive {
		list = append(list, entry(false, timeentry.FlagLongSpan))
	}
	if longDead {
		list = append(list, entry(true, timeentry.FlagLongSpan))
	}
	raw, err := timeentry.Encode(list)
	if err != nil {
		t.Fatal(err)
	}
	j, err := value.JSONOf(raw)
	if err != nil {
		t.Fatal(err)
	}
	return sourced{src: store.SourceRepo, r: row.Row{ID: id(t, ids.PrefixTask, n),
		Fields: map[string]value.Value{"title": tx(t, "t"), task.FieldTimeEntries: j}}}
}

// TestTaskTimeFindings: entry count x deleted entries x long_span flag.
// 300 live entries is the line (deleted entries never count), a long_span
// flag on a live entry is one finding naming the entry with its fix, on a
// deleted entry none.
func TestTaskTimeFindings(t *testing.T) {
	cases := []struct {
		name               string
		live, dead         int
		longLive, longDead bool
		want               map[Code]int
	}{
		{"no entries", 0, 0, false, false, map[Code]int{}},
		{"299 live", ManyTimeEntries - 1, 0, false, false, map[Code]int{}},
		{"300 live", ManyTimeEntries, 0, false, false, map[Code]int{TimeEntriesMany: 1}},
		{"301 live", ManyTimeEntries + 1, 0, false, false, map[Code]int{TimeEntriesMany: 1}},
		{"299 live + 5 deleted", ManyTimeEntries - 1, 5, false, false, map[Code]int{}},
		{"299 live + long live", ManyTimeEntries - 1, 0, true, false, map[Code]int{TimeEntriesMany: 1, LongSpan: 1}},
		{"long live only", 0, 0, true, false, map[Code]int{LongSpan: 1}},
		{"long deleted only", 0, 0, false, true, map[Code]int{}},
	}
	for i, c := range cases {
		s := taskWithEntries(t, byte(i+1), c.live, c.dead, c.longLive, c.longDead)
		got := taskFindings([]sourced{s})
		if gc := codes(got); !sameCodes(gc, c.want) {
			t.Errorf("%s: codes %v, want %v", c.name, gc, c.want)
		}
		for _, f := range got {
			tid := s.r.ID.String()
			switch f.Code {
			case LongSpan:
				if f.ID != tid || f.Store != "repo" || !strings.HasPrefix(f.Fix, "lore task time edit "+tid+" ") || !strings.Contains(f.Message, "counted in full") {
					t.Errorf("%s: long_span finding %+v", c.name, f)
				}
			case TimeEntriesMany:
				if f.ID != tid || f.Fix != "split the task: lore task split "+tid || !strings.HasPrefix(f.Message, strconv.Itoa(c.live+boolInt(c.longLive))+" time entries") {
					t.Errorf("%s: time_entries_many finding %+v", c.name, f)
				}
			default:
				// sameCodes above already refused any other code.
			}
		}
	}
}

// TestMetaUnknownKey: a row's meta entry whose key id no attached store's
// meta_keys defines is one finding naming the row, its store and the fix;
// a key defined in any attached store (another store too) is none.
func TestMetaUnknownKey(t *testing.T) {
	known, err := ids.Parse[ids.MetaKey](id(t, ids.PrefixMetaKey, 1).String())
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := ids.Parse[ids.MetaKey](id(t, ids.PrefixMetaKey, 2).String())
	if err != nil {
		t.Fatal(err)
	}
	entry, err := value.JSONOf([]byte(`{"name":"client","scope":"repo","v":"acme"}`))
	if err != nil {
		t.Fatal(err)
	}
	keyRow := sourced{src: store.Source("team"), r: row.Row{ID: known.Any(), Fields: map[string]value.Value{"name": tx(t, "client")}}}
	mem := func(n byte, keys ...ids.ID[ids.MetaKey]) sourced {
		m := map[ids.ID[ids.MetaKey]]value.JSON{}
		for _, k := range keys {
			m[k] = entry
		}
		return sourced{src: store.SourceRepo, r: row.Row{ID: id(t, ids.PrefixMemory, n), Meta: m, Fields: map[string]value.Value{"body": tx(t, "b")}}}
	}
	cases := []struct {
		name string
		rows []sourced
		want int
	}{
		{"no meta", []sourced{mem(3)}, 0},
		{"known key in another store", []sourced{mem(4, known)}, 0},
		{"unknown key", []sourced{mem(5, unknown)}, 1},
		{"known and unknown", []sourced{mem(6, known, unknown)}, 1},
		{"two rows unknown", []sourced{mem(7, unknown), mem(8, unknown)}, 2},
	}
	for _, c := range cases {
		all := map[row.Table][]sourced{schema.TableMetaKeys: {keyRow}, schema.TableMemories: c.rows}
		got := metaFindings(all)
		if len(got) != c.want {
			t.Fatalf("%s: %d findings %+v, want %d", c.name, len(got), got, c.want)
		}
		for _, f := range got {
			if f.Code != MetaUnknownKey || f.Store != "repo" || !strings.Contains(f.Message, unknown.String()) ||
				f.Fix != "attach the store that defines it, or lore meta unset "+f.ID+" <name>" {
				t.Errorf("%s: finding %+v", c.name, f)
			}
		}
	}
	// no attached registry at all: every key is unknown
	if got := metaFindings(map[row.Table][]sourced{schema.TableMemories: {mem(9, known)}}); len(got) != 1 {
		t.Errorf("no registry: %+v", got)
	}
}

func sameCodes(a, b map[Code]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
