package host

// TestUnknownFieldsSurviveMergeDriver (FORMAT_EVOLUTION rule 1, the
// merge-driver twin of model/row TestUnknownFieldsSurviveEveryPath): `lore
// merge-driver` over three row files keeps a key this lore does not model —
// a table field no schema declares, or an underscore key such as a future
// `_w` member — with its value unchanged, for every value shape, whichever
// side added or changed it, from v2 and from v1 documents (a v1 side is
// upcast and the result written at v2). A clash on such a key is settled
// like any non-text field: the newer `_hlc` wins and stderr says so.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
)

// mduValues are the value shapes of the model twin.
var mduValues = map[string]string{
	"string":     `"x"`,
	"big int":    `9007199254740993`,
	"decimal":    `0.1`,
	"nested":     `{"a":{"b":{"c":1}}}`,
	"array objs": `[{"a":1},{"b":[true,null]}]`,
	"null":       `null`,
	"empty obj":  `{}`,
	"unicode":    `"ünï ☃ 😀"`,
}

// mduCase places the key on the three sides ("" = absent). newerOurs makes
// ours the newer edit; want "" means the key must be gone.
type mduCase struct {
	name        string
	base, o, t  string
	newerOurs   bool
	bothChanged bool
	want        string
}

func mduCases(v, other string) []mduCase {
	return []mduCase{
		{name: "only theirs added it", t: v, want: v},
		{name: "only ours added it", o: v, want: v},
		{name: "both added the same", o: v, t: v, want: v},
		{name: "in base, untouched", base: v, o: v, t: v, want: v},
		{name: "theirs changed it", base: other, o: other, t: v, want: v},
		{name: "ours changed it", base: other, o: v, t: other, want: v},
		{name: "both changed, theirs newer", base: other, o: `"older"`, t: v, bothChanged: true, want: v},
		{name: "both changed, ours newer", base: other, o: v, t: `"older"`, newerOurs: true, bothChanged: true, want: v},
		{name: "theirs removed it", base: v, o: v, want: ""},
	}
}

// mduDoc is one side's memories row at second sec, version ver ("v2" with
// _hlc, or "v1"), with key set to raw when raw is not "".
func mduDoc(ver string, sec int, key, raw string) []byte {
	ts := "2026-11-05T10:00:0" + string(rune('0'+sec)) + ".000Z"
	head := `"_v":1,`
	if ver == "v2" {
		head = `"_v":2,"_hlc":"` + ts + `/0000/` + mdActorA.String() + `",`
	}
	doc := `{` + head + `"_table":"memories","id":"` + mdRowID.String() + `","kind":"core","body":"b","updated_at":"` + ts + `","updated_by":"` + mdActorA.String() + `"`
	if raw != "" {
		doc += `,"` + key + `":` + raw
	}
	return []byte(doc + `}`)
}

// mduCompact is raw JSON without insignificant whitespace, numbers kept
// as written (json.Compact never reparses a number).
func mduCompact(t *testing.T, raw []byte) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		t.Fatalf("compact %s: %v", raw, err)
	}
	return b.String()
}

func TestUnknownFieldsSurviveMergeDriver(t *testing.T) {
	const rowPath = ".lore/data/memories/mem_01936f3300007d4e8f5a6b7c8d9e0f1a.json"
	for _, key := range []string{"zz_future", "_zz_future"} {
		for vname, v := range mduValues {
			other := `"other"`
			if v == other {
				other = `"another"`
			}
			for _, ver := range []string{"v2", "v1"} {
				for _, c := range mduCases(v, other) {
					name := key + "/" + vname + "/" + c.name + "/" + ver
					so, st := 2, 3
					if c.newerOurs {
						so, st = 3, 2
					}
					out, stderr, err := runDriver(t, rowPath, mduDoc(ver, 1, key, c.base), mduDoc(ver, so, key, c.o), mduDoc(ver, st, key, c.t))
					if err != nil {
						t.Fatalf("%s: driver: %v (stderr %s)", name, err, stderr)
					}
					r, err := row.Decode(out)
					if err != nil {
						t.Fatalf("%s: merged file is not a row: %v\n%s", name, err, out)
					}
					if r.SourceVersion != row.FormatVersion {
						t.Errorf("%s: merged at _v %d", name, r.SourceVersion)
					}
					got, has := mduValueOf(t, out, key)
					switch {
					case c.want == "" && has:
						t.Errorf("%s: a key one side removed came back as %s", name, got)
					case c.want != "" && !has:
						t.Errorf("%s: the key was lost:\n%s", name, out)
					case c.want != "" && got != mduCompact(t, []byte(c.want)):
						t.Errorf("%s: value %s, want %s", name, got, mduCompact(t, []byte(c.want)))
					}
					if c.bothChanged && !strings.Contains(stderr, key) {
						t.Errorf("%s: a clash on %s was settled silently (stderr %q)", name, key, stderr)
					}
				}
			}
		}
	}
}

// mduValueOf returns key's raw value in the merged document, compact.
func mduValueOf(t *testing.T, doc []byte, key string) (string, bool) {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(doc, &m); err != nil {
		t.Fatalf("merged file: %v", err)
	}
	raw, ok := m[key]
	if !ok {
		return "", false
	}
	return mduCompact(t, raw), true
}
