package host

import (
	"bytes"
	"testing"
)

// TestInsertAfterFirstLine: the "filed:" line goes under the success line.
func TestInsertAfterFirstLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "L\n"},
		{"ok", "ok\nL\n"},
		{"ok\n", "ok\nL\n"},
		{"ok\nscope: x\n", "ok\nL\nscope: x\n"},
	}
	for _, c := range cases {
		if got := string(insertAfterFirstLine([]byte(c.in), "L\n")); got != c.want {
			t.Errorf("insertAfterFirstLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestWriteIndented: one JSON value is indented like v0.1.17's printJSON;
// anything else passes through.
func TestWriteIndented(t *testing.T) {
	var b bytes.Buffer
	if err := WriteIndented(&b, []byte(`{"a":1}`)); err != nil || b.String() != "{\n  \"a\": 1\n}\n" {
		t.Fatalf("indent: %q %v", b.String(), err)
	}
	b.Reset()
	if err := WriteIndented(&b, []byte("not json\n")); err != nil || b.String() != "not json\n" {
		t.Fatalf("passthrough: %q %v", b.String(), err)
	}
}
