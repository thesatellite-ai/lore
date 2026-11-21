//go:build stories && !windows

package stories

// O_empty_answers_test.go — story O, devco D-821 / D-824 (bug hunts H-77,
// H-83): an owner-shaped laptop keeps a note in a folder of its personal
// store and asks from a code repo and from the home folder. A recall that
// finds nothing (`lore search`, `lore ask`, `lore context --for-prompt`,
// `lore context --for-file`) was silently empty or printed a try command
// with a "<keywords>" placeholder. Each now names what it searched and how
// it read the question, and every `try:` line runs exactly where it was
// printed; the first try of a read that left the personal store out finds
// the note.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// TestStoryOEmptyAnswersSayWhatToTry: every empty recall says searched:,
// read as: and 1-3 try: lines that run where printed (devco D-821, D-824).
func TestStoryOEmptyAnswersSayWhatToTry(t *testing.T) {
	s := New(t, "O")
	me := s.Remote("github.com/" + oAdminPersonal.Login + "/lore-me")
	orbit := s.Remote("github.com/" + oAdminPersonal.Login + "/orbit")
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	var code *teamharness.Clone
	s.Step("personal-store/a-note-in-a-folder", func() {
		ada.Lore(ada.Home, "store", "init", "--name", "me", "--url", me.Dir, "--yes").OK(t)
		ada.Lore(ada.Home, "store", "folder", "add", "me", psFolder).OK(t)
		ada.Lore(ada.Home, "memory", "add", "--body", "The kubernetes migration of orbit waits for the new load balancer.", "--to", "me/"+psFolder, "--yes").OK(t)
		code = ada.CloneTo(orbit, ada.Code("orbit"))
		ada.LoreIn(code, "init").OK(t)
	})
	s.Step("code-repo/empty-answers-say-what-and-each-try-runs", func() {
		for _, c := range []struct {
			args []string
			says []string
		}{
			{[]string{"search", "--query", "kubernetes migration"}, []string{"(no matches)", "  searched: ", "  read as: words: kubernetes, migration"}},
			{[]string{"ask", "--query", "what is blocking the kubernetes migration"}, []string{"(no answer)", "  searched: ", "  read as: words: "}},
			{[]string{"context", "--for-prompt", "kubernetes migration"}, []string{"(no rows apply here)", "  searched: repo (rule, hotfix, ", "  read as: words: kubernetes, migration"}},
			{[]string{"context", "--for-file", "ops/kubernetes/migration.yaml"}, []string{"(no rows apply here)", "  read as: path: ops/kubernetes/migration.yaml (globs", "  try: lore rule list"}},
		} {
			out := ada.LoreIn(code, c.args...).OK(t).Says(t, c.says...).Out()
			first := ""
			for _, cmd := range oTries(t, "lore "+strings.Join(c.args, " "), out) {
				if first == "" {
					first = cmd
				}
				ada.LoreIn(code, oShellWords(cmd)...).OK(t) // runs where printed
			}
			// the personal store was left out: the first try reads it
			if !strings.Contains(first, "--store me") {
				t.Errorf("[O D-824] lore %s: the first try does not read the personal store: %q\n%s", strings.Join(c.args, " "), first, out)
			}
			ada.LoreIn(code, oShellWords(first)...).OK(t).Says(t, "load balancer")
		}
	})
	s.Step("home/empty-ask-in-the-personal-store-each-try-runs", func() {
		out := ada.Lore(ada.Home, "ask", "--store", "me", "--query", "who is zed-quill").OK(t).
			Says(t, "(no answer)", "  searched: me, me/"+psFolder, "question: who is").Out()
		for _, cmd := range oTries(t, "lore ask --store me", out) {
			if !strings.Contains(cmd, "--store me") {
				t.Errorf("[O D-821] a try from an ask --store me drops the store: %q", cmd)
			}
			ada.Lore(ada.Home, oShellWords(cmd)...).OK(t)
		}
	})
}

// oTries is the commands (after "lore ") of out's try: lines; 1-3 of them.
func oTries(t *testing.T, what, out string) []string {
	t.Helper()
	var cmds []string
	for _, line := range strings.Split(out, "\n") {
		if cmd, ok := strings.CutPrefix(line, "  try: lore "); ok {
			cmds = append(cmds, cmd)
		}
		if strings.Contains(line, "<") && strings.HasPrefix(line, "  try: ") {
			t.Errorf("[O D-824] %s prints a placeholder: %q", what, line)
		}
	}
	if len(cmds) == 0 || len(cmds) > 3 {
		t.Errorf("[O D-821] %s: want 1-3 try lines, got %d:\n%s", what, len(cmds), out)
	}
	return cmds
}

// oShellWords splits a printed command as a shell would for plain,
// double-quoted and single-quoted words.
func oShellWords(s string) []string {
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
