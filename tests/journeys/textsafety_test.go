//go:build journeys && !windows

package journeys

// textsafety_test.go — Noor's team writes in many scripts and pastes from
// terminals. A teammate's row holding ESC sequences (a coloured log line,
// a conceal code) prints escaped in show, list and search and is written
// escaped into the rendered LORE.md, while --json and the row file keep
// the exact bytes (I-16: the raw bytes recoloured and could clear the
// reader's terminal). A title with joined emoji and a Persian word keeps
// its ZWJ/ZWNJ through add, show and --json (I-4: they were stripped). A
// Chinese word inside a sentence and an emoji are found by search (I-5:
// only whole space-separated runs were).

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// tsID matches the first row id a command prints.
var tsID = regexp.MustCompile(`(mem|dec|rul)_[0-9a-f]{32}`)

func TestJourneyTextSafety(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	noor := w.Persona("Noor Vale", "noor@textsafety.example")
	lap := noor.Laptop()
	var repo *teamharness.Clone
	const esc = "\x1b"

	w.Step("day1/escape-sequences-print-escaped", func() {
		repo = lap.Clone(w.Remote("github.com/noor-dev/atlas"), "atlas")
		lap.LoreIn(repo, "init").OK(t)
		r := lap.LoreIn(repo, "memory", "add", "--body", "probe "+esc+"[31mRED"+esc+"[0m end "+esc+"[2J").OK(t)
		id := tsID.FindString(r.Out())
		if id == "" {
			t.Fatalf("memory add printed no id: %s", r)
		}
		for _, args := range [][]string{{"show", id}, {"memory", "list"}, {"search", "probe"}} {
			lap.LoreIn(repo, args...).OK(t).Says(t, `probe \x1b[31mRED\x1b[0m end \x1b[2J`).Never(t, esc)
		}
		lap.LoreIn(repo, "show", id, "--json").OK(t).Says(t, `probe \u001b[31mRED\u001b[0m end`)

		lap.LoreIn(repo, "rule", "add", "--body", "see "+esc+"[8mhidden"+esc+"[0m text").OK(t)
		lap.LoreIn(repo, "render").OK(t)
		for _, f := range []string{"LORE.md", "LORE.full.md"} {
			b, err := os.ReadFile(filepath.Join(repo.Dir, ".lore", f))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), esc) || !strings.Contains(string(b), `see \x1b[8mhidden\x1b[0m text`) {
				t.Errorf(".lore/%s must hold the rule escaped, never a raw ESC:\n%s", f, b)
			}
		}
	})

	w.Step("day1/joiners-round-trip", func() {
		title := "Deploy 🚀 👩‍💻 👨‍👩‍👧‍👦 می‌خواهم"
		r := lap.LoreIn(repo, "decision", "add", "--title", title, "--body", "x").OK(t).Says(t, title)
		id := tsID.FindString(r.Out())
		lap.LoreIn(repo, "show", id).OK(t).Says(t, title)
		lap.LoreIn(repo, "decision", "add", "--title", title+" again", "--body", "x", "--json").OK(t).Says(t, title)
		b, err := os.ReadFile(filepath.Join(repo.Dir, ".lore", "data", "decisions", id+".json"))
		if err != nil || !strings.Contains(string(b), "‍") || !strings.Contains(string(b), "‌") {
			t.Errorf("the row file must keep ZWJ and ZWNJ (err %v):\n%s", err, b)
		}
	})

	w.Step("day1/cjk-words-and-emoji-are-found", func() {
		lap.LoreIn(repo, "decision", "add", "--title", "数据库迁移规则：不要在生产环境直接修改表 日本語のテストです", "--body", "x").OK(t)
		for _, q := range []string{"迁移", "数据库迁移", "生产环境", "日本語", "🚀", "👩‍💻"} {
			lap.LoreIn(repo, "search", q).OK(t).Never(t, "(no matches)")
		}
		lap.LoreIn(repo, "search", "🦄").OK(t).Says(t, "(no matches)")
	})
}
