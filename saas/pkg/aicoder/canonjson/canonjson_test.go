package canonjson

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestEncode_SortedIndentedTrailingNewline(t *testing.T) {
	t.Parallel()
	got, err := Encode(map[string]any{"b": "x", "a": int64(1), "c": nil})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"a\": 1,\n  \"b\": \"x\",\n  \"c\": null\n}\n"
	if string(got) != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestEncode_NoHTMLEscape(t *testing.T) {
	t.Parallel()
	got, err := Encode(map[string]any{"body": "a < b && c > d"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "a < b && c > d") {
		t.Fatalf("html escaped: %s", got)
	}
}

func TestEncode_RawMessageCanonicalized(t *testing.T) {
	t.Parallel()
	got, err := Encode(map[string]any{"cfg": json.RawMessage(`{"z":1,"a":{"y":2,"b":3}}`)})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"cfg\": {\n    \"a\": {\n      \"b\": 3,\n      \"y\": 2\n    },\n    \"z\": 1\n  }\n}\n"
	if string(got) != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestEncode_Deterministic(t *testing.T) {
	t.Parallel()
	obj := map[string]any{}
	for _, k := range []string{"k1", "k9", "k3", "k5", "k2", "k8"} {
		obj[k] = k + "-v"
	}
	first, err := Encode(obj)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 50 {
		again, err := Encode(obj)
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatalf("run %d differs", i)
		}
	}
}

func TestEncode_UnsupportedType(t *testing.T) {
	t.Parallel()
	if _, err := Encode(map[string]any{"x": struct{}{}}); err == nil {
		t.Fatal("struct value must be rejected, not silently stringified")
	}
}

func TestRoundTrip_NumbersPreserved(t *testing.T) {
	t.Parallel()
	in := []byte(`{"big":9007199254740993,"f":0.1,"neg":-5,"s":"x"}`)
	obj, err := Decode(in, DefaultMaxDepth)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Encode(obj)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"9007199254740993", "0.1", "-5"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("lost number %s in %s", want, out)
		}
	}
	again, err := Canonicalize(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(out) {
		t.Fatalf("canonical form not a fixed point:\n%s\n%s", out, again)
	}
}

func TestDecode_Rejects(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, in string
		is       error
	}{
		{"array top level", `[1,2]`, ErrNotObject},
		{"scalar top level", `"x"`, ErrNotObject},
		{"trailing data", `{"a":1} {"b":2}`, nil},
		{"conflict markers", "{\n<<<<<<< ours\n  \"a\": 1\n=======\n  \"a\": 2\n>>>>>>> theirs\n}", nil},
		{"truncated", `{"a":`, nil},
		{"too deep", strings.Repeat(`{"a":`, 40) + "1" + strings.Repeat("}", 40), ErrTooDeep},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Decode([]byte(tc.in), DefaultMaxDepth)
			if err == nil {
				t.Fatal("expected error")
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Fatalf("err = %v, want %v", err, tc.is)
			}
		})
	}
}

func TestCompactAndParseValue(t *testing.T) {
	t.Parallel()
	v, err := ParseValue(`{"b":[1,"x"],"a":true}`)
	if err != nil {
		t.Fatal(err)
	}
	s, err := CompactValue(v)
	if err != nil {
		t.Fatal(err)
	}
	if s != `{"a":true,"b":[1,"x"]}` {
		t.Fatalf("compact = %s", s)
	}
	if _, err := ParseValue(`1 2`); err == nil {
		t.Fatal("trailing data must be rejected")
	}
}
