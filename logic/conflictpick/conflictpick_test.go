package conflictpick

import (
	"testing"

	"github.com/thesatellite-ai/lore/logic/canonjson"
)

// block is a git-shaped conflict block (label after the open and close
// markers, as git and logic/merge3 write them).
func block(ours, theirs string) string {
	return "<<<<<<< HEAD\n" + ours + "\n=======\n" + theirs + "\n>>>>>>> feat"
}

// TestText: input-space matrix over block count, position, side, diff3
// base sections, multi-line sides, empty sides and unfinished blocks.
func TestText(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		kept, other string
	}{
		{"no markers: unchanged", "plain text\nline two", "plain text\nline two", "plain text\nline two"},
		{"empty string", "", "", ""},
		{"one block alone", block("mine", "theirs"), "mine", "theirs"},
		{"block between lines", "a\n" + block("x", "y") + "\nz", "a\nx\nz", "a\ny\nz"},
		{"two blocks", block("a1", "b1") + "\nmid\n" + block("a2", "b2"), "a1\nmid\na2", "b1\nmid\nb2"},
		{"multi-line sides", block("m1\nm2", "t1\nt2\nt3"), "m1\nm2", "t1\nt2\nt3"},
		{"empty side", "<<<<<<< HEAD\n=======\ntheirs\n>>>>>>> feat", "", "theirs"},
		{"diff3 base never kept", "<<<<<<< HEAD\nmine\n||||||| base\nold\n=======\ntheirs\n>>>>>>> feat", "mine", "theirs"},
		{"trailing newline kept", block("m", "t") + "\n", "m\n", "t\n"},
		{"unicode sides", block("été ✓", "日本"), "été ✓", "日本"},
		// An unfinished block (no close) is not a complete conflict: left as written.
		{"unfinished block", "<<<<<<< HEAD\nmine\n=======\ntheirs", "<<<<<<< HEAD\nmine\n=======\ntheirs", "<<<<<<< HEAD\nmine\n=======\ntheirs"},
		// G-11: a marked text merged again nests; the same side is kept at every level.
		{"nested in the first section", "<<<<<<< HEAD\n" + block("x", "y") + "\n=======\nz\n>>>>>>> feat", "x", "z"},
		{"nested in the second section", "<<<<<<< HEAD\na\n=======\n" + block("b", "c") + "\n>>>>>>> feat", "a", "c"},
		{"three levels", "<<<<<<< HEAD\na\n=======\n<<<<<<< HEAD\nb\n=======\n" + block("c", "d") + "\n>>>>>>> feat\n>>>>>>> feat", "a", "d"},
		{"close before split: unchanged", "<<<<<<< HEAD\nx\n>>>>>>> feat\n=======\ny", "<<<<<<< HEAD\nx\n>>>>>>> feat\n=======\ny", "<<<<<<< HEAD\nx\n>>>>>>> feat\n=======\ny"},
		{"complete then unfinished", block("a", "b") + "\n<<<<<<< HEAD\nx\n=======\ny", "a\n<<<<<<< HEAD\nx\n=======\ny", "b\n<<<<<<< HEAD\nx\n=======\ny"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Text(c.in, false); got != c.kept {
				t.Errorf("kept: %q, want %q", got, c.kept)
			}
			if got := Text(c.in, true); got != c.other {
				t.Errorf("other: %q, want %q", got, c.other)
			}
		})
	}
}

