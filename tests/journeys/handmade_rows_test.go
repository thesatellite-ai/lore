//go:build journeys && !windows

package journeys

// handmade_rows_test.go — rows a teammate wrote by hand and pushed with
// plain git, past lore's write path (devco R-3, R-8). A rule whose text
// holds a right-to-left override or a zero-width space must not read
// differently on the reader's terminal or in the rendered agent context
// than it is; a broken row file the reader pulled is named with the commit
// and author that brought it, and lore fix gives one verdict, never
// "Everything is current." above errors and Status: BROKEN.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// bidiZW are the characters that must never reach a reader raw.
const bidiZW = "\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069\u200b\ufeff"

func TestJourneyHandMadeRows(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ada := w.Persona("Ada Reader", "ada@rows.example")
	kim := w.Persona("Kim Writer", "kim@rows.example")
	remote := w.Remote("github.com/rows-co/app")
	alap, klap := ada.Laptop(), kim.Laptop()
	a := alap.Clone(remote, "app")
	var ruleID, sha string

	w.Step("day1/ada-adopts-lore", func() {
		a.WriteFile("main.go", "package main\n")
		alap.LoreIn(a, "init").OK(t)
		out := alap.LoreIn(a, "rule", "add", "--body", "HANDMADE placeholder", "--severity", "must").OK(t).Out()
		ruleID = regexp.MustCompile(`rul_[0-9a-f]+`).FindString(out)
		if ruleID == "" {
			t.Fatalf("rule add printed no id: %s", out)
		}
		a.CommitAll("lore")
		if r := a.TryGit("push", "-q", "origin", "HEAD:main"); r.Code != 0 {
			a.Git("push", "-q", "origin", "HEAD:main")
		}
	})

	w.Step("day2/kim-edits-rows-by-hand", func() {
		k := klap.Clone(remote, "app")
		file := filepath.Join(k.Dir, ".lore", "data", "rules", ruleID+".json")
		raw, err := os.ReadFile(filepath.Clean(file))
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal("HANDMADE allow \u202eetirw\u202c x\u200by \u2066iso\u2069")
		doc["body"] = body
		out, _ := json.Marshal(doc)
		k.WriteFile(filepath.Join(".lore", "data", "rules", ruleID+".json"), string(out))
		broken := ".lore/data/memories/mem_01a1dc0c00007000800000000000000b.json"
		k.WriteFile(broken, "{not json")
		for _, f := range []string{file, filepath.Join(k.Dir, broken)} {
			if err := os.Chmod(f, 0o644); err != nil { // committed files are readable (modes invariant)
				t.Fatal(err)
			}
		}
		k.Git("add", "-A")
		k.Git("commit", "-q", "--no-verify", "-m", "notes")
		sha = k.Git("log", "-1", "--format=%h")
		k.Git("push", "-q", "origin", "HEAD:main")
	})

	w.Step("day2/ada-reads-what-came-in", func() {
		a.Git("pull", "-q", "--no-rebase", "origin", "main")
		for _, args := range [][]string{{"show", ruleID}, {"rule", "list"}, {"search", "HANDMADE"}} {
			r := alap.LoreIn(a, args...)
			if strings.ContainsAny(r.Out(), bidiZW) {
				t.Fatalf("lore %v printed a bidi or zero-width character raw:\n%+q", args, r.Out())
			}
		}
		alap.LoreIn(a, "show", ruleID).Says(t, `\u202eetirw\u202c`)
		alap.LoreIn(a, "render").OK(t)
		lore, ok := a.ReadFile(".lore/LORE.md")
		if ok && strings.ContainsAny(lore, bidiZW) {
			t.Fatalf("LORE.md holds a bidi or zero-width character raw")
		}
	})

	w.Step("day2/ada-runs-lore-fix", func() {
		r := alap.LoreIn(a, "fix")
		r.Says(t, "came in commit "+sha+" by Kim Writer <kim@rows.example>", "revert "+sha, "Status: BROKEN").Never(t, "Everything is current.", "checkout --")
	})
}
