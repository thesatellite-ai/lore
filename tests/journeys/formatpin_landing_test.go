//go:build journeys && !windows

package journeys

// formatpin_landing_test.go — decision D13 (round formatpin, item 5): a
// project on lore 0.1.17's format, with rows on main and a live task on
// refs/lore/live. A teammate on lore 0.1.18 reads it, tries to write, syncs
// and lands; main's row files stay at format 1 after every command, and
// each write is refused with the one E_FORMAT_OLD line, changing nothing.

import (
	"regexp"
	"strings"
	"testing"
)

// fpTaskID finds a task id in what lore printed.
var fpTaskID = regexp.MustCompile(`tsk_[0-9a-f]{32}`)

const (
	fpOldNote = "the deploy key rotates every quarter"
	fpOldTask = "move the cron to the new box"
	fpRefused = "is on the lore 0.1.17 format; run lore migrate run to upgrade it (teammates need lore 0.1.18)"
)

// fpMainFormats fails unless every row file on the remote's main is _v: 1.
func fpMainFormats(t testing.TB, label string, files func(string) []string, read func(string, string) (string, bool)) {
	t.Helper()
	n := 0
	for _, f := range files("main") {
		if !strings.HasPrefix(f, ".lore/data/") || !strings.HasSuffix(f, ".json") || strings.HasSuffix(f, "_meta.json") {
			continue
		}
		body, ok := read("main", f)
		if !ok {
			continue
		}
		n++
		if m := upFormat.FindStringSubmatch(body); m == nil || m[1] != "1" {
			t.Errorf("%s: %s on main is not format 1:\n%s", label, f, body)
		}
	}
	if n == 0 {
		t.Fatalf("%s: no row file on main", label)
	}
}

func TestJourneyFormatPinLanding(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ola := w.Persona("Ola Old", "ola@example.test")
	nia := w.Persona("Nia New", "nia@example.test")
	repo := w.Remote("github.com/acme/app")
	old := UseOldLore(t, ola.Laptop())
	oc := old.Clone(repo, "app")

	w.Step("day1/0117-rows-and-a-live-task", func() {
		old.Old(oc, "init", "--name", "app").OK(t)
		old.Old(oc, "memory", "add", "--body", fpOldNote).OK(t)
		upCommitPush(t, oc, "lore data")
		old.Old(oc, "task", "add", fpOldTask).OK(t) // live: refs/lore/live
		fpMainFormats(t, "0.1.17", repo.Files, repo.Read)
	})

	nm := nia.Laptop()
	nc := nm.Clone(repo, "app")
	w.Step("day1/new-lore-reads-refuses-writes", func() {
		nm.LoreIn(nc, "memory", "list").OK(t).Says(t, fpOldNote)
		nm.LoreIn(nc, "task", "list").OK(t)
		for _, args := range [][]string{
			{"memory", "add", "--body", "a new note"},
			{"task", "add", "a new live task"},
			{"land", "--all"},
		} {
			r := nm.LoreIn(nc, args...)
			if r.Code == 0 || !strings.Contains(r.Stderr, "E_FORMAT_OLD") || !strings.Contains(r.Stderr, fpRefused) {
				t.Errorf("lore %s: want E_FORMAT_OLD, got %s", strings.Join(args, " "), r)
			}
			if lines := strings.Count(strings.TrimSpace(r.Stderr), "\n") + 1; lines != 1 {
				t.Errorf("lore %s: the refusal is %d lines, want one:\n%s", strings.Join(args, " "), lines, r.Stderr)
			}
		}
		nm.LoreIn(nc, "sync").OK(t).Says(t, "nothing synced in this repo")
		if st := strings.TrimSpace(nc.Git("status", "--porcelain")); st != "" {
			t.Errorf("0.1.18 commands changed the 0.1.17 project:\n%s", st)
		}
		fpMainFormats(t, "after 0.1.18 commands", repo.Files, repo.Read)
	})

	w.Step("day2/0117-lands-its-task", func() {
		upPull(t, oc)
		// 0.1.17 finishes its live task and lands it itself: format 1 rows
		if id := fpTaskID.FindString(old.Old(oc, "task", "list").Out()); id != "" {
			old.Old(oc, "task", "done", id)
		}
		old.Old(oc, "sync")
		fpMainFormats(t, "after 0.1.17's landing", repo.Files, repo.Read)
		upPull(t, nc)
		nm.LoreIn(nc, "memory", "list").OK(t).Says(t, fpOldNote)
		if st := strings.TrimSpace(nc.Git("status", "--porcelain")); st != "" {
			t.Errorf("a pull and a read changed the 0.1.17 project:\n%s", st)
		}
	})
}
