//go:build setupe2e

package setupe2e

import "testing"

// TestParseShard pins SETUPE2E_SHARD: every "<i>/<n>" part of the shapes is
// distinct and together they are every shape once; a malformed value is an
// error, never a smaller run (the gate runs the matrix as three parts).
func TestParseShard(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"0/3", "4/3", "1/0", "x", "1/3x", "2 /3", "-1/3", "1/"} {
		if _, _, err := parseShard(bad); err == nil {
			t.Errorf("parseShard(%q): want an error", bad)
		}
	}
	if p, n, err := parseShard(""); err != nil || p != 1 || n != 1 {
		t.Errorf(`parseShard(""): got %d/%d %v, want 1/1`, p, n, err)
	}
	shapes := len(AllShapes())
	seen := make([]int, shapes)
	for _, v := range []string{"1/3", "2/3", "3/3"} {
		part, parts, err := parseShard(v)
		if err != nil {
			t.Fatalf("parseShard(%q): %v", v, err)
		}
		for i := range shapes {
			if inShard(i, part, parts) {
				seen[i]++
			}
		}
	}
	for i, c := range seen {
		if c != 1 {
			t.Fatalf("shape %d runs in %d parts of 3, want exactly 1", i, c)
		}
	}
}
