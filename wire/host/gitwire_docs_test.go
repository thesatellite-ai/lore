package host

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// oldLoreFixRe finds the command the pre-commit and commit-msg blocks name
// when the lore they run is older than the rows in .lore/data
// ("... is older; run <command> here with your lore 0.1.18 ...").
var oldLoreFixRe = regexp.MustCompile(`is older; run (doctor [^"]+?) here with`)

// docFixRe finds a backticked doctor command in a sentence of the docs.
var docFixRe = regexp.MustCompile("`(doctor --fix[^`]*)`")

// TestOldLoreRefusalDocsNameThePrintedFix (devco D-810): the README and the
// hooks page tell a reader which command gets them past the hook that
// refuses a commit when the lore it runs is older than 0.1.18. D-795 made
// the hook print `doctor --fix --pin-hooks`, but both pages kept the bare
// `doctor --fix`, so the docs gate (docsync) failed and a reader following
// the page ran a command that leaves the hooks on the old lore. Every
// sentence about that refusal must name the command the hook prints.
func TestOldLoreRefusalDocsNameThePrintedFix(t *testing.T) {
	t.Parallel()
	var printed string
	for hook, b := range hookBlocks("") {
		for _, l := range b.lines {
			m := oldLoreFixRe.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			if printed != "" && printed != m[1] {
				t.Fatalf("hook %s names %q, another hook names %q: the blocks must name one fix", hook, m[1], printed)
			}
			printed = m[1]
		}
	}
	if printed == "" {
		t.Fatal("no hook block names the command that gets past the old-lore refusal; update oldLoreFixRe")
	}
	root := filepath.Join("..", "..")
	pages := []string{"README.md", filepath.Join("docs", "sync", "hooks-and-merge-driver.md")}
	for _, page := range pages {
		raw, err := os.ReadFile(filepath.Join(root, page))
		if err != nil {
			t.Fatalf("read %s: %v", page, err)
		}
		seen := 0
		for i, line := range strings.Split(string(raw), "\n") {
			if !strings.Contains(line, "0.1.18") || !strings.Contains(line, "older") {
				continue
			}
			for _, m := range docFixRe.FindAllStringSubmatch(line, -1) {
				seen++
				if m[1] != printed {
					t.Errorf("%s:%d names `%s` for the old-lore refusal; the hook prints `%s`", page, i+1, m[1], printed)
				}
			}
		}
		if seen == 0 {
			t.Errorf("%s: no sentence about the old-lore refusal names `%s`", page, printed)
		}
	}
}
