package live

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/canonjson"

	"github.com/thesatellite-ai/lore/logic/merge3"
)

// strategies: priority is a scalar (newest by hlc), title text (conflict),
// labels a set, updated_at max — the field types F4 names.
func strategies(string) func(string) merge3.Strategy {
	return func(k string) merge3.Strategy {
		switch k {
		case "priority":
			return merge3.StrategyNewest
		case "labels":
			return merge3.StrategySet
		case "updated_at":
			return merge3.StrategyMax
		}
		return merge3.StrategyConflict
	}
}

const rowP = "_root/tasks/tsk_1.json"

type sideSpec int

const (
	absent sideSpec = iota
	same
	editTitle
	editPriority
	tomb
)

func mk(t *testing.T, s sideSpec, hlcMin int, actor string) []byte {
	t.Helper()
	body := obj(map[string]string{"id": "tsk_1", "title": "base", "priority": "medium"})
	switch s {
	case absent:
		return nil
	case editTitle:
		body["title"] = canonjson.String("title by " + actor)
	case editPriority:
		body["priority"] = canonjson.String("high-" + actor)
	case same, tomb:
		// the base body; a tombstone is made from it below
	}
	f := File{Body: body, Block: Block{LiveSince: t0, HLC: HLC{Wall: t0.Add(time.Duration(hlcMin) * time.Minute), Actor: actor}}}
	if s == tomb {
		f = Tombstone("tsk_1", f.Block)
	}
	b, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func tree(p string, b []byte) Tree {
	if b == nil {
		return Tree{}
	}
	return Tree{p: b}
}

// TestMergeTreesFileMatrix walks base × ours × theirs over {absent, same,
// edited, tombstone} for one row (F4 step 1 and 2).
func TestMergeTreesFileMatrix(t *testing.T) {
	type want struct {
		present  bool
		deleted  bool
		title    string
		priority string
		replaced string // the field the note names as replaced
		decision merge3.FileDecision
	}
	cases := []struct {
		name               string
		base, ours, theirs sideSpec
		oursHLC, theirsHLC int
		want               want
	}{
		{"add on ours only", absent, same, absent, 1, 0, want{present: true, title: "base", priority: "medium"}},
		{"add on theirs only", absent, absent, same, 0, 1, want{present: true, title: "base", priority: "medium"}},
		{"add on both, same", absent, same, same, 1, 1, want{present: true, title: "base", priority: "medium"}},
		{"add on both, different text → newer wins, no markers (L-19)", absent, editTitle, editTitle, 1, 2, want{present: true, title: "title by B", priority: "medium", replaced: "title", decision: merge3.DecisionFieldMerge}},
		{"both edit title, ours newer → ours, no markers (L-19)", same, editTitle, editTitle, 3, 2, want{present: true, title: "title by A", priority: "medium", replaced: "title", decision: merge3.DecisionFieldMerge}},
		{"edit title vs edit priority → both kept", same, editTitle, editPriority, 1, 2, want{present: true, title: "title by A", priority: "high-B", decision: merge3.DecisionFieldMerge}},
		{"both edit priority, theirs newer", same, editPriority, editPriority, 1, 2, want{present: true, title: "base", priority: "high-B", decision: merge3.DecisionFieldMerge}},
		{"both edit priority, ours newer (pushed later irrelevant)", same, editPriority, editPriority, 3, 2, want{present: true, title: "base", priority: "high-A", decision: merge3.DecisionFieldMerge}},
		{"tombstone vs unchanged → deleted", same, tomb, same, 2, 1, want{present: true, deleted: true}},
		{"unchanged vs tombstone → deleted", same, same, tomb, 1, 2, want{present: true, deleted: true}},
		{"tombstone vs edit → modify wins", same, tomb, editTitle, 2, 3, want{present: true, title: "title by B", priority: "medium", decision: merge3.DecisionModifyBeatsDelete}},
		{"edit vs tombstone → modify wins", same, editTitle, tomb, 3, 4, want{present: true, title: "title by A", priority: "medium", decision: merge3.DecisionModifyBeatsDelete}},
		{"both tombstoned", same, tomb, tomb, 1, 2, want{present: true, deleted: true}},
		{"removed on ours (left live) unchanged theirs", same, absent, same, 0, 1, want{present: false}},
		{"removed on ours, edited theirs → edit kept", same, absent, editTitle, 0, 2, want{present: true, title: "title by B", priority: "medium", decision: merge3.DecisionModifyBeatsDelete}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := tree(rowP, mk(t, c.base, 0, "base"))
			o := tree(rowP, mk(t, c.ours, c.oursHLC, "A"))
			th := tree(rowP, mk(t, c.theirs, c.theirsHLC, "B"))
			out, notes, err := MergeTrees(b, o, th, strategies)
			if err != nil {
				t.Fatal(err)
			}
			raw, ok := out[rowP]
			if ok != c.want.present {
				t.Fatalf("present = %v, want %v", ok, c.want.present)
			}
			if !ok {
				return
			}
			f, err := DecodeFile(raw)
			if err != nil {
				t.Fatal(err)
			}
			if f.Deleted() != c.want.deleted {
				t.Fatalf("deleted = %v (%s)", f.Deleted(), raw)
			}
			if c.want.deleted {
				return
			}
			title, _ := f.Body["title"].AsString()
			if f.Block.Conflict || strings.Contains(string(raw), "<<<<<<<") || strings.Contains(string(raw), `\u003c\u003c`) {
				t.Fatalf("a live merge never writes conflict markers (L-19): %s", raw)
			}
			if title != c.want.title {
				t.Fatalf("title %q, want %q", title, c.want.title)
			}
			if p, _ := f.Body["priority"].AsString(); p != c.want.priority {
				t.Fatalf("priority %q, want %q", p, c.want.priority)
			}
			wantHLC := MaxHLC(HLC{Wall: t0.Add(time.Duration(c.oursHLC) * time.Minute), Actor: "A"}, HLC{Wall: t0.Add(time.Duration(c.theirsHLC) * time.Minute), Actor: "B"})
			if c.ours != absent && c.theirs != absent && f.Block.HLC.Compare(wantHLC) != 0 {
				t.Fatalf("merged block keeps the highest hlc: %v want %v", f.Block.HLC, wantHLC)
			}
			if c.want.decision != 0 {
				if len(notes) != 1 || notes[0].Decision != c.want.decision {
					t.Fatalf("notes %+v, want decision %v", notes, c.want.decision)
				}
				if c.want.replaced != "" && (len(notes[0].Replaced) != 1 || notes[0].Replaced[0].Field != c.want.replaced || !strings.Contains(notes[0].Describe(), "kept the newer edit")) {
					t.Fatalf("note %+v (%s), want %s named as replaced", notes[0], notes[0].Describe(), c.want.replaced)
				}
			}
		})
	}
}

