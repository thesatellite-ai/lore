package live

// TestUnknownFieldsSurviveLiveMerge (FORMAT_EVOLUTION rule 1, the live
// merge twin of model/row TestUnknownFieldsSurviveEveryPath): a key this
// lore does not model — a table field no schema declares, or an underscore
// key such as a future `_w` member — survives MergeTrees with its value
// unchanged, for every value shape, whichever side added or changed it:
// for v2 and v1 row bodies (both carry `_v`, so both merge through
// logic/merge) and for a body without `_v` (a hand-written file: the
// merge3 path); a clash takes the newer side on every path, never markers
// (L-19: markers on the shared live ref broke every clone). Break-verified: logic/merge dropping Reserved or an
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
	// bothChanged: both sides set different values; the newer side wins
	// on every path, and the merge note names the replaced value.
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
					if c.bothChanged {
						unkCheckNoMarkers(t, name, out[p])
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

// unkCheckNoMarkers: a clash never leaves conflict markers in the merged
// live file (L-19).
func unkCheckNoMarkers(t *testing.T, name string, file []byte) {
	t.Helper()
	f, err := DecodeFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if f.HasConflictMarkers() || f.Block.Conflict {
		t.Errorf("%s: both sides changed, want the newer value and no markers, got %s", name, file)
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

// TestLiveV2TextClashKeepsNewer (devco D-219, L-19): two clones changed
// the free-text body of one v2 row at once. The live merge keeps the newer
// edit whichever side is ours, writes no markers (the live ref is shared
// by every clone), and its note names the replaced text.
func TestLiveV2TextClashKeepsNewer(t *testing.T) {
	p := RowPath("proj", unkTable, unkID)
	doc := func(sec int, body string) []byte {
		return unkFile(t, strings.Replace(unkDoc("v2", sec, "", ""), `"body":"b"`, `"body":"`+body+`"`, 1), sec)
	}
	base := Tree{p: doc(1, "b")}
	for _, swap := range []bool{false, true} {
		ours, theirs := Tree{p: doc(2, "from Mei")}, Tree{p: doc(3, "from Tom")}
		if swap {
			ours, theirs = theirs, ours
		}
		out, notes, err := MergeTrees(base, ours, theirs, nil)
		if err != nil {
			t.Fatal(err)
		}
		f, err := DecodeFile(out[p])
		if err != nil {
			t.Fatal(err)
		}
		if f.HasConflictMarkers() || f.Block.Conflict || f.Body.Str("body") != "from Tom" {
			t.Fatalf("swap=%v: body %q conflict=%v, want the newer text and no markers", swap, f.Body.Str("body"), f.Block.Conflict)
		}
		if len(notes) != 1 || !strings.Contains(notes[0].Describe(), `body was "from Mei"`) {
			t.Fatalf("swap=%v: notes %+v, want the replaced text named", swap, notes)
		}
	}
}
