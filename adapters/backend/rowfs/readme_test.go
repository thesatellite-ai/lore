package rowfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/mdwrap"
)

// TestStoreReadmeNotWrapped: the store README keeps one physical line per
// paragraph, bullet or cell (owner rule: never hard-wrap markdown).
func TestStoreReadmeNotWrapped(t *testing.T) {
	t.Parallel()
	if p := mdwrap.Check(StoreReadme); len(p) > 0 {
		t.Fatalf("StoreReadme is hard-wrapped:\n%s", mdwrap.Lines(p))
	}
	for _, want := range []string{"`_store/`", "project folder"} {
		if !strings.Contains(StoreReadme, want) {
			t.Errorf("StoreReadme does not explain %s", want)
		}
	}
	if earlierReadmes[readmeSum([]byte(StoreReadme))] {
		t.Fatal("the current README is listed as an earlier one: lore would rewrite it on every commit")
	}
}

// TestEnsureReadmeReplacesEarlierText: a README byte-identical to an
// earlier lore's text (also with CRLF line ends, as a Windows checkout
// has it) is replaced; a person's own README, or lore's text with one
// byte changed, is kept.
func TestEnsureReadmeReplacesEarlierText(t *testing.T) {
	t.Parallel()
	earlier, err := os.ReadFile(filepath.Join("testdata", "readme-earlier.txt"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		body    string
		replace bool
	}{
		{"earlier lore text", string(earlier), true},
		{"earlier lore text, CRLF", strings.ReplaceAll(string(earlier), "\n", "\r\n"), true},
		{"earlier text edited", string(earlier) + "our note\n", false},
		{"own readme", "# our store\n", false},
		{"current text", StoreReadme, false},
	}
	for _, c := range cases {
		dir := t.TempDir()
		p := filepath.Join(dir, ReadmeFile)
		if err := os.WriteFile(p, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := ReadmeOutdated(dir); got != c.replace {
			t.Errorf("%s: ReadmeOutdated = %v, want %v", c.name, got, c.replace)
		}
		wrote, err := EnsureReadme(dir)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(p)
		if c.replace && (!wrote || string(got) != StoreReadme) {
			t.Errorf("%s: not replaced (wrote %v)", c.name, wrote)
		}
		if !c.replace && (wrote || string(got) != c.body) {
			t.Errorf("%s: changed (wrote %v)", c.name, wrote)
		}
	}
	dir := t.TempDir()
	if wrote, err := EnsureReadme(dir); err != nil || !wrote {
		t.Fatalf("missing README not written: %v %v", wrote, err)
	}
}
