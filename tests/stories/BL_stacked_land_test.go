//go:build stories && !windows

package stories

// BL_stacked_land_test.go — a stacked branch keeps main's newer edits when
// its parent lands first (#155 stacked, devco:D-951).

import (
	"strings"
	"testing"
)

// TestStoryBLStackedLandKeepsMainEdits (#155 stacked, devco:D-951):
// feat/sy is made from feat/sx and edits three rows; main then archives
// one memory, gives the other metadata and a newer body and closes the
// task; feat/sx merges and lands, which moves feat/sy's notes onto the
// shared notes. feat/sy's land then stops with E_LAND_CONFLICT on the one
// text both changed (the memory's body), its promote too, and the printed
// --take main land keeps every main edit while the child's own wording of
// the other rows comes in. The old move wrote the child's raw copies onto
// the landing, so its land (even with --take main naming the memory)
// undid the archive, the metadata, the newer body and the done status.
func TestStoryBLStackedLandKeepsMainEdits(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	var m1, m2, task string
	s.Step("day1/rows-on-main", func() {
		m1 = idIn(t, blMemID, w.admin.LoreIn(w.aapp, "memory", "add", "--body", "pelican invoices export nightly").OK(t))
		m2 = idIn(t, blMemID, w.admin.LoreIn(w.aapp, "memory", "add", "--body", "heron receipts export hourly").OK(t))
		task = idIn(t, blTaskID, w.admin.LoreIn(w.aapp, "task", "add", "--title", "export walrus ledger").OK(t))
		w.admin.LoreIn(w.aapp, "sync").OK(t)
	})
	s.Step("day1/stacked-child-edits-the-rows", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/sx")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "the parent branch note").OK(t)
		w.aapp.WriteFile("sx.txt", "sx\n")
		w.aapp.CommitAll("feat: sx")
		w.aapp.Git("push", "-q", "origin", "feat/sx")
		w.aapp.Git("switch", "-q", "-c", "feat/sy")
		w.admin.LoreIn(w.aapp, "memory", "edit", "--id", m1, "--body", "pelican invoices export nightly (child wording)").OK(t)
		w.admin.LoreIn(w.aapp, "memory", "edit", "--id", m2, "--body", "heron receipts export hourly (child wording)").OK(t)
		w.admin.LoreIn(w.aapp, "task", "edit", "--id", task, "--body", "child note on the task").OK(t)
		w.aapp.WriteFile("sy.txt", "sy\n")
		w.aapp.CommitAll("feat: sy")
		w.aapp.Git("push", "-q", "origin", "feat/sy")
		w.admin.LoreIn(w.aapp, "branch", "list").OK(t).Says(t, "feat/sx", "feat/sy")
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
		w.admin.LoreIn(w.aapp, "memory", "show", "--id", m2).OK(t).Says(t, "every 30 minutes").Never(t, "hourly (child wording)")
		w.admin.LoreIn(w.aapp, "meta", "get", "--id", m2).OK(t).Says(t, "team_owner", "billing")
		w.admin.LoreIn(w.aapp, "task", "show", "--id", task).OK(t).Says(t, "done")
	}
	s.Step("day2/the-parent-merges-and-lands", func() {
		s.AdvanceDays(1)
		w.aapp.Git("merge", "-q", "--no-ff", "-m", "Merge feat/sx", "feat/sx")
		w.aapp.Git("push", "-q", "origin", "main")
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/sx").OK(t).Says(t, "landed", "feat/sy (stacked on it) now sit on the shared notes")
		mainKeptItsEdits()
	})
	s.Step("day2/the-child-promote-refuses-the-same-text", func() {
		w.aapp.Git("switch", "-q", "feat/sy")
		ids := strings.Join([]string{m1, m2, task}, ",")
		w.admin.LoreIn(w.aapp, "branch", "promote", "--ids", ids).
			Refused(t, "E_LAND_CONFLICT", m2+" (body)", "nothing was promoted").Never(t, m1+" (", task+" (")
		mainKeptItsEdits()
	})
	s.Step("day2/the-child-land-refuses-the-same-text", func() {
		w.aapp.Git("merge", "-q", "--no-ff", "-m", "Merge feat/sy", "feat/sy")
		w.aapp.Git("push", "-q", "origin", "main")
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/sy").
			Refused(t, "E_LAND_CONFLICT", m2+" (body)", "nothing was landed", "lore branch land --branch feat/sy --take main --ids "+m2)
		mainKeptItsEdits()
	})
	s.Step("day2/take-main-keeps-every-main-edit", func() {
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/sy", "--take", "main", "--ids", m2).OK(t).Says(t, "landed")
		mainKeptItsEdits()
		// the child's own wording of the rows main did not reword came in
		w.admin.LoreIn(w.aapp, "show", "--id", m1, "--json").OK(t).Says(t, "(child wording)")
		w.admin.LoreIn(w.aapp, "task", "show", "--id", task).OK(t).Says(t, "child note on the task")
	})
}
