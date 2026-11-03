package live

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"saas/pkg/aicoder/clock"
	"saas/pkg/aicoder/gitremote/gitremotetest"
	"saas/pkg/aicoder/merge3"
	"saas/pkg/aicoder/signals"
)

// A store branch's live ref is refs/lore/live/<branch>; two branches never
// share one, and it never equals the code repo's plain refs/lore/live.
func TestStoreRefPerBranch(t *testing.T) {
	if got := StoreRef("main"); got != "refs/lore/live/main" {
		t.Fatalf("StoreRef(main) = %q", got)
	}
	seen := map[string]bool{RefLive: true}
	for _, b := range []string{"main", "staging", "日本", "a.b"} {
		r := StoreRef(b)
		if seen[r] {
			t.Fatalf("two branches share %q", r)
		}
		seen[r] = true
		if !strings.HasPrefix(r, RefLive+"/") {
			t.Fatalf("%q is not under %s", r, RefLive)
		}
	}
	// The live and signals refs of one branch are different refs.
	if StoreRef("main") == signals.StoreRef("main") {
		t.Fatal("live and signals refs collide")
	}
}

func TestRowKeyStringRoundTrip(t *testing.T) {
	for _, k := range []RowKey{{"tasks", "tsk_1"}, {"memories", "mem_日本"}, {"decisions", "dec_a.b"}} {
		p := RowPath("web/app", k.Table, k.ID)
		f, got, ok := ParseRowPath(p)
		if !ok || f != "web/app" || got != k || got.String() != k.Table+"/"+k.ID {
			t.Errorf("%v → %q → %q %v %v", k, p, f, got, ok)
		}
	}
	if (RowKey{}).String() != "/" {
		t.Fatal("zero key renders as the bare separator")
	}
}

