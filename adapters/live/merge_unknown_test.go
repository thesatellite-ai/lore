package live

// TestUnknownFieldsSurviveLiveMerge (FORMAT_EVOLUTION rule 1, the live
// merge twin of model/row TestUnknownFieldsSurviveEveryPath): a key this
// lore does not model — a table field no schema declares, or an underscore
// key such as a future `_w` member — survives MergeTrees with its value
// unchanged, for every value shape, whichever side added or changed it:
// for v2 and v1 row bodies (both carry `_v`, so both merge through
// logic/merge; a clash takes the newer side) and for a body without `_v` (a
// hand-written file: the merge3 path, where a clash keeps both values in a
// loud conflict). Break-verified: logic/merge dropping Reserved or an
// undeclared field fails every v2 and v1 case.

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/canonjson"
	lorerow "github.com/thesatellite-ai/lore/model/row"
)

// unknownValues are the value shapes of the model twin.
var unknownValues = map[string]string{
	"string":     `"x"`,
	"big int":    `9007199254740993`,
	"decimal":    `0.1`,
	"nested":     `{"a":{"b":{"c":1}}}`,
	"array objs": `[{"a":1},{"b":[true,null]}]`,
	"null":       `null`,
	"empty obj":  `{}`,
	"unicode":    `"ünï ☃ 😀"`,
}

// unknownMergeCase places the unknown key on the three sides: "" means
// absent; otherwise the raw value. newerOurs makes ours the newer edit
// (theirs is newer otherwise).
type unknownMergeCase struct {
	name       string
	base, o, t string
	newerOurs  bool
	// bothChanged: both sides set different values. A row merge takes the
	// newer side; the merge3 path (a body without `_v`, no strategies)
	// keeps both in a loud conflict instead, so nothing is lost either way.
	bothChanged bool
	want        string // "" = the key must be absent after the merge
}

