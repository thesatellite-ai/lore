//go:build journeys && !windows

package journeys

// emptysearch_test.go — devco:D-821 (bug hunt H-77, owner HIGH): an agent
// asks lore from a code repo and the answer is in a folder of the person's
// own store. `lore search`, `lore memory search` and `lore ask` answered
// "(no matches)" (ask: one fixed sentence) and nothing more, so the agent
// gave up. Now every empty answer says what it searched, how it read the
// question and what to try, and each printed command runs where it was
// printed and finds the note. lore context with nothing applying says the
// same (devco:D-824, H-83).

import (
	"strings"
	"testing"
)

func TestJourneyEmptySearchSaysWhatToTry(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	nia := w.Persona("Nia Okoro", "nia@acme-co.example")
	lap := nia.Laptop()
	me := w.Remote("github.com/nia-okoro/lore-me")
	code := lap.Clone(w.Remote("github.com/acme-co/orbit"), "orbit")

	w.Step("day1/a-note-in-a-folder-of-the-personal-store", func() {
		lap.Lore(lap.Home, "store", "init", "--name", "me", "--url", me.SSHURL(), "--yes").OK(t)
		lap.Lore(lap.Home, "store", "folder", "add", "me", "work").OK(t)
		lap.Lore(lap.Home, "memory", "add", "--body", "The kubernetes migration of orbit waits for the new load balancer.", "--to", "me/work", "--yes").OK(t)
		lap.LoreIn(code, "init").OK(t)
	})

	w.Step("day1/empty-answers-say-what-was-searched-and-what-to-try", func() {
		for _, c := range []struct {
			args []string
			says []string
		}{
			{[]string{"search", "--query", "kubernetes migration"}, []string{"(no matches)", "  searched: ", "  read as: words: kubernetes, migration", `  try: lore search --query "kubernetes migration" --store me`}},
			{[]string{"memory", "search", "--query", "kubernetes"}, []string{"(no matches)", "(memory)", `  try: lore memory search --query "kubernetes" --store me`}},
			{[]string{"ask", "--query", "what is blocking the kubernetes migration"}, []string{"(no answer)", "  searched: ", `  try: lore ask --query "what is blocking the kubernetes migration" --store me`}},
			// devco:D-824 (H-83): lore context with nothing applying, the
			// same lines; it never reads the personal store, so the first
			// try is the search that does
			{[]string{"context", "--for-prompt", "kubernetes migration"}, []string{"(no rows apply here)", "  searched: repo (rule, hotfix, ", "  read as: words: kubernetes, migration", `  try: lore search --query "kubernetes migration" --store me`}},
			{[]string{"context", "--for-file", "ops/kubernetes/migration.yaml"}, []string{"(no rows apply here)", "  read as: path: ops/kubernetes/migration.yaml (globs", "words: kubernetes, migration", `  try: lore search --query "kubernetes migration" --store me`, "  try: lore rule list"}},
		} {
			out := lap.LoreIn(code, c.args...).OK(t).Says(t, c.says...).Out()
			tries := 0
			for _, line := range strings.Split(out, "\n") {
				cmd, ok := strings.CutPrefix(line, "  try: lore ")
				if !ok {
					continue
				}
				tries++
				// every printed command runs where it was printed
				lap.LoreIn(code, tryWords(cmd)...).OK(t)
			}
			if tries == 0 || tries > 3 {
				t.Errorf("lore %s: want 1-3 try lines, got %d:\n%s", strings.Join(c.args, " "), tries, out)
			}
			// the first try (the personal store) finds the note
			first := strings.SplitN(strings.SplitN(out, "  try: lore ", 2)[1], "\n", 2)[0]
			lap.LoreIn(code, tryWords(first)...).OK(t).Says(t, "load balancer")
		}
		lap.LoreIn(code, "search", "--query", "kubernetes migration", "--json").OK(t).
			Says(t, `"searched":`, `"try":`, `"kubernetes"`)
	})
}

// tryWords splits a printed command (after "lore ") as a shell would for
// plain, double-quoted and single-quoted words.
func tryWords(s string) []string {
	var words []string
	var cur strings.Builder
	var quote rune
	inWord := false
	for _, r := range s {
		switch {
		case quote == 0 && (r == '"' || r == '\''):
			quote, inWord = r, true
		case r == quote:
			quote = 0
		case r == ' ' && quote == 0:
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
			}
			inWord = false
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}
