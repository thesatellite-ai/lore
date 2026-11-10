package row

import (
	"strings"
	"testing"
)

// TestUnknownFieldsSurviveEveryPath (FORMAT_EVOLUTION rule 1, the model
// part): a key this lore does not model — a table field or an underscore
// key such as a future `_w` member — comes back byte-identical after decode
// → encode, for every value shape, from a v1 and a v2 document. The
// backend and merge paths keep the encoded bytes (rowfs, logic/merge), so
// this matrix is the one place a value could be reshaped.
func TestUnknownFieldsSurviveEveryPath(t *testing.T) {
	values := map[string]string{
		"string":     `"x"`,
		"big int":    `9007199254740993`,
		"decimal":    `0.1`,
		"nested":     `{"a":{"b":{"c":1}}}`,
		"array objs": `[{"a":1},{"b":[true,null]}]`,
		"null":       `null`,
		"empty obj":  `{}`,
		"unicode":    `"ünï ☃ 😀"`,
	}
	const id = `"id":"mem_01936f0000007a1b8c2d3e4f5a6b7c8d","updated_at":"2026-11-04T10:42:07.123Z"`
	for _, key := range []string{"zz_future", "_zz_future"} {
		for name, v := range values {
			for _, ver := range []string{`"_v":1,`, `"_v":2,"_hlc":"2026-11-04T10:42:07.123Z/0000/act_01936f0000007a1b8c2d3e4f5a6b7c8d",`} {
				doc := `{` + ver + `"_table":"memories",` + id + `,"` + key + `":` + v + `}`
				r, err := Decode([]byte(doc))
				if err != nil {
					t.Fatalf("%s/%s: decode: %v", key, name, err)
				}
				out, err := Encode(r)
				if err != nil {
					t.Fatalf("%s/%s: encode: %v", key, name, err)
				}
				r2, err := Decode(out)
				if err != nil {
					t.Fatalf("%s/%s: re-decode: %v", key, name, err)
				}
				out2, _ := Encode(r2)
				if string(out) != string(out2) || !strings.Contains(compact(string(out)), `"`+key+`":`+compact(v)) {
					t.Fatalf("%s/%s: value changed:\n%s", key, name, out)
				}
			}
		}
	}
}

// compact drops the canonical form's whitespace outside strings (enough
// for these fixtures, whose strings hold no spaces except the unicode one).
func compact(s string) string {
	var b strings.Builder
	in := false
	for i, c := range s {
		switch {
		case c == '"' && (i == 0 || s[i-1] != '\\'):
			in = !in
		case !in && (c == ' ' || c == '\n'):
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}