func TestMergeTreesRefFiles(t *testing.T) {
	purged, _ := encodePurged(map[string]bool{"tsk_gone": true})
	gone, _ := File{Body: obj(map[string]string{"id": "tsk_gone", "title": "x"})}.Encode()
	ours := Tree{EpochPath: encodeEpoch(2), "_root/tasks/tsk_gone.json": gone}
	theirs := Tree{EpochPath: encodeEpoch(3), PurgedPath: purged}
	out, _, err := MergeTrees(Tree{}, ours, theirs, strategies)
	if err != nil {
		t.Fatal(err)
	}
	if out.Epoch() != 3 {
		t.Fatalf("epoch must be the higher one: %d", out.Epoch())
	}
	if _, has := out["_root/tasks/tsk_gone.json"]; has {
		t.Fatal("a purged id must never come back from a stale side")
	}
	if p, _ := out.Purged(); !p["tsk_gone"] {
		t.Fatal("purged list must be the union")
	}
}

func TestMergeBlocksCombine(t *testing.T) {
	base, _ := File{Body: obj(map[string]string{"id": "tsk_1"}), Block: Block{LiveSince: t0}}.Encode()
	o, _ := File{Body: obj(map[string]string{"id": "tsk_1"}), Block: Block{LiveSince: t0, LastCopyAt: t0.Add(7 * 24 * time.Hour)}}.Encode()
	th, _ := File{Body: obj(map[string]string{"id": "tsk_1"}), Block: Block{LiveSince: t0, HLC: HLC{Wall: t0, Actor: "B"}}}.Encode()
	out, _, err := MergeTrees(Tree{rowP: base}, Tree{rowP: o}, Tree{rowP: th}, strategies)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := DecodeFile(out[rowP])
	if f.Block.LastCopyAt.IsZero() || f.Block.HLC.Actor != "B" {
		t.Fatalf("block-only changes on both sides must both survive: %+v", f.Block)
	}
}

// A companion row stays one only while every side says so (an explicit
// write on either side makes it land on its own).
func TestCompanionFlagCombines(t *testing.T) {
	comp := func(c bool) []byte {
		b, _ := File{Body: obj(map[string]string{"id": "act_1", "n": "x"}), Block: Block{Companion: c}}.Encode()
		return b
	}
	for _, c := range []struct{ o, th, want bool }{{true, true, true}, {true, false, false}, {false, true, false}} {
		p := "_root/actors/act_1.json"
		out, _, err := MergeTrees(Tree{}, Tree{p: comp(c.o)}, Tree{p: comp(c.th)}, strategies)
		if err != nil {
			t.Fatal(err)
		}
		f, _ := DecodeFile(out[p])
		if f.Block.Companion != c.want {
			t.Fatalf("%+v → %v", c, f.Block.Companion)
		}
	}
	prev := File{Body: obj(map[string]string{"id": "act_1"}), Block: Block{Companion: true}}
	if NewVersion(&prev, obj(map[string]string{"id": "act_1"}), t0, "a").Block.Companion {
		t.Fatal("an edit is a write on purpose: no longer a companion")
	}
}
