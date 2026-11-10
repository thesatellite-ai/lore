package settings

// References inside json fields (OV13b): `lore secrets list` and the bulk
// rewrites (move, relabel, rename-entry) read and rewrite the string
// values of a json field too, so a reference kept in structured data is
// never skipped. The walk keeps every non-string value as written and
// re-encodes only a json value whose strings changed.
//
//	fieldTexts(json) ─▶ jsonStrings: every string leaf (object values, array items)
//	rowPatch(json)   ─▶ jsonRewrite: rewriteText on each leaf ─▶ value.JSONOf

import (
	"bytes"
	"encoding/json"

	"github.com/thesatellite-ai/lore/model/value"
)

// jsonStrings is every string leaf of raw (object member values and array
// items, at any depth); keys are names, not text, and are left out.
func jsonStrings(raw json.RawMessage) []string {
	var out []string
	walkJSON(raw, func(s string) string { out = append(out, s); return s })
	return out
}

// jsonRewrite applies fix to every string leaf of v; n counts the
// references changed. An unchanged value is returned as is.
func jsonRewrite(v value.JSON, fix rewriter) (value.JSON, int, error) {
	n := 0
	out, ok := walkJSON(v.Raw(), func(s string) string {
		ns, k := rewriteText(s, fix)
		n += k
		return ns
	})
	if !ok || n == 0 {
		return v, 0, nil
	}
	nv, err := value.JSONOf(out)
	return nv, n, err
}

// walkJSON calls f on every string leaf and returns raw rebuilt with f's
// results; ok is false when raw does not decode (it is left alone).
func walkJSON(raw json.RawMessage, f func(string) string) (json.RawMessage, bool) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 {
		return raw, false
	}
	switch t[0] {
	case '"':
		var s string
		if json.Unmarshal(t, &s) != nil {
			return raw, false
		}
		b, err := json.Marshal(f(s))
		return b, err == nil
	case '{':
		var m map[string]json.RawMessage
		if json.Unmarshal(t, &m) != nil {
			return raw, false
		}
		for k, v := range m {
			nv, ok := walkJSON(v, f)
			if !ok {
				return raw, false
			}
			m[k] = nv
		}
		b, err := json.Marshal(m)
		return b, err == nil
	case '[':
		var a []json.RawMessage
		if json.Unmarshal(t, &a) != nil {
			return raw, false
		}
		for i, v := range a {
			nv, ok := walkJSON(v, f)
			if !ok {
				return raw, false
			}
			a[i] = nv
		}
		b, err := json.Marshal(a)
		return b, err == nil
	}
	return raw, true // number, bool, null: kept as written
}
