package merge

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

var (
	t0      = time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	actorA  = mustActor("act_01936f0000007a1b8c2d3e4f5a6b7c8d")
	actorB  = mustActor("act_01936f0000007a1b8c2d3e4f5a6b7c8e")
	rowID   = mustAny("mem_01936f3300007d4e8f5a6b7c8d9e0f1a")
	testDef = schema.Table(schema.TableMemories, ids.PrefixMemory,
		schema.Text("kind", schema.OneOf(schema.MemoryKinds()...)),
		schema.Text("body"),
		schema.Number("rank"),
	)
)

func mustActor(s string) ids.ID[ids.Actor] {
	a, err := ids.Parse[ids.Actor](s)
	if err != nil {
		panic(err)
	}
	return a
}

func mustAny(s string) ids.AnyID {
	a, err := ids.ParseAny(s)
	if err != nil {
		panic(err)
	}
	return a
}

func stamp(ms int, a ids.ID[ids.Actor]) hlc.HLC {
	return hlc.HLC{Wall: t0.Add(time.Duration(ms) * time.Millisecond), Actor: a}
}

func text(s string) value.Value {
	v, err := value.TextOf(s)
	if err != nil {
		panic(err)
	}
	return v
}

// live builds a live memory row; kv alternates field name and value.
func live(h hlc.HLC, kv ...value.Value) row.Row {
	r := row.Row{Table: schema.TableMemories, ID: rowID, HLC: h, CreatedAt: t0, CreatedBy: actorA, Fields: map[string]value.Value{}}
	for i := 0; i+1 < len(kv); i += 2 {
		r.Fields[kv[i].String()] = kv[i+1]
	}
	return r
}

func tomb(h hlc.HLC) row.Row {
	return row.Row{Table: schema.TableMemories, ID: rowID, HLC: h, Deleted: true}
}

func field(r row.Row, k string) string {
	v, ok := r.Fields[k]
	if !ok {
		return "<absent>"
	}
	return v.String()
}