func TestTreeFolders(t *testing.T) {
	cases := []struct {
		name string
		tree Tree
		want []string
	}{
		{"empty", Tree{}, []string{}},
		{"nil", nil, []string{}},
		{"only meta", Tree{EpochPath: []byte("1"), MetaPath: []byte("{}"), PurgedPath: []byte("{}")}, []string{}},
		{"sorted and deduped", Tree{
			RowPath("web", "tasks", "t1"):      nil,
			RowPath("web", "memories", "m1"):   nil,
			RowPath(RootFolder, "tasks", "t2"): nil,
			RowPath("api/v2", "tasks", "t3"):   nil,
			"web/README.md":                    nil,
			"stray.json":                       nil,
			"web/tasks/.json":                  nil,
			RowPath("日本", "rules", "r1"):       nil,
			metaFolder + "/x/tasks/t.json":     nil,
		}, []string{RootFolder, "api/v2", "web", "日本"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.tree.Folders()
			if got == nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Folders() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestCompactOrNull(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, "null"},
		{"", ""},
		{"plain text", "plain text"},
		{"日本", "日本"},
		{true, "true"},
		{json.Number("3"), "3"},
		{[]any{"a", json.Number("1")}, `["a",1]`},
		{map[string]any{"b": json.Number("2"), "a": "x"}, `{"a":"x","b":2}`},
	}
	for _, tc := range cases {
		if got := compactOrNull(tc.in); got != tc.want {
			t.Errorf("compactOrNull(%#v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A clash on a non-string field is made visible as marked text; a field
// that already carries markers is left alone.
func TestMarkNonTextConflicts(t *testing.T) {
	marked := merge3.MarkConflict("a", "b")
	body := map[string]any{"priority": json.Number("2"), "title": marked, "tags": []any{"x"}}
	markNonTextConflicts(body, []merge3.Conflict{
		{Key: "priority", Ours: json.Number("2"), Theirs: json.Number("4")},
		{Key: "title", Ours: "a", Theirs: "b"},
		{Key: "tags", Ours: []any{"x"}, Theirs: nil},
	})
	if body["title"] != marked {
		t.Fatalf("already-marked text must not be re-marked: %q", body["title"])
	}
	for _, k := range []string{"priority", "tags"} {
		s, ok := body[k].(string)
		if !ok || !merge3.ContainsConflictMarkers(s) {
			t.Fatalf("%s must become marked text: %#v", k, body[k])
		}
	}
	if !strings.Contains(body["tags"].(string), "null") || !strings.Contains(body["priority"].(string), "4") {
		t.Fatalf("both sides are visible: %q %q", body["tags"], body["priority"])
	}
}

func TestMergeNoteDescribe(t *testing.T) {
	ours := HLC{Wall: t0, Counter: 1, Actor: "act_a"}
	theirs := HLC{Wall: t0.Add(time.Minute), Counter: 0, Actor: "act_b"}
	k := RowKey{Table: "tasks", ID: "tsk_1"}
	cases := []struct {
		name string
		n    MergeNote
		want []string
	}{
		{"deleted by you", MergeNote{Key: k, Decision: merge3.DecisionModifyBeatsDelete, DeletedBy: merge3.SideOurs}, []string{"tsk_1", "deleted by you", "kept"}},
		{"deleted by teammate", MergeNote{Key: k, Decision: merge3.DecisionModifyBeatsDelete, DeletedBy: merge3.SideTheirs}, []string{"deleted by a teammate", "kept"}},
		{"field merge clean", MergeNote{Key: k, Decision: merge3.DecisionFieldMerge, OursHLC: ours, TheirsHLC: theirs}, []string{"merged both edits", theirs.String()}},
		{"field merge clean ours newer", MergeNote{Key: k, Decision: merge3.DecisionFieldMerge, OursHLC: theirs, TheirsHLC: ours}, []string{theirs.String()}},
		{"field merge conflict", MergeNote{Key: k, Decision: merge3.DecisionFieldMerge, Conflict: true, ConflictKeys: []string{"body", "title"}}, []string{"both sides edited body, title", "lore sync conflicts"}},
		{"both deleted", MergeNote{Key: k, Decision: merge3.DecisionBothDeleted}, []string{"deleted on both sides"}},
		{"other decision", MergeNote{Key: k, Decision: merge3.DecisionOursOnly}, []string{"tsk_1: ours-only"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.n.Describe()
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Fatalf("Describe() = %q, missing %q", got, w)
				}
			}
		})
	}
}

func TestShortOID(t *testing.T) {
	for in, want := range map[string]string{"": "", "abc": "abc", "abcdefg": "abcdefg", "abcdefgh": "abcdefg", strings.Repeat("f", 40): "fffffff"} {
		if got := short(in); got != want {
			t.Errorf("short(%q) = %q, want %q", in, got, want)
		}
	}
}

// Signed stamps the configured signature with the ref's clock, never the
// wall clock, and does not change the configured signature.
func TestRefSigned(t *testing.T) {
	w := gitremotetest.NewWorld(t)
	c := w.Clone("a")
	r := newRef(t, c, c.Remote)
	s := r.Signed()
	if !s.When.Equal(t0) || s.Name != "a" || s.Email != "a@example.test" {
		t.Fatalf("Signed() = %+v", s)
	}
	if !r.Signature.When.IsZero() {
		t.Fatal("Signed must not mutate the ref's signature")
	}
	r.Clock.(*clock.Fake).Set(t0.Add(time.Hour))
	if !r.Signed().When.Equal(t0.Add(time.Hour)) {
		t.Fatal("Signed follows the clock")
	}
	r.Clock = nil
	if got := r.Signed().When; got.Location() != time.UTC || time.Since(got) > time.Minute {
		t.Fatalf("no clock: the wall clock in UTC, got %v", got)
	}
}

// ClearSeen forgets the last integrated remote commit; clearing twice, or
// clearing a ref that never integrated, is fine.
func TestClearSeen(t *testing.T) {
	ctx := context.Background()
	w := gitremotetest.NewWorld(t)
	c := w.Clone("a")
	r := newRef(t, c, c.Remote)
	if err := r.ClearSeen(); err != nil {
		t.Fatalf("clear with nothing seen: %v", err)
	}
	if err := r.markSeen("abc123"); err != nil {
		t.Fatal(err)
	}
	snap, err := r.Snapshot(ctx)
	if err != nil || snap.Seen != "abc123" {
		t.Fatalf("seen recorded: %+v %v", snap, err)
	}
	if err := r.ClearSeen(); err != nil {
		t.Fatal(err)
	}
	snap, _ = r.Snapshot(ctx)
	if snap.Seen != "" {
		t.Fatalf("seen cleared: %+v", snap)
	}
	if _, err := os.Stat(r.stateFile(seenSuffix)); !os.IsNotExist(err) {
		t.Fatalf("state file removed: %v", err)
	}
	if err := r.ClearSeen(); err != nil {
		t.Fatalf("second clear: %v", err)
	}
	// Another ref's seen marker in the same state dir is not touched.
	other := newRef(t, c, c.Remote)
	other.Name = StoreRef("main")
	if err := other.markSeen("def456"); err != nil {
		t.Fatal(err)
	}
	if err := r.ClearSeen(); err != nil {
		t.Fatal(err)
	}
	if s, _ := other.Snapshot(ctx); s.Seen != "def456" {
		t.Fatalf("a store branch's seen marker survives the code ref's clear: %+v", s)
	}
}
