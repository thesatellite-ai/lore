package canon

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

// goldenInput was fed to v0.1.17's canonjson.Canonicalize; its output is
// testdata/canonjson_golden.json. Matching it byte for byte proves row files
// written by the old and the new code are identical.
const goldenInput = `{"title":"Checkout <needs> retry & 502","_v":2,"id":"mem_01936f2a7c4b7e1d8a3f5b6c7d8e9f00","n":1.50,"big":12345678901234567890,"list":[],"obj":{},"nested":{"z":[1,{"b":true,"a":null}],"a":" xé"},"emoji":"😀\t\n","neg":-0.0e5}`

func TestDocumentMatchesCanonjson(t *testing.T) {
	t.Parallel()
	want, err := os.ReadFile("testdata/canonjson_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Document([]byte(goldenInput))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	again, err := Document(got)
	if err != nil || !bytes.Equal(again, got) {
		t.Fatal("document form is not a fixed point")
	}
}

func TestObjectAndMembers(t *testing.T) {
	t.Parallel()
	doc, err := Object([]Member{{Key: "b", Raw: []byte(`{"y":1, "x":2}`)}, {Key: "a", Raw: []byte(`"s"`)}})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"a\": \"s\",\n  \"b\": {\n    \"x\": 2,\n    \"y\": 1\n  }\n}\n"
	if string(doc) != want {
		t.Fatalf("got %q", doc)
	}
	ms, err := Members(doc)
	if err != nil || len(ms) != 2 || ms[0].Key != "a" || string(ms[1].Raw) != `{"x":2,"y":1}` {
		t.Fatalf("members %+v %v", ms, err)
	}
	if _, err := Object([]Member{{Key: "a", Raw: []byte("1")}, {Key: "a", Raw: []byte("2")}}); !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("duplicate key err = %v", err)
	}
}

func TestRejects(t *testing.T) {
	t.Parallel()
	deep := strings.Repeat("[", MaxDepth+1) + strings.Repeat("]", MaxDepth+1)
	ok := strings.Repeat("[", MaxDepth) + strings.Repeat("]", MaxDepth)
	if _, err := Compact([]byte(ok)); err != nil {
		t.Fatalf("depth %d refused: %v", MaxDepth, err)
	}
	for name, tc := range map[string]struct {
		in   string
		want error
	}{
		"deep":      {deep, ErrTooDeep},
		"dup":       {`{"a":1,"a":2}`, ErrDuplicateKey},
		"trailing":  {`{} {}`, ErrTrailing},
		"garbage":   {`{"a":}`, ErrInvalid},
		"empty":     {``, ErrInvalid},
		"bad close": {`[1}`, ErrInvalid},
	} {
		if _, err := Compact([]byte(tc.in)); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v want %v", name, err, tc.want)
		}
	}
	if _, err := Document([]byte(`[1]`)); !errors.Is(err, ErrNotObject) {
		t.Fatal("array accepted as document")
	}
}

func TestKindOf(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]Kind{
		`null`: KindNull, `true`: KindBool, `-1.5e3`: KindNumber, `"x"`: KindString,
		`[]`: KindArray, `{}`: KindObject, `{`: KindInvalid, `01`: KindInvalid,
	} {
		if got := KindOf([]byte(in)); got != want {
			t.Errorf("KindOf(%s) = %d want %d", in, got, want)
		}
	}
}

func FuzzCompact(f *testing.F) {
	f.Add([]byte(goldenInput))
	f.Add([]byte(`[1,"a",{"b":null}]`))
	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := Compact(b)
		if err != nil {
			return
		}
		c2, err := Compact(c)
		if err != nil || !bytes.Equal(c, c2) {
			t.Fatalf("compact not a fixed point: %q -> %q (%v)", c, c2, err)
		}
	})
}