func TestRows(t *testing.T) {
	p := PolicyFor(testDef)
	base := live(stamp(0, actorA), text("kind"), text("core"), text("body"), text("hello"), text("rank"), value.Int(1))
	cases := []struct {
		name          string
		base          row.Row
		ours, theirs  row.Row
		want          map[string]string
		conflicts     []string
		newerWins     []string
		deleted       bool
		editAfterDel  bool
		markersInBody bool
	}{
		{
			name:   "one side changed a field: that side wins",
			base:   base,
			ours:   live(stamp(5, actorA), text("kind"), text("core"), text("body"), text("hello"), text("rank"), value.Int(2)),
			theirs: base,
			want:   map[string]string{"rank": "2", "body": "hello"},
		},
		{
			name:   "different fields on both sides: both kept",
			base:   base,
			ours:   live(stamp(5, actorA), text("kind"), text("core"), text("body"), text("hello"), text("rank"), value.Int(2)),
			theirs: live(stamp(6, actorB), text("kind"), text("episodic"), text("body"), text("hello"), text("rank"), value.Int(1)),
			want:   map[string]string{"rank": "2", "kind": "episodic"},
		},
		{
			name:      "both changed a closed-set text field: higher HLC wins, no conflict",
			base:      base,
			ours:      live(stamp(9, actorA), text("kind"), text("archival"), text("body"), text("hello"), text("rank"), value.Int(1)),
			theirs:    live(stamp(6, actorB), text("kind"), text("episodic"), text("body"), text("hello"), text("rank"), value.Int(1)),
			want:      map[string]string{"kind": "archival"},
			newerWins: []string{"kind"},
		},
		{
			name:      "both changed a number: higher HLC wins",
			base:      base,
			ours:      live(stamp(5, actorA), text("kind"), text("core"), text("body"), text("hello"), text("rank"), value.Int(7)),
			theirs:    live(stamp(6, actorB), text("kind"), text("core"), text("body"), text("hello"), text("rank"), value.Int(8)),
			want:      map[string]string{"rank": "8"},
			newerWins: []string{"rank"},
		},
		{
			name:          "both changed free text: markers and a conflict",
			base:          base,
			ours:          live(stamp(5, actorA), text("kind"), text("core"), text("body"), text("ours text"), text("rank"), value.Int(1)),
			theirs:        live(stamp(6, actorB), text("kind"), text("core"), text("body"), text("theirs text"), text("rank"), value.Int(1)),
			conflicts:     []string{"body"},
			markersInBody: true,
		},
		{
			name:   "one side removed a field, other untouched: removed",
			base:   base,
			ours:   live(stamp(5, actorA), text("kind"), text("core"), text("body"), text("hello")),
			theirs: base,
			want:   map[string]string{"rank": "<absent>"},
		},
		{
			name:   "no base, both added the same row with different fields: union",
			base:   Absent,
			ours:   live(stamp(5, actorA), text("body"), text("hello")),
			theirs: live(stamp(6, actorB), text("rank"), value.Int(3)),
			want:   map[string]string{"body": "hello", "rank": "3"},
		},
		{
			name:    "tombstone newer than the edit wins",
			base:    base,
			ours:    tomb(stamp(9, actorA)),
			theirs:  live(stamp(5, actorB), text("body"), text("edited")),
			deleted: true,
		},
		{
			name:         "edit newer than the tombstone: tombstone kept and reported",
			base:         base,
			ours:         tomb(stamp(5, actorA)),
			theirs:       live(stamp(9, actorB), text("body"), text("edited")),
			deleted:      true,
			editAfterDel: true,
		},
		{
			name:    "two tombstones: the earlier one (DEL-TOMB2)",
			base:    base,
			ours:    tomb(stamp(5, actorA)),
			theirs:  tomb(stamp(9, actorB)),
			deleted: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Rows(p, tc.base, tc.ours, tc.theirs)
			if err != nil {
				t.Fatal(err)
			}
			if res.Row.Deleted != tc.deleted {
				t.Fatalf("deleted = %v, want %v", res.Row.Deleted, tc.deleted)
			}
			if got := !res.EditAfterDelete.IsZero(); got != tc.editAfterDel {
				t.Fatalf("edit after delete reported = %v, want %v", got, tc.editAfterDel)
			}
			for k, want := range tc.want {
				if got := field(res.Row, k); got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
			var conf []string
			for _, c := range res.Conflicts {
				conf = append(conf, c.Field)
			}
			if strings.Join(conf, ",") != strings.Join(tc.conflicts, ",") {
				t.Errorf("conflicts = %v, want %v", conf, tc.conflicts)
			}
			if strings.Join(res.NewerWins, ",") != strings.Join(tc.newerWins, ",") {
				t.Errorf("newer wins = %v, want %v", res.NewerWins, tc.newerWins)
			}
			if tc.markersInBody {
				body := field(res.Row, "body")
				if !HasMarkers(body) || !strings.Contains(body, "ours text") || !strings.Contains(body, "theirs text") {
					t.Errorf("body lost a side or has no markers: %q", body)
				}
				if strings.Index(body, "theirs text") > strings.Index(body, "ours text") {
					t.Errorf("newer side (theirs) must come first: %q", body)
				}
			}
			if !tc.deleted {
				if res.Row.HLC.Compare(hlc.Max(tc.ours.HLC, tc.theirs.HLC)) != 0 {
					t.Errorf("hlc = %s, want the max of both sides", res.Row.HLC)
				}
				if _, err := row.Encode(res.Row); err != nil {
					t.Errorf("merged row does not encode: %v", err)
				}
			}
		})
	}
}

func TestRowsRefusesDifferentRows(t *testing.T) {
	other := live(stamp(1, actorA))
	other.ID = mustAny("mem_01936f3300007d4e8f5a6b7c8d9e0f1b")
	if _, err := Rows(PolicyFor(testDef), Absent, live(stamp(1, actorA)), other); err == nil {
		t.Fatal("merged two different rows")
	}
}

func TestUnknownTableKeepsStringClashesLoud(t *testing.T) {
	base := live(stamp(0, actorA), text("kind"), text("core"))
	res, err := Rows(UnknownTable(), base,
		live(stamp(5, actorA), text("kind"), text("episodic")),
		live(stamp(6, actorB), text("kind"), text("archival")))
	if err != nil {
		t.Fatal(err)
	}
	if res.Clean() {
		t.Fatal("unknown table settled a string clash silently")
	}
}

