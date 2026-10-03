package lsync

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"saas/pkg/aicoder/merge3"

	"entgo.io/ent/schema/field"
)

func TestParseLooseTime(t *testing.T) {
	t.Parallel()
	want := time.Date(2026, 10, 3, 14, 42, 53, 32924000, time.UTC)
	for _, in := range []string{
		"2026-10-03T14:42:53.032924000Z",
		"2026-10-03T14:42:53.032924Z",
		"2026-10-03 20:12:53.032924 +0530 IST m=+0.031915251",
		"2026-10-03 20:12:53.032924 +0530 IST",
		"2026-10-03 14:42:53.032924+00:00",
	} {
		got, err := parseLooseTime(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if !got.Equal(want) {
			t.Fatalf("%q = %v want %v", in, got, want)
		}
	}
	if _, err := parseLooseTime("yesterday"); err == nil {
		t.Fatal("garbage must fail")
	}
	if got, err := parseLooseTime("2026-10-03 14:42:53"); err != nil || got.Hour() != 14 {
		t.Fatalf("CURRENT_TIMESTAMP form: %v %v", got, err)
	}
}

func TestFormatTimeFixedWidthSortsChronologically(t *testing.T) {
	t.Parallel()
	a := formatTime(time.Date(2026, 1, 1, 0, 0, 5, 100000000, time.UTC))
	b := formatTime(time.Date(2026, 1, 1, 0, 0, 5, 120000000, time.UTC))
	if len(a) != len(b) || !(a < b) {
		t.Fatalf("%s vs %s must be fixed width and ordered", a, b)
	}
	local := time.Date(2026, 1, 1, 5, 30, 0, 0, time.FixedZone("IST", 19800))
	if formatTime(local) != "2026-01-01T00:00:00.000000000Z" {
		t.Fatalf("not normalised to UTC: %s", formatTime(local))
	}
}

func TestToDocValue_AllTypes(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, tc := range []struct {
		name string
		col  Column
		in   any
		want any
	}{
		{"nil", Column{Type: field.TypeString}, nil, nil},
		{"time", Column{Type: field.TypeTime}, ts, "2026-01-02T03:04:05.000000000Z"},
		{"time text", Column{Type: field.TypeTime}, "2026-01-02 03:04:05", "2026-01-02T03:04:05.000000000Z"},
		{"bool int", Column{Type: field.TypeBool}, int64(1), true},
		{"bool false", Column{Type: field.TypeBool}, int64(0), false},
		{"int", Column{Type: field.TypeInt}, int64(42), int64(42)},
		{"int from float", Column{Type: field.TypeInt64}, float64(7), int64(7)},
		{"float", Column{Type: field.TypeFloat64}, 0.5, 0.5},
		{"float from int", Column{Type: field.TypeFloat64}, int64(2), float64(2)},
		{"string", Column{Type: field.TypeString}, "x", "x"},
		{"bytes as string", Column{Type: field.TypeString}, []byte("y"), "y"},
		{"text holding number", Column{Type: field.TypeString}, int64(5), "5"},
		{"enum", Column{Type: field.TypeEnum}, "must", "must"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := toDocValue(tc.col, tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %#v want %#v", got, tc.want)
			}
		})
	}
}

func TestToDocValue_JSONAndErrors(t *testing.T) {
	t.Parallel()
	got, err := toDocValue(Column{Type: field.TypeJSON}, `{"b":1,"a":[2]}`)
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := got.(map[string]any); !ok || m["b"] != json.Number("1") {
		t.Fatalf("json not parsed: %#v", got)
	}
	if _, err := toDocValue(Column{Type: field.TypeJSON}, "{bad"); err == nil {
		t.Fatal("invalid JSON column must error")
	}
	if _, err := toDocValue(Column{Type: field.TypeInt}, 1.5); err == nil {
		t.Fatal("non-integral float in int column must error")
	}
	if _, err := toDocValue(Column{Type: field.TypeTime}, "garbage"); err == nil {
		t.Fatal("bad timestamp must error")
	}
}

