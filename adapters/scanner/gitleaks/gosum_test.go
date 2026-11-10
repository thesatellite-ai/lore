package gitleaks

import (
	"strings"
	"testing"
)

// A go.sum module hash is not a secret (issue #12 split: lore's own commit
// check flagged go.sum's "modernc.org/token ... h1:" line as
// generic-api-key). The hash is built at run time from two halves so this
// file never holds the flagged line itself.
func gosumHashLine() string {
	return "modernc.org/token v1.1.0 h1:" + "Xl7Ap9dKaEs5kLoOQeQm" + "PWevfnk/DM5qcLcYlA8ys6Y="
}

// TestModuleSumHashIsNotASecret: the hash line of a go.sum or go.work.sum
// file is no finding; the same line in any other file still is (the
// filter is scoped to checksum files, never a weaker rule), and a real
// key in a go.sum-shaped file is still caught.
func TestModuleSumHashIsNotASecret(t *testing.T) {
	t.Parallel()
	s := full(t)
	hash := gosumHashLine()
	pat := "ghp_" + strings.Repeat("A1b2", 9)
	other := "golang.org/x/sync v0.10.0/go.mod h1:" + strings.Repeat("Q", 43) + "=\n"
	cases := []struct {
		name, field, text string
		hit               bool
	}{
		{"go.sum hash line", "go.sum", other + hash + "\n", false},
		{"go.work.sum hash line", "go.work.sum", hash + "\n", false},
		{"go.sum in a sub folder, windows path", `tools\\go.sum`, hash + "\r\n", false},
		{"same line in another file", "notes.md", hash + "\n", true},
		{"same line with no path", "", hash + "\n", true},
		{"real token on its own go.sum line", "go.sum", other + "token = " + pat + "\n" + hash + "\n", true},
		{"real token in the hash slot", "go.sum", "example.com/token v1.0.0 h1:" + pat + "\n", true},
		{"hash line with trailing text", "go.sum", hash + " token = " + pat + "\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := s.Scan(c.field, c.text)
			if (len(got) > 0) != c.hit {
				t.Fatalf("Scan(%q) = %d findings %+v, want hit=%v", c.field, len(got), got, c.hit)
			}
		})
	}
}
