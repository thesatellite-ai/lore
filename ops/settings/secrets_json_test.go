package settings

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

const jsonRef = "${secret:gopass://acme/db?holder=mch_1&label=old.box}"

// relabel is the test rewriter: every gopass target gets label mira.box.
func relabel(t refs.SecretTarget) (refs.SecretTarget, bool) {
	if t.Manager != "gopass" {
		return t, false
	}
	return setOption(t, refs.OptLabel, "mira.box"), true
}

// canonical is raw in canonical compact form, for a semantic comparison
// (key order and escaping of the stored bytes may differ).
func canonical(t *testing.T, raw []byte) string {
	t.Helper()
	b, err := canon.Compact(raw)
	if err != nil {
		t.Fatalf("not json: %s", raw)
	}
	return string(b)
}

// TestJSONFieldRefs (OV13b): references inside json string leaves (object
// values, array items, any depth, and a bare json string) are found by
// lore secrets list and rewritten by the bulk rewrites; object keys are
// names (never read); numbers, bools and null keep their value; a json
// value with no reference to change is returned untouched; unicode and
// "&" in strings survive the round trip.
func TestJSONFieldRefs(t *testing.T) {
	want := "${secret:gopass://acme/db?holder=mch_1&label=mira.box}"
	cases := []struct {
		name    string
		in      string
		strs    int    // string leaves jsonStrings returns
		changed int    // references rewritten
		want    string // semantic result ("" = unchanged)
	}{
		{name: "object value", in: `{"dsn":"` + jsonRef + `","port":5432}`, strs: 1, changed: 1, want: `{"dsn":"` + want + `","port":5432}`},
		{name: "nested object", in: `{"db":{"main":{"pw":"x ` + jsonRef + ` y"}},"on":true,"n":null}`, strs: 1, changed: 1,
			want: `{"db":{"main":{"pw":"x ` + want + ` y"}},"on":true,"n":null}`},
		{name: "array items", in: `["a","` + jsonRef + `",3,["` + jsonRef + `"]]`, strs: 3, changed: 2, want: `["a","` + want + `",3,["` + want + `"]]`},
		{name: "bare string", in: `"` + jsonRef + `"`, strs: 1, changed: 1, want: `"` + want + `"`},
		{name: "key is not text", in: `{"` + jsonRef + `":1}`, strs: 0, changed: 0},
		{name: "no ref", in: `{"a":"plain","b":1.50}`, strs: 1, changed: 0},
		{name: "env target untouched", in: `{"a":"${secret:env://DB_PW}"}`, strs: 1, changed: 0},
		{name: "unicode and amp kept", in: `{"note":"köln & <b> ` + jsonRef + `"}`, strs: 1, changed: 1, want: `{"note":"köln & <b> ` + want + `"}`},
		{name: "number precision kept", in: `{"big":12345678901234567890,"r":"` + jsonRef + `"}`, strs: 1, changed: 1,
			want: `{"big":12345678901234567890,"r":"` + want + `"}`},
		{name: "empty object", in: `{}`, strs: 0, changed: 0},
		{name: "scalar number", in: `42`, strs: 0, changed: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, err := value.JSONOf([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if got := len(jsonStrings(v.Raw())); got != c.strs {
				t.Fatalf("jsonStrings = %d leaves, want %d", got, c.strs)
			}
			if c.strs > 0 && c.changed > 0 && !slices.ContainsFunc(fieldTexts(v), func(s string) bool { return refs.References(s) != nil }) {
				t.Fatal("fieldTexts does not expose the reference to lore secrets list")
			}
			nv, n, err := jsonRewrite(v, relabel)
			if err != nil {
				t.Fatal(err)
			}
			if n != c.changed {
				t.Fatalf("changed %d, want %d", n, c.changed)
			}
			if c.want == "" {
				if string(nv.Raw()) != string(v.Raw()) {
					t.Fatalf("an unchanged value was re-encoded: %s", nv.Raw())
				}
				return
			}
			if got, exp := canonical(t, nv.Raw()), canonical(t, []byte(c.want)); got != exp {
				t.Fatalf("got %s\nwant %s", nv.Raw(), c.want)
			}
		})
	}
}

// TestRowPatchJSONField: rowPatch rewrites a json field beside a text
// field and leaves a field without references out of the patch.
func TestRowPatchJSONField(t *testing.T) {
	j, err := value.JSONOf([]byte(`{"dsn":"` + jsonRef + `","port":5432}`))
	if err != nil {
		t.Fatal(err)
	}
	other, err := value.JSONOf([]byte(`{"x":1}`))
	if err != nil {
		t.Fatal(err)
	}
	body, err := value.TextOf("see " + jsonRef)
	if err != nil {
		t.Fatal(err)
	}
	patch, n, err := rowPatch(row.Row{Fields: map[string]value.Value{"config": j, "other": other, "body": body}}, relabel)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || len(patch) != 2 || patch["other"] != nil {
		t.Fatalf("patch %v n=%d", patch, n)
	}
	if _, ok := patch["config"].(value.JSON); !ok {
		t.Fatalf("config stays json: %T", patch["config"])
	}
}