func TestToSQLValue(t *testing.T) {
	t.Parallel()
	if v, err := toSQLValue(Column{Type: field.TypeInt}, json.Number("12")); err != nil || v != int64(12) {
		t.Fatalf("int: %v %v", v, err)
	}
	if v, err := toSQLValue(Column{Type: field.TypeFloat64}, json.Number("0.25")); err != nil || v != 0.25 {
		t.Fatalf("float: %v %v", v, err)
	}
	if v, err := toSQLValue(Column{Type: field.TypeBool}, true); err != nil || v != true {
		t.Fatalf("bool: %v %v", v, err)
	}
	if v, err := toSQLValue(Column{Type: field.TypeJSON}, map[string]any{"z": json.Number("1"), "a": "x"}); err != nil || v != `{"a":"x","z":1}` {
		t.Fatalf("json: %v %v", v, err)
	}
	v, err := toSQLValue(Column{Type: field.TypeTime}, "2026-01-02T03:04:05.000000000Z")
	if tv, ok := v.(time.Time); err != nil || !ok || tv.Location() != time.UTC {
		t.Fatalf("time: %v %v", v, err)
	}
	for _, bad := range []struct {
		col Column
		v   any
	}{
		{Column{Type: field.TypeInt}, "12"},
		{Column{Type: field.TypeInt}, json.Number("1.5")},
		{Column{Type: field.TypeFloat64}, "x"},
		{Column{Type: field.TypeBool}, "true"},
		{Column{Type: field.TypeString}, json.Number("1")},
		{Column{Type: field.TypeTime}, json.Number("1")},
	} {
		if _, err := toSQLValue(bad.col, bad.v); err == nil {
			t.Fatalf("%v into %s must error", bad.v, bad.col.Type)
		}
	}
}

func TestArgsFromDoc_ExtrasAndVolatile(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry()
	tbl, _ := reg.Table("memories")
	ra, err := argsFromDoc(tbl, map[string]any{
		keyVersion: json.Number("1"), keyTable: "memories",
		"id": "mem_01a10237c9e87e4c843d8045973a65a1", "body": "b",
		"last_accessed_at": "2026-01-01T00:00:00Z", // volatile: ignored
		"brand_new":        "kept",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, has := ra.values["last_accessed_at"]; has {
		t.Fatal("volatile column must never be imported from a file")
	}
	if ra.extra["brand_new"] != "kept" {
		t.Fatal("unknown key not preserved")
	}
	if _, err := argsFromDoc(tbl, map[string]any{"body": "no id"}); err == nil {
		t.Fatal("document without id must error")
	}
}

func TestDecodeDoc_Envelope(t *testing.T) {
	t.Parallel()
	if _, err := decodeDoc([]byte(`{"_v": 2}`)); !errors.Is(err, errNewerFormat) {
		t.Fatalf("newer _v: %v", err)
	}
	for _, bad := range []string{`{}`, `{"_v": "1"}`, `{"_v": 0}`, `{"_v": 1.5}`} {
		if _, err := decodeDoc([]byte(bad)); err == nil {
			t.Fatalf("%s must be rejected", bad)
		}
	}
	if _, err := decodeDoc([]byte(`{"_v": 1, "id": "x"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestSameIgnoringUpdatedAtAndNewer(t *testing.T) {
	t.Parallel()
	a := map[string]any{"body": "x", "updated_at": "2026-01-01T00:00:00.000000000Z"}
	b := map[string]any{"body": "x", "updated_at": "2026-02-01T00:00:00.000000000Z"}
	if same, err := sameIgnoringUpdatedAt(a, b); err != nil || !same {
		t.Fatal("updated_at-only difference must compare equal")
	}
	if !firstIsNewer(b, a) || firstIsNewer(a, b) || firstIsNewer(a, a) {
		t.Fatal("firstIsNewer ordering wrong")
	}
	if same, _ := sameDoc(nil, nil); !same {
		t.Fatal("nil == nil")
	}
	if same, _ := sameDoc(a, nil); same {
		t.Fatal("doc != nil")
	}
}

func TestStrategyFor(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry()
	tasks, _ := reg.Table("tasks")
	s := StrategyFor(tasks)
	for key, want := range map[string]merge3.Strategy{
		"updated_at":   merge3.StrategyMax,
		"status":       merge3.StrategyNewest, // enum
		"due_at":       merge3.StrategyNewest, // time
		"mission_id":   merge3.StrategyNewest, // reference
		"title":        merge3.StrategyConflict,
		"body":         merge3.StrategyConflict,
		"not_a_column": merge3.StrategyConflict,
	} {
		if got := s(key); got != want {
			t.Fatalf("%s: got %v want %v", key, got, want)
		}
	}
}

func TestRelPathRoundTrip(t *testing.T) {
	t.Parallel()
	id := "mem_01a10237c9e87e4c843d8045973a65a1"
	tbl, gotID, ok := parseRelPath(relPath("memories", id))
	if !ok || tbl != "memories" || gotID != id {
		t.Fatalf("round trip failed: %s %s %v", tbl, gotID, ok)
	}
	for _, bad := range []string{"memories/x.json", "a/b/" + id + ".json", id + ".json", "memories/" + id + ".txt", "../" + id + ".json"} {
		if _, _, ok := parseRelPath(bad); ok {
			t.Fatalf("%q must be rejected", bad)
		}
	}
}
