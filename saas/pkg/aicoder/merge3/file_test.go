package merge3

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"saas/pkg/aicoder/canonjson"
)

// Row fixtures: a base task and edits that touch different or the same
// fields. Status is newest-wins, title is text (conflict), labels is a set.
var (
	baseRow  = obj("id", "tsk_02", "title", "fix login", "status", "todo", "labels", []any{"auth"})
	editA    = obj("id", "tsk_02", "title", "fix login", "status", "done", "labels", []any{"auth"})         // status only
	editB    = obj("id", "tsk_02", "title", "fix login timeout", "status", "todo", "labels", []any{"auth"}) // title only
	editC    = obj("id", "tsk_02", "title", "fix the login", "status", "in_progress", "labels", []any{"auth", "ui"})
	tombDoc1 = obj("id", "tsk_02", "_live", obj("deleted", true, "hlc", "0005"))
	tombDoc2 = obj("id", "tsk_02", "_live", obj("deleted", true, "hlc", "0007"))
)

func filePolicy(newer Side) Policy {
	return Policy{StrategyFor: strategies(map[string]Strategy{"status": StrategyNewest, "labels": StrategySet}), Newer: newer}
}

func TestMergeFile_Contract(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                string
		base, ours, theirs  File
		wantKind            FileKind
		wantDoc             map[string]any // nil: not checked here
		wantDecision        FileDecision
		wantDeletedBy       Side
		wantConflict        bool
		wantDocFromMergeNot bool
	}{
		{name: "added on ours only", base: Absent(), ours: Row(editA), theirs: Absent(), wantKind: FileRow, wantDoc: editA, wantDecision: DecisionOursOnly},
		{name: "added on theirs only", base: Absent(), ours: Absent(), theirs: Row(editB), wantKind: FileRow, wantDoc: editB, wantDecision: DecisionTheirsOnly},
		{name: "added on both, same", base: Absent(), ours: Row(editA), theirs: Row(editA), wantKind: FileRow, wantDoc: editA, wantDecision: DecisionSame},
		{name: "added on both, different → merge with empty base", base: Absent(), ours: Row(editA), theirs: Row(editB),
			wantKind: FileRow, wantDecision: DecisionFieldMerge, wantConflict: true},
		{name: "tombstone on ours, theirs unchanged → deleted", base: Row(baseRow), ours: Tombstone(tombDoc1), theirs: Row(baseRow),
			wantKind: FileTombstone, wantDoc: tombDoc1, wantDecision: DecisionOursOnly},
		{name: "removed on theirs, ours unchanged → deleted", base: Row(baseRow), ours: Row(baseRow), theirs: Absent(),
			wantKind: FileAbsent, wantDecision: DecisionTheirsOnly},
		{name: "tombstone on ours, edit on theirs → modify wins", base: Row(baseRow), ours: Tombstone(tombDoc1), theirs: Row(editB),
			wantKind: FileRow, wantDoc: editB, wantDecision: DecisionModifyBeatsDelete, wantDeletedBy: SideOurs},
		{name: "edit on ours, removed on theirs → modify wins", base: Row(baseRow), ours: Row(editA), theirs: Absent(),
			wantKind: FileRow, wantDoc: editA, wantDecision: DecisionModifyBeatsDelete, wantDeletedBy: SideTheirs},
		{name: "added on ours, tombstone added on theirs → row kept", base: Absent(), ours: Row(editA), theirs: Tombstone(tombDoc1),
			wantKind: FileRow, wantDoc: editA, wantDecision: DecisionModifyBeatsDelete, wantDeletedBy: SideTheirs},
		{name: "both edited different fields → clean field merge", base: Row(baseRow), ours: Row(editA), theirs: Row(editB),
			wantKind: FileRow, wantDoc: obj("id", "tsk_02", "title", "fix login timeout", "status", "done", "labels", []any{"auth"}), wantDecision: DecisionFieldMerge},
		{name: "both edited the title → markers", base: Row(baseRow), ours: Row(editB), theirs: Row(editC),
			wantKind: FileRow, wantDecision: DecisionFieldMerge, wantConflict: true},
		{name: "tombstone vs removed → tombstone", base: Row(baseRow), ours: Absent(), theirs: Tombstone(tombDoc1),
			wantKind: FileTombstone, wantDoc: tombDoc1, wantDecision: DecisionBothDeleted},
		{name: "two tombstones → newer side's (theirs)", base: Row(baseRow), ours: Tombstone(tombDoc1), theirs: Tombstone(tombDoc2),
			wantKind: FileTombstone, wantDoc: tombDoc2, wantDecision: DecisionBothDeleted},
		{name: "resurrected on both from a tombstone → merge with empty base", base: Tombstone(tombDoc1), ours: Row(editA), theirs: Row(editA),
			wantKind: FileRow, wantDoc: editA, wantDecision: DecisionSame},
		// A tombstone that still carries the row's fields must not act as
		// the base of a resurrection: both sides re-adding is an add/add,
		// so a clash on title stays a visible conflict (empty base).
		{name: "resurrected differently from a full tombstone → add/add conflict",
			base:     Tombstone(obj("id", "tsk_02", "title", "x", "status", "todo", "_live", obj("deleted", true))),
			ours:     Row(obj("id", "tsk_02", "title", "x", "status", "done")),
			theirs:   Row(obj("id", "tsk_02", "title", "y", "status", "todo")),
			wantKind: FileRow, wantDecision: DecisionFieldMerge, wantConflict: true},
		{name: "all absent", base: Absent(), ours: Absent(), theirs: Absent(), wantKind: FileAbsent, wantDecision: DecisionSame},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := MergeFile(tc.base, tc.ours, tc.theirs, filePolicy(SideTheirs))
			if err != nil {
				t.Fatal(err)
			}
			if res.File.Kind != tc.wantKind || res.Decision != tc.wantDecision {
				t.Fatalf("got %v/%v want %v/%v", res.File.Kind, res.Decision, tc.wantKind, tc.wantDecision)
			}
			if tc.wantDecision == DecisionModifyBeatsDelete && res.DeletedBy != tc.wantDeletedBy {
				t.Fatalf("DeletedBy = %v", res.DeletedBy)
			}
			if res.Conflicted() != tc.wantConflict {
				t.Fatalf("conflicted = %v (%+v)", res.Conflicted(), res.Fields.Conflicts)
			}
			if tc.wantDoc != nil && !reflect.DeepEqual(res.File.Doc, tc.wantDoc) {
				t.Fatalf("doc = %v want %v", res.File.Doc, tc.wantDoc)
			}
		})
	}
}