// obj decodes a JSON object (test input).
func obj(t *testing.T, s string) canonjson.Object {
	t.Helper()
	o, err := canonjson.Decode([]byte(s), canonjson.DefaultMaxDepth)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// enc is the canonical text of o.
func enc(t *testing.T, o canonjson.Object) string {
	t.Helper()
	b, err := canonjson.Encode(o)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// jq is s as a JSON string literal body (newlines escaped).
func jq(s string) string {
	v, err := canonjson.String(s).MarshalJSON()
	if err != nil {
		panic(err)
	}
	return string(v)
}

// TestObject: every string at any depth (fields, arrays, nested objects)
// is settled; numbers, bools, nulls and clean strings are untouched; meta
// conflict entries take the chosen side's value, a null side removes the
// entry; the input is not modified.
func TestObject(t *testing.T) {
	b := jq(block("mine", "theirs"))
	in := `{"id":"tsk_1","title":` + b + `,"n":3,"ok":true,"none":null,"clean":"x",` +
		`"tags":[` + b + `,"keep"],"nested":{"deep":{"s":` + b + `}},` +
		`"meta":{"mky_a":{"name":"a","conflict":{"base":1,"ours":2,"theirs":3}},` +
		`"mky_b":{"name":"b","conflict":{"base":1,"ours":null,"theirs":4}},` +
		`"mky_c":{"name":"c","v":"clean"}}}`
	src := obj(t, in)
	before := enc(t, src)
	cases := []struct {
		name  string
		other bool
		want  string
	}{
		{"kept", false, `{"id":"tsk_1","title":"mine","n":3,"ok":true,"none":null,"clean":"x","tags":["mine","keep"],"nested":{"deep":{"s":"mine"}},` +
			`"meta":{"mky_a":{"name":"a","v":2},"mky_c":{"name":"c","v":"clean"}}}`},
		{"other", true, `{"id":"tsk_1","title":"theirs","n":3,"ok":true,"none":null,"clean":"x","tags":["theirs","keep"],"nested":{"deep":{"s":"theirs"}},` +
			`"meta":{"mky_a":{"name":"a","v":3},"mky_b":{"name":"b","v":4},"mky_c":{"name":"c","v":"clean"}}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Object(src, c.other)
			if enc(t, got) != enc(t, obj(t, c.want)) {
				t.Fatalf("got\n%s\nwant\n%s", enc(t, got), enc(t, obj(t, c.want)))
			}
			if HasMarkers(got) {
				t.Fatal("a settled object still reports markers")
			}
			if enc(t, src) != before {
				t.Fatal("Object modified its input")
			}
		})
	}
}

// TestHasMarkers: where a conflict can hide, and what is not one.
func TestHasMarkers(t *testing.T) {
	b := jq(block("a", "b"))
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"clean row", `{"id":"x","title":"t","n":1}`, false},
		{"empty object", `{}`, false},
		{"field", `{"title":` + b + `}`, true},
		{"array item", `{"tags":["x",` + b + `]}`, true},
		{"nested object", `{"a":{"b":{"c":` + b + `}}}`, true},
		{"object key", `{"a":{` + b + `:1}}`, true},
		{"top-level key", `{` + b + `:1}`, true},
		{"meta conflict entry", `{"meta":{"mky_a":{"conflict":{"ours":1,"theirs":2}}}}`, true},
		{"meta clean entry", `{"meta":{"mky_a":{"v":1}}}`, false},
		{"meta not an object", `{"meta":"x"}`, false},
		{"unfinished block", `{"title":` + jq("<<<<<<< HEAD\na\n=======\nb") + `}`, false},
		{"marker text without newlines", `{"title":"<<<<<<< ======= >>>>>>>"}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HasMarkers(obj(t, c.in)); got != c.want {
				t.Fatalf("HasMarkers = %v, want %v", got, c.want)
			}
		})
	}
}

// TestObjectKeyMarkersStay: a marker block in an object key is not settled
// (a key is not a value to choose), so HasMarkers still reports it and the
// caller refuses (G-SYNC-1: "edit the row by hand").
func TestObjectKeyMarkersStay(t *testing.T) {
	in := obj(t, `{"a":{`+jq(block("k1", "k2"))+`:1}}`)
	if !HasMarkers(Object(in, false)) {
		t.Fatal("a key holding markers must still be reported after Object")
	}
}