func TestDriverMergesBytes(t *testing.T) {
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	enc := func(r row.Row) []byte { return encode(t, r) }
	base := live(stamp(0, actorA), text("body"), text("hello"), text("kind"), text("core"))
	ours := live(stamp(5, actorA), text("body"), text("hello"), text("kind"), text("episodic"))
	theirs := live(stamp(6, actorB), text("body"), text("hello world"), text("kind"), text("core"))
	var reported int
	d := Driver{Registry: reg, Report: func(Result) { reported++ }}
	out, conflict, err := d.Merge(enc(base), enc(ours), enc(theirs))
	if err != nil || conflict {
		t.Fatalf("merge: conflict=%v err=%v", conflict, err)
	}
	got, err := row.Decode(out)
	if err != nil {
		t.Fatal(err)
	}
	if field(got, "kind") != "episodic" || field(got, "body") != "hello world" || reported != 1 {
		t.Fatalf("got kind=%q body=%q reported=%d", field(got, "kind"), field(got, "body"), reported)
	}

	clash := live(stamp(7, actorA), text("body"), text("ours"), text("kind"), text("core"))
	out, conflict, err = d.Merge(nil, enc(clash), enc(theirs))
	if err != nil || !conflict {
		t.Fatalf("text clash: conflict=%v err=%v", conflict, err)
	}
	if !bytes.Contains(out, []byte(MarkerSplit)) {
		t.Fatal("no markers in the merged document")
	}
	if _, _, err := d.Merge(nil, []byte("not json"), enc(theirs)); err == nil {
		t.Fatal("merged a non-row")
	}
}

func TestHasMarkers(t *testing.T) {
	cases := map[string]bool{
		Mark(stamp(2, actorA), "a", stamp(1, actorB), "b"): true,
		"<<<<<<< HEAD\na\n=======\nb\n>>>>>>> x":           true,
		"plain text":                                       false,
		"=======\nonly a split":                            false,
	}
	for s, want := range cases {
		if got := HasMarkers(s); got != want {
			t.Errorf("HasMarkers(%q) = %v, want %v", s, got, want)
		}
	}
}

// FuzzRowsSymmetric: Rows is deterministic, and for every non-text field
// the outcome does not depend on which side is passed as ours.
func FuzzRowsSymmetric(f *testing.F) {
	f.Add("core", "episodic", "archival", int64(1), int64(2), int64(3), 5, 6, false, false)
	f.Add("core", "core", "core", int64(1), int64(1), int64(9), 6, 6, true, false)
	f.Add("a", "b", "c", int64(0), int64(4), int64(4), 1, 9, false, true)
	p := Policy{IsText: func(string) bool { return false }}
	f.Fuzz(func(t *testing.T, bk, ok, tk string, br, or, tr int64, oms, tms int, oDel, tDel bool) {
		mk := func(ms int, a ids.ID[ids.Actor], k string, r int64, del bool) row.Row {
			if del {
				return tomb(stamp(ms%1000, a))
			}
			tv, err := value.TextOf(strings.ToValidUTF8(k, "?"))
			if err != nil {
				t.Fatal(err)
			}
			return live(stamp(ms%1000, a), text("kind"), tv, text("rank"), value.Int(r))
		}
		oms, tms = abs(oms), abs(tms)
		base := mk(0, actorA, bk, br, false)
		ours := mk(oms, actorA, ok, or, oDel)
		theirs := mk(tms, actorB, tk, tr, tDel)
		r1, err := Rows(p, base, ours, theirs)
		if err != nil {
			t.Fatal(err)
		}
		r1b, err := Rows(p, base, ours, theirs)
		if err != nil {
			t.Fatal(err)
		}
		r2, err := Rows(p, base, theirs, ours)
		if err != nil {
			t.Fatal(err)
		}
		e1, e1b, e2 := encode(t, r1.Row), encode(t, r1b.Row), encode(t, r2.Row)
		if !bytes.Equal(e1, e1b) {
			t.Fatal("not deterministic")
		}
		if !bytes.Equal(e1, e2) {
			t.Fatalf("not symmetric:\n%s\n%s", e1, e2)
		}
	})
}

// abs folds a fuzzed int into [0, 1000) without overflowing on MinInt.
func abs(n int) int { return int(uint(n) % 1000) }

func encode(t *testing.T, r row.Row) []byte {
	t.Helper()
	b, err := row.Encode(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestRowsBothTombstonesKeepEarlier pins DEL-TOMB2 (DELETION 3.7, owner
// 2026-11-05): when both sides are tombstones the EARLIER one (lower HLC)
// is kept, in either argument order (this replaced "the newer one").
func TestRowsBothTombstonesKeepEarlier(t *testing.T) {
	t.Parallel()
	p := PolicyFor(testDef)
	early, late := tomb(stamp(5, actorA)), tomb(stamp(9, actorB))
	for _, pair := range [][2]row.Row{{early, late}, {late, early}} {
		res, err := Rows(p, Absent, pair[0], pair[1])
		if err != nil {
			t.Fatal(err)
		}
		if !res.Row.Deleted || res.Row.HLC != early.HLC {
			t.Fatalf("kept %v deleted=%v, want the earlier tombstone %v", res.Row.HLC, res.Row.Deleted, early.HLC)
		}
	}
}
