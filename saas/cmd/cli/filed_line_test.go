package main

import "testing"

// TestInsertAfterFirstLine: the "filed:" line goes right under the first
// output line (v0.1.17: ✓ line, filed, then scope), for every output shape.
func TestInsertAfterFirstLine(t *testing.T) {
	const filed = "  filed: client repo default\n"
	cases := []struct{ in, want string }{
		{"✓ mem_1\n  scope: [master]\n", "✓ mem_1\n" + filed + "  scope: [master]\n"},
		{"✓ mem_1\n", "✓ mem_1\n" + filed},
		{"✓ mem_1", "✓ mem_1\n" + filed},
		{"", filed},
		{"\n", "\n" + filed},
		{"✓ ünï\n  a\n  b\n", "✓ ünï\n" + filed + "  a\n  b\n"},
	}
	for _, tc := range cases {
		if got := string(insertAfterFirstLine([]byte(tc.in), filed)); got != tc.want {
			t.Errorf("insertAfterFirstLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