// sideState is one value a side (or base) can hold in the sweep.
type sideState struct {
	name string
	file File
}

func states() []sideState {
	return []sideState{
		{"absent", Absent()},
		{"base", Row(baseRow)},
		{"editA", Row(editA)},
		{"editB", Row(editB)},
		{"editC", Row(editC)},
		{"tomb1", Tombstone(tombDoc1)},
		{"tomb2", Tombstone(tombDoc2)},
	}
}

func canon(t *testing.T, f File) string {
	t.Helper()
	if f.Kind == FileAbsent {
		return "absent"
	}
	s, err := canonjson.CompactValue(f.Doc)
	if err != nil {
		t.Fatal(err)
	}
	return f.Kind.String() + ":" + s
}

// TestMergeFile_Sweep checks properties over every base × ours × theirs ×
// newer-side combination instead of pinning outputs:
//   - no row edit is ever lost: if a side holds a row that differs from
//     base, the result is a row;
//   - one-sided changes win exactly; identical sides pass through;
//   - the result is a whole file: a row result's keys all come from the
//     inputs, and each value is some input's value or a conflict marker;
//   - swapping ours/theirs (and the newer side) gives the same kind and,
//     when clean, the same content.
func TestMergeFile_Sweep(t *testing.T) {
	t.Parallel()
	for _, b := range states() {
		for _, o := range states() {
			for _, th := range states() {
				for _, newer := range []Side{SideOurs, SideTheirs} {
					name := fmt.Sprintf("b=%s/o=%s/t=%s/newer=%d", b.name, o.name, th.name, newer)
					res, err := MergeFile(b.file, o.file, th.file, filePolicy(newer))
					if err != nil {
						t.Fatalf("%s: %v", name, err)
					}
					cb, co, ct, cr := canon(t, b.file), canon(t, o.file), canon(t, th.file), canon(t, res.File)
					switch {
					case co == ct && cr != co:
						t.Errorf("%s: identical sides changed to %s", name, cr)
					case co == cb && cr != ct:
						t.Errorf("%s: only theirs changed, got %s", name, cr)
					case ct == cb && cr != co:
						t.Errorf("%s: only ours changed, got %s", name, cr)
					}
					edited := (o.file.Kind == FileRow && co != cb) || (th.file.Kind == FileRow && ct != cb)
					if edited && res.File.Kind != FileRow {
						t.Errorf("%s: an edit was lost (result %v)", name, res.File.Kind)
					}
					if res.File.Kind == FileRow {
						checkWhole(t, name, res.File.Doc, b.file, o.file, th.file)
					}
					other := SideOurs
					if newer == SideOurs {
						other = SideTheirs
					}
					sw, err := MergeFile(b.file, th.file, o.file, filePolicy(other))
					if err != nil {
						t.Fatal(err)
					}
					if sw.File.Kind != res.File.Kind {
						t.Errorf("%s: swapped sides give %v, not %v", name, sw.File.Kind, res.File.Kind)
					}
					if !res.Conflicted() && !sw.Conflicted() && res.File.Kind == FileRow {
						// Set order may differ (ours first); compare as sets.
						if !sameIgnoringSetOrder(res.File.Doc, sw.File.Doc) {
							t.Errorf("%s: swapped clean merge differs: %v vs %v", name, res.File.Doc, sw.File.Doc)
						}
					}
				}
			}
		}
	}
}

