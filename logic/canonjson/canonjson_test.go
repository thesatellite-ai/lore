package canonjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestEncode_SortedIndentedTrailingNewline(t *testing.T) {
	t.Parallel()
	got, err := Encode(Object{"b": String("x"), "a": Int(1), "c": Null()})
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
	got, err := Encode(Object{"body": String("a < b && c > d")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "a < b && c > d") {
		t.Fatalf("html escaped: %s", got)
	}
}

func TestEncode_RawCanonicalized(t *testing.T) {
	t.Parallel()
	cfg, err := Raw([]byte(`{"z":1,"a":{"y":2,"b":3}}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Encode(Object{"cfg": cfg})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"cfg\": {\n    \"a\": {\n      \"b\": 3,\n      \"y\": 2\n    },\n    \"z\": 1\n  }\n}\n"
	if string(got) != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestEncode_BadNumberRejected(t *testing.T) {
	t.Parallel()
	if _, err := Encode(Object{"n": Number("1x")}); err == nil {
		t.Fatal("a bad number literal must be refused, not written")
	}
	if _, ok := Float(0); !ok {
		t.Fatal("0 is a number")
	}
	inf := 1e308
	if _, ok := Float(inf * 10); ok {
		t.Fatal("+Inf has no JSON form")
	}
}

// legacyEncode is the v0.1.17 algorithm (encoding/json over map[string]any
// with numbers as json.Number): the byte-identity reference.
func legacyEncode(t *testing.T, in []byte) []byte {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(in))
	dec.UseNumber()
	var v map[string]any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", indentUnit)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestCanonicalize_ByteIdenticalToLegacy: every committed v0.1.17 row file
// must keep its bytes, so the new writer is checked against the old one on
// shapes that differ in escaping, emptiness, nesting and number text.
func TestCanonicalize_ByteIdenticalToLegacy(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		`{}`,
		`{"a":[],"b":{},"c":[{}],"d":[[]]}`,
		`{"s":"<a href=\"x\">&amp;</a>","u":"line\u2028sep\u2029","t":"tab\tnl\nq\"bs\\","e":"é日本"}`,
		`{"n":[0,-0,1e10,1E-7,0.1,9007199254740993,-12.50]}`,
		`{"z":{"y":{"x":[1,{"b":true,"a":false},null]}},"a":"z"}`,
		`{"ctl":"\u0001\u001f","bad":"\ud800"}`,
	} {
		got, err := Canonicalize([]byte(in))
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if want := legacyEncode(t, []byte(in)); !bytes.Equal(got, want) {
			t.Fatalf("%s:\ngot  %q\nwant %q", in, got, want)
		}
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
		{"empty", ``, nil},
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

func TestEncode_TooDeepRefused(t *testing.T) {
	t.Parallel()
	v := Int(1)
	for range DefaultMaxDepth + 2 {
		v = ObjectValue(Object{"a": v})
	}
	o, _ := v.AsObject()
	if _, err := Encode(o); !errors.Is(err, ErrTooDeep) {
		t.Fatalf("err = %v, want ErrTooDeep", err)
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
	for _, sc := range []string{`null`, `true`, `false`, `"x"`, `-1.5e3`, `[]`} {
		pv, err := ParseValue(sc)
		if err != nil {
			t.Fatal(err)
		}
		if out, _ := CompactValue(pv); out != sc {
			t.Fatalf("%s round trips as %s", sc, out)
		}
	}
}

func TestEqualAndJSONMethods(t *testing.T) {
	t.Parallel()
	a, _ := ParseValue(`{"x":[1,"a",null,{"k":false}]}`)
	b, _ := ParseValue(`{"x":[1,"a",null,{"k":false}]}`)
	c, _ := ParseValue(`{"x":[1.0,"a",null,{"k":false}]}`)
	if !Equal(a, b) || Equal(a, c) || Equal(Null(), Bool(false)) || Equal(String("1"), Int(1)) {
		t.Fatal("Equal matrix wrong")
	}
	type wrap struct {
		V Value  `json:"v"`
		O Object `json:"o"`
	}
	out, err := json.Marshal(wrap{V: Int(7), O: Object{"b": Bool(true), "a": Null()}})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"v":7,"o":{"a":null,"b":true}}` {
		t.Fatalf("marshal = %s", out)
	}
	var back wrap
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if n, _ := back.V.AsNumber(); n != "7" || !back.O.Has("a") || back.O.Str("a") != "" {
		t.Fatalf("unmarshal = %+v", back)
	}
	if back.O.Clone() == nil || Object(nil).Clone() != nil {
		t.Fatal("Clone nil handling")
	}
}
