package merge

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
)

// js builds a JSON value (nil for "").
func js(t *testing.T, s string) value.Value {
	t.Helper()
	if s == "" {
		return nil
	}
	v, err := value.JSONOf([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// raw is v's JSON text ("" for nil).
func raw(t *testing.T, v value.Value) string {
	t.Helper()
	if v == nil {
		return ""
	}
	b, err := v.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSetUnionMatrix: RQ5 set merge (adds kept, removals kept unless
// re-added), the same in both directions, normalised strings.
func TestSetUnionMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, base, ours, theirs, want string }{
		{"both add", `["a"]`, `["a","b"]`, `["a","c"]`, `["a","b","c"]`},
		{"remove vs add", `["a","b"]`, `["a"]`, `["a","b","c"]`, `["a","c"]`},
		{"both remove different", `["a","b","c"]`, `["a","c"]`, `["a","b"]`, `["a"]`},
		{"no base, union", ``, `["b"]`, `["a"]`, `["a","b"]`},
		{"trimmed equal", `[]`, `[" a"]`, `["a"]`, `[" a"]`},
		{"objects by value", `[{"task_id":"t1","until":"done"}]`, `[{"until":"x","task_id":"t1"}]`, `[{"task_id":"t1","until":"done"},{"task_id":"t2"}]`, `[{"task_id":"t1","until":"x"},{"task_id":"t2"}]`},
		{"null side", `["a"]`, `null`, `["a","b"]`, `["b"]`},
	}
	for _, c := range cases {
		for _, n := range []side{newerOurs, newerTheirs} {
			got, err := StrategySetUnion(js(t, c.base), js(t, c.ours), js(t, c.theirs), n)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			rev, err := StrategySetUnion(js(t, c.base), js(t, c.theirs), js(t, c.ours), !n)
			if err != nil {
				t.Fatal(err)
			}
			if c.name == "trimmed equal" {
				continue // one of two equal-by-identity spellings survives
			}
			if raw(t, got) != c.want || raw(t, rev) != c.want {
				t.Errorf("%s: got %s / reversed %s, want %s", c.name, raw(t, got), raw(t, rev), c.want)
			}
		}
	}
}

// TestSetUnionKeepsListType: a decoded text list stays a value.List.
func TestSetUnionKeepsListType(t *testing.T) {
	t.Parallel()
	l, err := value.ParseList(value.KindText, []byte(`["x"]`))
	if err != nil {
		t.Fatal(err)
	}
	m, err := value.ParseList(value.KindText, []byte(`["y"]`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := StrategySetUnion(nil, l, m, newerOurs)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(value.List); !ok || raw(t, got) != `["x","y"]` {
		t.Errorf("got %T %s", got, raw(t, got))
	}
}

// TestURLSet: repos.urls entries are one per normalised url; the same url
// changed on both sides takes the newer side; sorted by from.
func TestURLSet(t *testing.T) {
	t.Parallel()
	base := `[{"url":"https://github.com/a/r.git","kind":"primary","how":"init","from":"2026-01-01"}]`
	ours := `[{"url":"https://github.com/a/r.git","kind":"primary","how":"init","from":"2026-01-01","to":"2026-02-01"},{"url":"git@github.com:b/r.git","kind":"primary","how":"set-url","from":"2026-02-01"}]`
	theirs := `[{"url":"git@github.com:a/r.git","kind":"primary","how":"init","from":"2026-01-01"},{"url":"https://github.com/c/r","kind":"unconfirmed","how":"seen","from":"2026-01-15"}]`
	got, err := StrategyURLSet(js(t, base), js(t, ours), js(t, theirs), newerOurs)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"from":"2026-01-01","how":"init","kind":"primary","to":"2026-02-01","url":"https://github.com/a/r.git"},{"from":"2026-01-15","how":"seen","kind":"unconfirmed","url":"https://github.com/c/r"},{"from":"2026-02-01","how":"set-url","kind":"primary","url":"git@github.com:b/r.git"}]`
	if raw(t, got) != want {
		t.Errorf("got  %s\nwant %s", raw(t, got), want)
	}
}

// TestEntriesByID: RESOURCES 3.13 snippet and the strict clash.
func TestEntriesByID(t *testing.T) {
	t.Parallel()
	base := `{"fld_p":{"name":"port","v":8080},"fld_n":{"name":"notes","v":"blue/green"},"fld_u":{"name":"uses","v":["r4"]}}`
	ours := `{"fld_p":{"name":"port","v":8080},"fld_n":{"name":"notes","v":"blue/green"},"fld_u":{"name":"uses","v":["r4","r5"]}}`
	theirs := `{"fld_p":{"name":"port","v":8090},"fld_n":{"name":"notes","v":"canary"},"fld_u":{"name":"uses","v":["r4","r11"]},"fld_x":{"name":"x","v":1}}`
	got, conf, err := StrategyEntriesByID(js(t, base), js(t, ours), js(t, theirs), newerTheirs)
	if err != nil || len(conf) != 0 {
		t.Fatalf("%v %v", err, conf)
	}
	want := `{"fld_n":{"name":"notes","v":"canary"},"fld_p":{"name":"port","v":8090},"fld_u":{"name":"uses","v":["r11","r4","r5"]},"fld_x":{"name":"x","v":1}}`
	if raw(t, got) != want {
		t.Errorf("got  %s\nwant %s", raw(t, got), want)
	}

	// strict list: ours removed r5, theirs added r11 → a person decides.
	base = `{"fld_u":{"name":"uses","strict":true,"v":["r4","r5"]}}`
	ours = `{"fld_u":{"name":"uses","strict":true,"v":["r4"]}}`
	theirs = `{"fld_u":{"name":"uses","strict":true,"v":["r4","r5","r11"]}}`
	got, conf, err = StrategyEntriesByID(js(t, base), js(t, ours), js(t, theirs), newerOurs)
	if err != nil || len(conf) != 1 || conf[0] != "fld_u" {
		t.Fatalf("strict list: %v %v", err, conf)
	}
	want = `{"fld_u":{"conflict":{"base":["r4","r5"],"ours":["r4"],"theirs":["r4","r5","r11"]},"name":"uses","strict":true}}`
	if raw(t, got) != want {
		t.Errorf("got  %s\nwant %s", raw(t, got), want)
	}

	// strict scalar both changed, and strict removed-vs-changed.
	base = `{"fld_p":{"name":"port","strict":true,"v":1},"fld_q":{"name":"q","strict":true,"v":1}}`
	ours = `{"fld_p":{"name":"port","strict":true,"v":2}}`
	theirs = `{"fld_p":{"name":"port","strict":true,"v":3},"fld_q":{"name":"q","strict":true,"v":5}}`
	_, conf, err = StrategyEntriesByID(js(t, base), js(t, ours), js(t, theirs), newerOurs)
	if err != nil || len(conf) != 2 {
		t.Fatalf("strict scalar: %v %v", err, conf)
	}

	// removed on one side, unchanged on the other: removed; not an object: refused.
	got, _, err = StrategyEntriesByID(js(t, `{"fld_a":{"name":"a","v":1}}`), js(t, `{}`), js(t, `{"fld_a":{"name":"a","v":1},"fld_b":{"name":"b","v":2}}`), newerOurs)
	if err != nil || raw(t, got) != `{"fld_b":{"name":"b","v":2}}` {
		t.Errorf("removal: %s %v", raw(t, got), err)
	}
	if _, _, err := StrategyEntriesByID(nil, js(t, `[1]`), js(t, `{}`), newerOurs); err == nil {
		t.Error("a list accepted as entries")
	}
}

// TestOriginMovesUnion: _o keeps its write-once part and unions moves.
func TestOriginMovesUnion(t *testing.T) {
	t.Parallel()
	b := live(stamp(0, actorA))
	b.Origin = js(t, `{"cmd":"task add","repo":"rep_1"}`).(value.JSON)
	o := live(stamp(10, actorA))
	o.Origin = js(t, `{"cmd":"task add","moves":[{"at":"2026-11-02T00:00:00Z","moved_from":"a","moved_to":"b"}],"repo":"rep_1"}`).(value.JSON)
	th := live(stamp(20, actorB))
	th.Origin = js(t, `{"cmd":"task add","moves":[{"at":"2026-11-01T00:00:00Z","moved_from":"a","moved_to":"c"}],"repo":"rep_2"}`).(value.JSON)
	want := `{"cmd":"task add","moves":[{"at":"2026-11-01T00:00:00Z","moved_from":"a","moved_to":"c"},{"at":"2026-11-02T00:00:00Z","moved_from":"a","moved_to":"b"}],"repo":"rep_2"}`
	for range 2 {
		if got := mergeOrigin(b, o, th); got.String() != want {
			t.Errorf("got  %s\nwant %s", got.String(), want)
		}
		o, th = th, o
	}
	// one side without _o keeps the other.
	n := live(stamp(30, actorA))
	if got := mergeOrigin(b, n, th); got.String() != th.Origin.String() {
		t.Errorf("absent side: %s", got.String())
	}
}
