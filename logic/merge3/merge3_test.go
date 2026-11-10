package merge3

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/logic/canonjson"
)

// doc parses a JSON object for a test case (nil for "").
func doc(t *testing.T, s string) canonjson.Object {
	t.Helper()
	if s == "" {
		return nil
	}
	o, err := canonjson.Decode([]byte(s), canonjson.DefaultMaxDepth)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func compact(t *testing.T, o canonjson.Object) string {
	t.Helper()
	s, err := canonjson.CompactValue(canonjson.ObjectValue(o))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMergeMatrix(t *testing.T) {
	t.Parallel()
	pol := Policy{Newer: SideTheirs, StrategyFor: func(k string) Strategy {
		switch k {
		case "status":
			return StrategyNewest
		case "updated_at":
			return StrategyMax
		case "labels":
			return StrategySet
		}
		return StrategyConflict
	}}
	for _, tc := range []struct {
		name, base, ours, theirs, want string
		conflicts, auto                []string
	}{
		{"unchanged", `{"a":1}`, `{"a":1}`, `{"a":1}`, `{"a":1}`, nil, nil},
		{"ours only", `{"a":1}`, `{"a":2}`, `{"a":1}`, `{"a":2}`, nil, nil},
		{"theirs only delete", `{"a":1,"b":2}`, `{"a":1,"b":2}`, `{"a":1}`, `{"a":1}`, nil, nil},
		{"same change", `{"a":1}`, `{"a":3}`, `{"a":3}`, `{"a":3}`, nil, nil},
		{"null is not absent", `{"a":1}`, `{"a":null}`, `{"a":1}`, `{"a":null}`, nil, nil},
		{"newest theirs", `{"status":"todo"}`, `{"status":"doing"}`, `{"status":"done"}`, `{"status":"done"}`, nil, []string{"status"}},
		{"max string", `{"updated_at":"1"}`, `{"updated_at":"3"}`, `{"updated_at":"2"}`, `{"updated_at":"3"}`, nil, []string{"updated_at"}},
		{"max non-string conflicts", `{"updated_at":1}`, `{"updated_at":3}`, `{"updated_at":2}`, `{"updated_at":3}`, []string{"updated_at"}, nil},
		{"text conflict marked", `{"t":"a"}`, `{"t":"b"}`, `{"t":"c"}`, `{"t":"<<<<<<< ours\nb\n=======\nc\n>>>>>>> theirs"}`, []string{"t"}, nil},
		{"delete vs edit conflicts", `{"t":"a"}`, `{}`, `{"t":"c"}`, `{}`, []string{"t"}, nil},
		{"set union", `{"labels":["x","y"]}`, `{"labels":["x","y","o"]}`, `{"labels":["y","t"]}`, `{"labels":["y","o","t"]}`, nil, []string{"labels"}},
		{"set object elems", `{"labels":[]}`, `{"labels":[{"b":1,"a":2}]}`, `{"labels":[{"a":2,"b":1}]}`, `{"labels":[{"a":2,"b":1}]}`, nil, nil},
		{"add/add nil base", ``, `{"a":1,"o":true}`, `{"a":1,"t":false}`, `{"a":1,"o":true,"t":false}`, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := Merge(doc(t, tc.base), doc(t, tc.ours), doc(t, tc.theirs), pol)
			if err != nil {
				t.Fatal(err)
			}
			if got := compact(t, r.Merged); got != tc.want {
				t.Fatalf("merged = %s, want %s", got, tc.want)
			}
			var keys []string
			for _, c := range r.Conflicts {
				keys = append(keys, c.Key)
			}
			if !slices.Equal(keys, tc.conflicts) || !slices.Equal(r.AutoResolved, tc.auto) {
				t.Fatalf("conflicts %v auto %v, want %v %v", keys, r.AutoResolved, tc.conflicts, tc.auto)
			}
		})
	}
}

func TestMergeConflictValues(t *testing.T) {
	t.Parallel()
	r, err := Merge(doc(t, `{"n":1}`), doc(t, `{}`), doc(t, `{"n":2}`), Policy{})
	if err != nil {
		t.Fatal(err)
	}
	c := r.Conflicts[0]
	if !c.Ours.IsNull() {
		t.Fatalf("a deleted side's value is null, got %v", c.Ours)
	}
	if n, _ := c.Theirs.AsNumber(); n != "2" {
		t.Fatalf("theirs = %v", c.Theirs)
	}
}

func TestMergeBadNumberIsError(t *testing.T) {
	t.Parallel()
	bad := canonjson.Object{"n": canonjson.Number("1x")}
	if _, err := Merge(nil, bad, canonjson.Object{"n": canonjson.Int(1)}, Policy{}); err == nil {
		t.Fatal("a value that cannot be written must fail the merge, not merge silently")
	}
}

func TestMergeFileMatrix(t *testing.T) {
	t.Parallel()
	row := func(s string) File { return Row(doc(t, s)) }
	tomb := func(s string) File { return Tombstone(doc(t, s)) }
	for _, tc := range []struct {
		name               string
		base, ours, theirs File
		newer              Side
		decision           FileDecision
		kind               FileKind
		want               string
		deletedBy          Side
	}{
		{"same", row(`{"a":1}`), row(`{"a":2}`), row(`{"a":2}`), SideOurs, DecisionSame, FileRow, `{"a":2}`, SideOurs},
		{"theirs added", Absent(), Absent(), row(`{"a":1}`), SideOurs, DecisionTheirsOnly, FileRow, `{"a":1}`, SideOurs},
		{"ours deleted unchanged", row(`{"a":1}`), Absent(), row(`{"a":1}`), SideOurs, DecisionOursOnly, FileAbsent, ``, SideOurs},
		{"field merge", row(`{"a":1,"b":1}`), row(`{"a":2,"b":1}`), row(`{"a":1,"b":2}`), SideOurs, DecisionFieldMerge, FileRow, `{"a":2,"b":2}`, SideOurs},
		{"add/add", Absent(), row(`{"a":1}`), row(`{"b":1}`), SideOurs, DecisionFieldMerge, FileRow, `{"a":1,"b":1}`, SideOurs},
		{"modify beats tombstone", row(`{"a":1}`), row(`{"a":2}`), tomb(`{"d":true}`), SideTheirs, DecisionModifyBeatsDelete, FileRow, `{"a":2}`, SideTheirs},
		{"modify beats absent", row(`{"a":1}`), Absent(), row(`{"a":3}`), SideOurs, DecisionModifyBeatsDelete, FileRow, `{"a":3}`, SideOurs},
		{"two tombstones keep earlier", row(`{"a":1}`), tomb(`{"by":"o"}`), tomb(`{"by":"t"}`), SideTheirs, DecisionBothDeleted, FileTombstone, `{"by":"o"}`, SideOurs},
		{"two tombstones ours newer", row(`{"a":1}`), tomb(`{"by":"o"}`), tomb(`{"by":"t"}`), SideOurs, DecisionBothDeleted, FileTombstone, `{"by":"t"}`, SideOurs},
		{"tombstone beats absent", row(`{"a":1}`), Absent(), tomb(`{"by":"t"}`), SideOurs, DecisionBothDeleted, FileTombstone, `{"by":"t"}`, SideOurs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := MergeFile(tc.base, tc.ours, tc.theirs, Policy{Newer: tc.newer})
			if err != nil {
				t.Fatal(err)
			}
			if r.Decision != tc.decision || r.File.Kind != tc.kind || r.DeletedBy != tc.deletedBy {
				t.Fatalf("got %v %v deleted by %v", r.Decision, r.File.Kind, r.DeletedBy)
			}
			got := ""
			if r.File.Doc != nil {
				got = compact(t, r.File.Doc)
			}
			if got != tc.want {
				t.Fatalf("doc = %s, want %s", got, tc.want)
			}
		})
	}
	if _, err := MergeFile(File{Kind: FileRow}, Absent(), Absent(), Policy{}); err == nil {
		t.Fatal("a row without a doc breaks the invariant and must be refused")
	}
}

func TestConflictMarkers(t *testing.T) {
	t.Parallel()
	if !ContainsConflictMarkers(MarkConflict("a", "b")) || ContainsConflictMarkers("a\n=======\nb") {
		t.Fatal("marker detection wrong")
	}
	if FileTombstone.String() != "tombstone" || DecisionBothDeleted.String() != "both-deleted" || FileKind(9).String() != "kind(9)" {
		t.Fatal("names wrong")
	}
}
