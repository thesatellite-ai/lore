package host

import (
	"bytes"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
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

// TestPurgedLines: a purge rewrites the trash line; a plain delete given
// --confirm gets DQ8's note; anything else is unchanged.
func TestPurgedLines(t *testing.T) {
	in := []byte("✓ mem_x moved to the trash\n")
	mid, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	id := mid.Any()
	got := string(purgedLines(in, &engine.PurgeRequest{Purged: []ids.AnyID{id}}, true))
	if want := "✓ purged for good: " + id.String() + " (tombstones written; old content is still in git history)\n"; got != want {
		t.Fatalf("purge: %q, want %q", got, want)
	}
	if got := purgedLines(in, &engine.PurgeRequest{}, true); !bytes.Contains(got, []byte("a delete is soft now")) {
		t.Fatalf("confirm note missing: %q", got)
	}
	if got := purgedLines(in, &engine.PurgeRequest{}, false); !bytes.Equal(got, in) {
		t.Fatalf("plain delete changed: %q", got)
	}
}

// TestWriteIndented: one JSON value is indented like v0.1.17's printJSON;
// anything else passes through.
func TestWriteIndented(t *testing.T) {
	var b bytes.Buffer
	if err := writeIndented(&b, []byte(`{"a":1}`)); err != nil || b.String() != "{\n  \"a\": 1\n}\n" {
		t.Fatalf("indent: %q %v", b.String(), err)
	}
	b.Reset()
	if err := writeIndented(&b, []byte("not json\n")); err != nil || b.String() != "not json\n" {
		t.Fatalf("passthrough: %q %v", b.String(), err)
	}
}
