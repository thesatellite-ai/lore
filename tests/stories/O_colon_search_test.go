//go:build stories && !windows

package stories

// O_colon_search_test.go — story O, devco D-964 (bug hunt H-223, a
// benchmark team's set 4 item 2): their task and script names carry colons
// (`task lint:no-os-getenv`, `task gqlkit:all`), and `lore search --query
// "lint:runq-determinism exempt"` found no rows while the same words
// without the colon did: the colon made the query FTS5 syntax and the word
// one phrase every row had to hold word for word. Here an owner-shaped
// laptop keeps such rows in an org repo and searches the way that team
// types: every colon query finds what the colon-free query finds, in
// `lore search` and in `lore <kind> search`, and punctuated names
// (a.b/c-d (x)) still find their row.

import (
	"strings"
	"testing"
)

// TestStoryOColonSearch: a colon inside a query word separates words, so
// a task name typed with its colon finds the rows (devco D-964).
func TestStoryOColonSearch(t *testing.T) {
	s := New(t, "O")
	web := s.Seed(s.Remote("github.com/"+oOwner+"/web"), func(dir string) { PlainRepo(t, dir) })
	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	wc := ada.CloneTo(web, ada.OrgDir("web"))

	const (
		exempt = "The replay worker is exempt from the runq-determinism lint"
		getenv = "Drop the os.Getenv calls flagged by the no-os-getenv lint"
		gqlkit = "run the gqlkit generator for all modules"
		parser = "The module a.b/c-d (x) holds the parser"
	)
	s.Step("D-964/rows-named-like-task-names", func() {
		ada.LoreIn(wc, "init").OK(t)
		ada.LoreIn(wc, "memory", "add", "--body", exempt+"; its output order is fixed by hand.").OK(t)
		ada.LoreIn(wc, "task", "add", "--title", getenv).OK(t)
		ada.LoreIn(wc, "memory", "add", "--body", "After editing a .graphqls file, "+gqlkit+".").OK(t)
		ada.LoreIn(wc, "memory", "add", "--body", parser+".").OK(t)
	})

	s.Step("D-964/a-colon-query-finds-what-the-words-find", func() {
		for _, c := range []struct {
			cell, query, want string
			args              []string
		}{
			{"D-964/search-colon-and-word", "lint:runq-determinism exempt", exempt, nil},
			{"D-964/search-task-name", "lint:no-os-getenv", getenv, nil},
			{"D-964/search-task-name-and-word", "gqlkit:all modules", gqlkit, nil},
			{"D-964/search-punctuated-name", "a.b/c-d (x)", parser, nil},
			{"D-964/task-search-task-name", "lint:no-os-getenv", getenv, []string{"task"}},
			{"D-964/memory-search-colon", "lint:runq-determinism exempt", exempt, []string{"memory"}},
		} {
			args := append(append([]string{}, c.args...), "search", "--query", c.query)
			r := ada.LoreIn(wc, args...)
			out := r.Out()
			ok := r.Code == 0 && strings.Contains(out, c.want) && !strings.Contains(out, "(no matches)") && !strings.Contains(out, "syntax error")
			oCheck(t, c.cell, ok, "`lore %s` in the org repo: want the row %q:\n%s", strings.Join(args, " "), c.want, r)
		}
	})
}
