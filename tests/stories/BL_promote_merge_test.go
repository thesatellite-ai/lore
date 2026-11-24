//go:build stories && !windows

package stories

// BL_promote_merge_test.go — lore branch promote merges a branch layer's
// copy of a row with the shared notes' newer edits, as a landing does
// (#155, devco:D-947).

import (
	"strings"
	"testing"
)

// TestStoryBLPromoteKeepsMainEdits (#155, devco:D-947): rows edited on an
// isolated branch, then archived, given metadata, given a newer body and
// closed on main after the branch copied them. Promoting them refuses
// with E_LAND_CONFLICT naming the one text both sides changed (the
// memory's body) and changes nothing on main; with --take main every main
// edit stays (archive, metadata, newer body, done status) while the
// fields only the branch changed come in. The old promote wrote the
// branch's whole rows over main and said only "put 3 row(s)".
func TestStoryBLPromoteKeepsMainEdits(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	var m1, m2, task string
	s.Step("day1/rows-on-main", func() {
		m1 = idIn(t, blMemID, w.admin.LoreIn(w.aapp, "memory", "add", "--body", "pelican invoices export nightly").OK(t))
		m2 = idIn(t, blMemID, w.admin.LoreIn(w.aapp, "memory", "add", "--body", "heron receipts export hourly").OK(t))
		task = idIn(t, blTaskID, w.admin.LoreIn(w.aapp, "task", "add", "--title", "export walrus ledger").OK(t))
		w.admin.LoreIn(w.aapp, "sync").OK(t)
	})
	s.Step("day1/edited-on-the-branch", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/promote")
		w.admin.LoreIn(w.aapp, "memory", "edit", "--id", m1, "--body", "pelican invoices export nightly (branch wording)").OK(t)
		w.admin.LoreIn(w.aapp, "memory", "edit", "--id", m2, "--body", "heron receipts export hourly (branch wording)").OK(t)
		w.admin.LoreIn(w.aapp, "task", "edit", "--id", task, "--body", "branch note on the task").OK(t)
	})
	s.Step("day1/changed-on-main-after-the-copy", func() {
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "memory", "archive", "--id", m1, "--yes").OK(t)
		w.admin.LoreIn(w.aapp, "meta", "set", "--id", m2, "--pair", "team_owner=billing").OK(t)
		w.admin.LoreIn(w.aapp, "memory", "edit", "--id", m2, "--body", "heron receipts export every 30 minutes").OK(t)
		w.admin.LoreIn(w.aapp, "task", "done", "--id", task, "--no-lesson").OK(t)
		w.admin.LoreIn(w.aapp, "sync").OK(t)
	})
	mainKeptItsEdits := func() {
		t.Helper()
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "show", "--id", m1, "--json").OK(t).Says(t, `"archived_at"`)
		w.admin.LoreIn(w.aapp, "memory", "show", "--id", m2).OK(t).Says(t, "every 30 minutes").Never(t, "hourly (branch wording)")
		w.admin.LoreIn(w.aapp, "meta", "get", "--id", m2).OK(t).Says(t, "team_owner", "billing")
		w.admin.LoreIn(w.aapp, "task", "show", "--id", task).OK(t).Says(t, "done")
	}
	s.Step("day1/the-same-text-refuses-the-promote", func() {
		w.aapp.Git("switch", "-q", "feat/promote")
		ids := strings.Join([]string{m1, m2, task}, ",")
		w.admin.LoreIn(w.aapp, "branch", "promote", "--ids", ids).
			Refused(t, "E_LAND_CONFLICT", m2+" (body)", "nothing was promoted", "lore branch promote --ids "+ids+" --take main").
			Never(t, m1+" (", task+" (")
		mainKeptItsEdits()
	})
	s.Step("day1/take-main-keeps-every-main-edit", func() {
		w.aapp.Git("switch", "-q", "feat/promote")
		w.admin.LoreIn(w.aapp, "branch", "promote", "--ids", strings.Join([]string{m1, m2, task}, ","), "--take", "main").OK(t).
			Says(t, "put 3 row(s)", "2 merged field by field", "1 settled by --take")
		mainKeptItsEdits()
		// the fields only the branch changed came in
		w.admin.LoreIn(w.aapp, "show", "--id", m1, "--json").OK(t).Says(t, "(branch wording)")
		w.admin.LoreIn(w.aapp, "task", "show", "--id", task).OK(t).Says(t, "branch note on the task")
	})
}
