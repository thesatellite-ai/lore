//go:build journeys && !windows

package journeys

// dryrun_checkonly_test.go — devco:D-928 (bug hunt H-186, owner urgent, a
// real 440-action import, item 2): a write's --dry-run was its own preview
// code, so `task edit --where … --priority bogus --dry-run` said "would
// change 1 tasks" while the write refused the value, and `meta set --id …
// --dry-run` stored the value. A dry run is now a check-only write: it
// fails with the write's own code, and a dry run that passes writes
// nothing.

import (
	"regexp"
	"testing"
)

// dcMemID matches the memory id an add prints.
var dcMemID = regexp.MustCompile(`\b(mem_[0-9a-f]{32})\b`)

func TestJourneyDryRunChecksLikeTheWrite(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ivo := w.Persona("Ivo Lind", "ivo@acme-co.example")
	lap := ivo.Laptop()
	code := lap.Clone(w.Remote("github.com/acme-co/feeds"), "feeds")
	var mem string

	w.Step("day1/a-task-and-a-memory", func() {
		lap.LoreIn(code, "init", "--yes").OK(t)
		lap.LoreIn(code, "task", "add", "--title", "Ship the feed", "--yes").OK(t)
		m := dcMemID.FindStringSubmatch(lap.LoreIn(code, "memory", "add", "--body", "Feeds refresh every ten minutes.", "--yes").OK(t).Out())
		if m == nil {
			t.Fatal("no memory id")
		}
		mem = m[1]
	})

	w.Step("day1/a-dry-run-refuses-what-the-write-refuses", func() {
		lap.LoreIn(code, "task", "edit", "--where", "status:todo", "--priority", "bogus", "--dry-run").
			Refused(t, "E_INVALID_INPUT", "would fail (nothing written)", "bad --priority bogus")
		lap.LoreIn(code, "task", "edit", "--where", "status:todo", "--priority", "bogus", "--yes").
			Refused(t, "E_INVALID_INPUT", "bad --priority bogus")
		lap.LoreIn(code, "meta", "set", "--id", mem, "--pair", "state=x", "--dry-run").Refused(t, "E_META_RESERVED")
	})

	w.Step("day1/a-dry-run-that-passes-writes-nothing", func() {
		lap.LoreIn(code, "task", "edit", "--where", "status:todo", "--priority", "high", "--dry-run").OK(t).Says(t, "would change 1 tasks")
		lap.LoreIn(code, "task", "list").OK(t).Says(t, "[medium]").Never(t, "[high]")
		lap.LoreIn(code, "meta", "set", "--id", mem, "--pair", "release_note=x", "--dry-run").OK(t)
		lap.LoreIn(code, "meta", "get", "--id", mem).OK(t).Never(t, "release_note")
		lap.LoreIn(code, "memory", "archive", "--where", "body~=Feeds", "--dry-run").OK(t).Says(t, "would archive")
		lap.LoreIn(code, "memory", "list").OK(t).Says(t, "Feeds refresh")
	})
}