// checkWhole asserts every key of a merged row comes from an input and
// every value is an input's value, a set union of inputs, or a marker.
func checkWhole(t *testing.T, name string, doc map[string]any, inputs ...File) {
	t.Helper()
	for k, v := range doc {
		if s, ok := v.(string); ok && ContainsConflictMarkers(s) {
			continue
		}
		vs, _ := canonjson.CompactValue(v)
		found := false
		for _, in := range inputs {
			if in.Kind != FileRow {
				continue
			}
			if iv, ok := in.Doc[k]; ok {
				is, _ := canonjson.CompactValue(iv)
				if is == vs {
					found = true
				}
			}
		}
		if _, isList := v.([]any); isList {
			found = true // set merges may combine inputs; checked in TestStrategySet
		}
		if !found {
			t.Errorf("%s: key %s = %s comes from no input", name, k, vs)
		}
	}
}

func sameIgnoringSetOrder(a, b map[string]any) bool {
	norm := func(m map[string]any) string {
		c := map[string]any{}
		for k, v := range m {
			if l, ok := v.([]any); ok {
				parts := make([]string, 0, len(l))
				for _, e := range l {
					s, _ := canonjson.CompactValue(e)
					parts = append(parts, s)
				}
				sortStrings(parts)
				c[k] = strings.Join(parts, ",")
				continue
			}
			c[k] = v
		}
		s, _ := canonjson.CompactValue(c)
		return s
	}
	return norm(a) == norm(b)
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func TestMergeFile_RejectsInconsistentFile(t *testing.T) {
	t.Parallel()
	if _, err := MergeFile(File{Kind: FileRow}, Absent(), Absent(), Policy{}); err == nil {
		t.Fatal("row without doc accepted")
	}
	if _, err := MergeFile(Absent(), File{Kind: FileAbsent, Doc: obj()}, Absent(), Policy{}); err == nil {
		t.Fatal("absent with doc accepted")
	}
}

func TestStrategySet(t *testing.T) {
	t.Parallel()
	pol := Policy{StrategyFor: func(string) Strategy { return StrategySet }}
	l := func(v ...any) []any { return v }
	link := func(p string) map[string]any { return obj("project", p) }
	cases := []struct {
		name          string
		base, o, th   any // nil base = key absent in base
		want          any
		wantConflicts int
	}{
		{"both add different", l("a"), l("a", "b"), l("a", "c"), l("a", "b", "c"), 0},
		{"both add same", l(), l("x"), l("x"), l("x"), 0},
		{"ours removes, theirs adds", l("a", "b"), l("b"), l("a", "b", "c"), l("b", "c"), 0},
		{"theirs removes, ours adds", l("a", "b"), l("a", "b", "c"), l("a"), l("a", "c"), 0},
		{"both remove different", l("a", "b", "c"), l("b", "c"), l("a", "c"), l("c"), 0},
		{"absent base = empty set", nil, l("a"), l("b"), l("a", "b"), 0},
		{"objects compare by content", l(link("acme")), l(link("acme"), link("web")), l(obj("project", "acme"), link("globex")), l(link("acme"), link("web"), link("globex")), 0},
		{"duplicates collapse", l(), l("a", "a"), l("b"), l("a", "b"), 0},
		{"non-array side → conflict", l("a"), "oops", l("a", "b"), nil, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var base map[string]any
			if tc.base != nil {
				base = obj("links", tc.base)
			} else {
				base = obj()
			}
			res, err := Merge(base, obj("links", tc.o), obj("links", tc.th), pol)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Conflicts) != tc.wantConflicts {
				t.Fatalf("conflicts = %v", res.Conflicts)
			}
			if tc.wantConflicts == 0 && !reflect.DeepEqual(res.Merged["links"], tc.want) {
				t.Fatalf("links = %v want %v", res.Merged["links"], tc.want)
			}
			sameChange := reflect.DeepEqual(tc.o, tc.th) // not a clash: nothing to resolve
			if tc.wantConflicts == 0 && !sameChange && !reflect.DeepEqual(res.AutoResolved, []string{"links"}) {
				t.Fatalf("auto = %v", res.AutoResolved)
			}
		})
	}
	// Deleted on one side, changed on the other: a real conflict.
	res, err := Merge(obj("links", l("a")), obj(), obj("links", l("a", "b")), pol)
	if err != nil || len(res.Conflicts) != 1 {
		t.Fatalf("delete vs change = %+v %v", res, err)
	}
}

func TestFileNames(t *testing.T) {
	t.Parallel()
	for k := FileAbsent; k <= FileTombstone; k++ {
		if strings.HasPrefix(k.String(), "kind(") {
			t.Errorf("kind %d unnamed", int(k))
		}
	}
	for d := DecisionSame; d <= DecisionBothDeleted; d++ {
		if strings.HasPrefix(d.String(), "decision(") {
			t.Errorf("decision %d unnamed", int(d))
		}
	}
	if FileKind(9).String() != "kind(9)" || FileDecision(9).String() != "decision(9)" {
		t.Error("unknown names")
	}
}
