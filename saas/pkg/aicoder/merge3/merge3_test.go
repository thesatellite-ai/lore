package merge3

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func obj(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func strategies(m map[string]Strategy) func(string) Strategy {
	return func(k string) Strategy {
		if s, ok := m[k]; ok {
			return s
		}
		return StrategyConflict
	}
}

func TestMerge_Table(t *testing.T) {
	t.Parallel()
	pol := Policy{StrategyFor: strategies(map[string]Strategy{
		"status":     StrategyNewest,
		"updated_at": StrategyMax,
	}), Newer: SideTheirs}

	for _, tc := range []struct {
		name          string
		base, o, th   map[string]any
		want          map[string]any
		wantConflicts []string
		wantAuto      []string
	}{
		{
			name: "unchanged",
			base: obj("a", "1"), o: obj("a", "1"), th: obj("a", "1"),
			want: obj("a", "1"),
		},
		{
			name: "only theirs changed",
			base: obj("a", "1"), o: obj("a", "1"), th: obj("a", "2"),
			want: obj("a", "2"),
		},
		{
			name: "only ours changed",
			base: obj("a", "1"), o: obj("a", "2"), th: obj("a", "1"),
			want: obj("a", "2"),
		},
		{
			name: "both changed identically",
			base: obj("a", "1"), o: obj("a", "3"), th: obj("a", "3"),
			want: obj("a", "3"),
		},
		{
			name: "different fields edited",
			base: obj("title", "t", "body", "b"), o: obj("title", "T2", "body", "b"), th: obj("title", "t", "body", "B2"),
			want: obj("title", "T2", "body", "B2"),
		},
		{
			name: "newest strategy theirs wins",
			base: obj("status", "todo"), o: obj("status", "done"), th: obj("status", "doing"),
			want: obj("status", "doing"), wantAuto: []string{"status"},
		},
		{
			name: "max strategy picks larger timestamp",
			base: obj("updated_at", "2026-01-01T00:00:00Z"), o: obj("updated_at", "2026-03-01T00:00:00Z"), th: obj("updated_at", "2026-02-01T00:00:00Z"),
			want: obj("updated_at", "2026-03-01T00:00:00Z"), wantAuto: []string{"updated_at"},
		},
		{
			name: "text clash gets markers",
			base: obj("body", "x"), o: obj("body", "mine"), th: obj("body", "yours"),
			want: obj("body", MarkConflict("mine", "yours")), wantConflicts: []string{"body"},
		},
		{
			name: "deleted on one side changed on other is conflict",
			base: obj("k", "v"), o: obj(), th: obj("k", "v2"),
			want: obj(), wantConflicts: []string{"k"},
		},
		{
			name: "deleted on one side unchanged on other deletes",
			base: obj("k", "v", "x", "1"), o: obj("x", "1"), th: obj("k", "v", "x", "1"),
			want: obj("x", "1"),
		},
		{
			name: "add/add without base equal",
			base: nil, o: obj("a", "1"), th: obj("a", "1"),
			want: obj("a", "1"),
		},
		{
			name: "add/add without base different",
			base: nil, o: obj("a", "1"), th: obj("a", "2"),
			want: obj("a", MarkConflict("1", "2")), wantConflicts: []string{"a"},
		},
		{
			name: "null vs absent distinct",
			base: obj("a", nil), o: obj(), th: obj("a", nil),
			want: obj(),
		},
		{
			name: "number type differences compare equal",
			base: obj("n", json.Number("1")), o: obj("n", int64(1)), th: obj("n", json.Number("2")),
			want: obj("n", json.Number("2")),
		},
		{
			name: "non-string clash keeps ours and reports",
			base: obj("n", json.Number("1")), o: obj("n", json.Number("2")), th: obj("n", json.Number("3")),
			want: obj("n", json.Number("2")), wantConflicts: []string{"n"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := Merge(tc.base, tc.o, tc.th, pol)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(res.Merged, tc.want) {
				t.Fatalf("merged = %#v\nwant %#v", res.Merged, tc.want)
			}
			var gotKeys []string
			for _, c := range res.Conflicts {
				gotKeys = append(gotKeys, c.Key)
			}
			if !reflect.DeepEqual(gotKeys, tc.wantConflicts) {
				t.Fatalf("conflicts = %v want %v", gotKeys, tc.wantConflicts)
			}
			if !reflect.DeepEqual(res.AutoResolved, tc.wantAuto) && (len(res.AutoResolved) != 0 || len(tc.wantAuto) != 0) {
				t.Fatalf("auto = %v want %v", res.AutoResolved, tc.wantAuto)
			}
		})
	}
}

func TestMerge_NewestOurs(t *testing.T) {
	t.Parallel()
	res, err := Merge(obj("s", "a"), obj("s", "b"), obj("s", "c"), Policy{
		StrategyFor: func(string) Strategy { return StrategyNewest }, Newer: SideOurs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Merged["s"] != "b" {
		t.Fatalf("ours should win, got %v", res.Merged["s"])
	}
}

func TestMerge_MaxWithDeletedSideConflicts(t *testing.T) {
	t.Parallel()
	res, err := Merge(obj("u", "1"), obj(), obj("u", "2"), Policy{
		StrategyFor: func(string) Strategy { return StrategyMax },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 1 {
		t.Fatalf("expected conflict, got %+v", res)
	}
}

func TestMerge_UnsupportedValueErrors(t *testing.T) {
	t.Parallel()
	if _, err := Merge(obj("a", struct{}{}), obj("a", "x"), obj("a", "y"), Policy{}); err == nil {
		t.Fatal("expected error for unsupported value type")
	}
}

func TestContainsConflictMarkers(t *testing.T) {
	t.Parallel()
	if !ContainsConflictMarkers(MarkConflict("a", "b")) {
		t.Fatal("MarkConflict output must be detected")
	}
	raw := "x\n<<<<<<< HEAD\na\n=======\nb\n>>>>>>> feature\n"
	if !ContainsConflictMarkers(raw) {
		t.Fatal("raw git hunk must be detected")
	}
	for _, clean := range []string{"", "plain body", "a ======= b", "<<<<<<< only start"} {
		if ContainsConflictMarkers(clean) {
			t.Fatalf("false positive on %q", clean)
		}
	}
	if ContainsConflictMarkers(strings.Repeat("=", 7)) {
		t.Fatal("split line alone is not a conflict")
	}
}
