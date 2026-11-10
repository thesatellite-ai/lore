package policy

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
)

func num(t *testing.T, lit string) value.Value {
	t.Helper()
	n, err := value.NumberOf(lit)
	if err != nil {
		t.Fatalf("NumberOf(%q): %v", lit, err)
	}
	return n
}

// TestShapeWeightCheck: an urgency weight is a signed number with at most
// two decimals and |v| <= 1000 (P2-TASK-6); anything else fails closed with
// E_CONFIG_INVALID naming the row, and the accepted value reads as exact
// integer hundredths.
func TestShapeWeightCheck(t *testing.T) {
	k := UrgencyKey("due", "")
	cases := []struct {
		name string
		v    value.Value
		ok   bool
		want int64 // hundredths
	}{
		{"whole", num(t, "12"), true, 1200},
		{"negative", num(t, "-3"), true, -300},
		{"one decimal", num(t, "0.5"), true, 50},
		{"two decimals", num(t, "1.23"), true, 123},
		{"exact tenth (no float drift)", num(t, "0.1"), true, 10},
		{"zero", num(t, "0"), true, 0},
		{"ceiling", num(t, "1000"), true, 100000},
		{"negative ceiling", num(t, "-1000"), true, -100000},
		{"three decimals", num(t, "1.234"), false, 0},
		{"above ceiling", num(t, "1001"), false, 0},
		{"below negative ceiling", num(t, "-1000.01"), false, 0},
		{"exponent", num(t, "1e2"), false, 0},
		{"text", txt("12"), false, 0},
		{"bool", value.BoolOf(true), false, 0},
		{"list", list("1"), false, 0},
	}
	for _, c := range cases {
		s, err := Resolve([]Entry{{Level: LevelRepo, Key: k, Value: c.v, Source: src(1)}})
		if !c.ok {
			if !errs.Has(err, errs.ConfigInvalid) {
				t.Errorf("%s: want E_CONFIG_INVALID, got %v", c.name, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		got, set := s.Weight(k)
		if !set || got != c.want {
			t.Errorf("%s: Weight = %d,%v want %d,true", c.name, got, set, c.want)
		}
	}
}

// TestShapeWeightNearestWins: a weight is a preference, so the nearest
// level that sets it wins, whatever its sign or size (OV15B-URG), in any
// input order; an unset key reports false so the caller uses TASK 13.2.
func TestShapeWeightNearestWins(t *testing.T) {
	k := UrgencyKey(urgencyPriority, "high")
	repo := Entry{Level: LevelRepo, Key: k, Value: num(t, "-5"), Source: src(1)}
	folder := Entry{Level: LevelFolder, Key: k, Value: num(t, "7.5"), Source: src(2)}
	org := Entry{Level: LevelOrg, Key: k, Value: num(t, "100"), Source: src(3)}
	cases := []struct {
		name    string
		entries []Entry
		want    int64
		level   Level
	}{
		{"repo over folder over org", []Entry{org, folder, repo}, -500, LevelRepo},
		{"folder over org", []Entry{org, folder}, 750, LevelFolder},
		{"org alone", []Entry{org}, 10000, LevelOrg},
		{"order does not matter", []Entry{repo, org, folder}, -500, LevelRepo},
	}
	for _, c := range cases {
		s, err := Resolve(c.entries)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got, ok := s.Weight(k)
		e, _ := s.Get(k)
		if !ok || got != c.want || e.Level != c.level {
			t.Errorf("%s: %d,%v at %s; want %d at %s", c.name, got, ok, e.Level, c.want, c.level)
		}
	}
	if _, ok := Defaults().Weight(k); ok {
		t.Error("unset weight must report false")
	}
	if _, ok := Defaults().Weight(KeyMetaMaxBytes); ok {
		t.Error("a limit key is not a weight")
	}
}

// TestUrgencyKeysAreWeights: all 12 urgency keys (4 priority levels + 8
// terms, TASK 13.2) are ShapeWeight keys with the 1000 ceiling.
func TestUrgencyKeysAreWeights(t *testing.T) {
	ks := UrgencyKeys()
	if len(ks) != 12 {
		t.Fatalf("got %d urgency keys, want 12", len(ks))
	}
	for _, u := range ks {
		d, ok := Lookup(u.Key)
		if !ok || d.Shape != ShapeWeight || d.Ceiling != ceilingUrgencyWeight {
			t.Errorf("%s: %+v %v", u.Key, d, ok)
		}
	}
	if UrgencyKey("priority", "low") != "tasks.urgency.priority.low" || UrgencyKey("age", "") != "tasks.urgency.age" {
		t.Error("urgency key names changed")
	}
}

// TestStaleAfterKeys: resources.stale_after (P2-RES-6: whole hours, default
// 24) and meta.stale_after (P2-META-14: default 168) are limits: the smaller
// is stricter across levels, 0 is allowed (the caller treats it as off), and
// a fraction, a negative or text is refused.
func TestStaleAfterKeys(t *testing.T) {
	for _, c := range []struct {
		k   Key
		def int64
	}{{KeyResourcesStaleAfter, 24}, {KeyMetaStaleAfter, 168}} {
		if n, ok := Defaults().Limit(c.k); !ok || n != c.def {
			t.Errorf("%s default %d,%v want %d", c.k, n, ok, c.def)
		}
		s, err := Resolve([]Entry{
			{Level: LevelRepo, Key: c.k, Value: value.Int(48), Source: src(1)},
			{Level: LevelOrg, Key: c.k, Value: value.Int(6), Source: src(2)},
		})
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := s.Limit(c.k); n != 6 {
			t.Errorf("%s: a nearer level loosened to %d", c.k, n)
		}
		s, err = Resolve([]Entry{{Level: LevelOrg, Key: c.k, Value: value.Int(0), Source: src(2)}})
		if err != nil {
			t.Fatal(err)
		}
		if n, ok := s.Limit(c.k); !ok || n != 0 {
			t.Errorf("%s: 0 = %d,%v", c.k, n, ok)
		}
		for _, bad := range []value.Value{num(t, "1.5"), num(t, "-1"), txt("24h")} {
			if _, err := Resolve([]Entry{{Level: LevelRepo, Key: c.k, Value: bad, Source: src(1)}}); !errs.Has(err, errs.ConfigInvalid) {
				t.Errorf("%s=%v: want E_CONFIG_INVALID, got %v", c.k, bad, err)
			}
		}
	}
}