// unknownCases is the three-way matrix for one value v (other is a second
// value of the same shape family, so "both changed" is a real clash).
func unknownCases(v, other string) []unknownMergeCase {
	return []unknownMergeCase{
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

const (
	unkTable = "memories"
	unkID    = "mem_01936f3300007d4e8f5a6b7c8d9e0f1a"
	unkActor = "act_01936f0000007a1b8c2d3e4f5a6b7c8d"
)

// unkDoc is one side's row body in format ver ("v2" with _hlc at second
// sec, "v1", or "bare" without `_v`), with key set to raw when raw is not "".
func unkDoc(ver string, sec int, key, raw string) string {
	ts := "2026-11-05T10:00:0" + strconv.Itoa(sec) + ".000Z"
	head := ""
	switch ver {
	case "v1":
		head = `"_v":1,`
	case "v2":
		head = `"_v":2,"_hlc":"` + ts + `/0000/` + unkActor + `",`
	}
	doc := `{` + head + `"_table":"` + unkTable + `","id":"` + unkID + `","kind":"core","body":"b","updated_at":"` + ts + `","updated_by":"` + unkActor + `"`
	if raw != "" {
		doc += `,"` + key + `":` + raw
	}
	return doc + `}`
}

// unkFile wraps a body into a live file whose block stamp orders the sides
// the same way as the body's _hlc.
func unkFile(t *testing.T, doc string, sec int) []byte {
	t.Helper()
	f := fileWith(t, doc)
	f.Block.HLC = HLC{Wall: t0.Add(timeSeconds(sec))}
	b, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// timeSeconds is n seconds.
func timeSeconds(n int) time.Duration { return time.Duration(n) * time.Second }

func TestUnknownFieldsSurviveLiveMerge(t *testing.T) {
	p := RowPath("proj", unkTable, unkID)
	for _, key := range []string{"zz_future", "_zz_future"} {
		for vname, v := range unknownValues {
			other := `"other"`
			if v == other {
				other = `"another"`
			}
			for _, ver := range []string{"v2", "v1", "bare"} {
				for _, c := range unknownCases(v, other) {
					name := key + "/" + vname + "/" + c.name + "/" + ver
					// Base at second 1; the newer side at 3, the other at 2.
					so, st := 2, 3
					if c.newerOurs {
						so, st = 3, 2
					}
					base := Tree{p: unkFile(t, unkDoc(ver, 1, key, c.base), 1)}
					ours := Tree{p: unkFile(t, unkDoc(ver, so, key, c.o), so)}
					theirs := Tree{p: unkFile(t, unkDoc(ver, st, key, c.t), st)}
					out, _, err := MergeTrees(base, ours, theirs, nil)
					if err != nil {
						t.Fatalf("%s: merge: %v", name, err)
					}
					got, has := unkMergedValue(t, name, out[p], key)
					if ver == "bare" && c.bothChanged {
						unkCheckLoudConflict(t, name, out[p], got, c.want)
						continue
					}
					switch {
					case c.want == "" && has:
						t.Errorf("%s: a key one side removed came back as %s", name, got)
					case c.want != "" && !has:
						t.Errorf("%s: the key was lost", name)
					case c.want != "" && got != unkCompact(t, c.want):
						t.Errorf("%s: value %s, want %s", name, got, unkCompact(t, c.want))
					}
				}
			}
		}
	}
}

// unkCheckLoudConflict: the merged value is a marker block holding both
// sides' values, and the file is flagged as a conflict.
func unkCheckLoudConflict(t *testing.T, name string, file []byte, got, want string) {
	t.Helper()
	f, err := DecodeFile(file)
	if err != nil {
		t.Fatal(err)
	}
	text, err := strconv.Unquote(got)
	if err != nil {
		t.Errorf("%s: both sides changed, want a marker block, got %s", name, got)
		return
	}
	w := unkCompact(t, want)
	if u, err := strconv.Unquote(w); err == nil {
		w = u
	}
	if !strings.Contains(text, w) || !strings.Contains(text, "older") || !f.HasConflictMarkers() {
		t.Errorf("%s: both sides changed, want both values kept in a conflict, got %s (conflict=%v)", name, got, f.HasConflictMarkers())
	}
}

// unkMergedValue returns key's value in the merged live file, compact.
func unkMergedValue(t *testing.T, name string, b []byte, key string) (string, bool) {
	t.Helper()
	if b == nil {
		t.Fatalf("%s: the row is gone after the merge", name)
	}
	f, err := DecodeFile(b)
	if err != nil {
		t.Fatalf("%s: merged file: %v", name, err)
	}
	if v2, ok := f.Body[lorerow.KeyVersion]; ok {
		if n, _ := v2.AsNumber(); n == "2" {
			// A v2 merge result must still be a row document.
			body, encErr := f.EncodeBody()
			if encErr != nil {
				t.Fatal(encErr)
			}
			if _, decErr := lorerow.Decode(body); decErr != nil {
				t.Fatalf("%s: merged v2 body is not a row: %v", name, decErr)
			}
		}
	}
	v, has := f.Body[key]
	if !has {
		return "", false
	}
	raw, err := canonjson.Encode(canonjson.Object{"k": v})
	if err != nil {
		t.Fatal(err)
	}
	s := strings.TrimSuffix(strings.TrimPrefix(unkSquash(string(raw)), `{"k":`), `}`)
	return s, true
}

// unkCompact renders raw JSON the way unkMergedValue does.
func unkCompact(t *testing.T, raw string) string {
	t.Helper()
	v, err := canonjson.Decode([]byte(`{"k":`+raw+`}`), canonjson.DefaultMaxDepth)
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonjson.Encode(v)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(strings.TrimPrefix(unkSquash(string(b)), `{"k":`), `}`)
}

// unkSquash drops whitespace outside strings (canonical output may be
// indented).
func unkSquash(s string) string {
	var b strings.Builder
	in, esc := false, false
	for _, c := range s {
		switch {
		case esc:
			esc = false
		case in && c == '\\':
			esc = true
		case c == '"':
			in = !in
		case !in && (c == ' ' || c == '\n' || c == '\t'):
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}
