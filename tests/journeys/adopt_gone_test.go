//go:build journeys && !windows

package journeys

// adopt_gone_test.go — devco:D-737 (owner, moving a repo's notes into a
// store): the repo's open tasks were on its live ref; the owner deleted
// them, ran lore trash purge --confirm and lore live purge --confirm, and
// lore store adopt still counted and copied them ("from the repo's live
// ref: 38", 38 near-duplicates). Only lore live compact made adopt see
// them gone. Now a deleted, trashed or purged live row is never adopted,
// counted or listed as a near-duplicate, whether or not the purge has
// been pushed yet; an open live task is adopted as before.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

const (
	jAgFolder = "client_gone"
	jAgSlug   = "github.com/acme-co/gone-app"
)

// jAgTaskID adds an open (live) task titled title in dir and returns its id.
func jAgTaskID(t *testing.T, m *Machine, dir, title string) string {
	t.Helper()
	r := m.Lore(dir, "task", "add", "--title", title, "--commitment", "accepted", "--to", "repo", "--json").OK(t)
	i := strings.Index(r.Stdout, `"id": "tsk_`)
	if i < 0 {
		t.Fatalf("task add printed no id:\n%s", r.Stdout)
	}
	return r.Stdout[i+len(`"id": "`) : i+len(`"id": "`)+36]
}

func TestJourneyAdoptSkipsGoneLiveRows(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	repo := jPlainRepo(t, lee, w.Remote(jAgSlug), filepath.Join(parent, "gone-app"))
	titles := []string{"Rotate the staging certs", "Archive the old invoices", "Check the backup restore", "Renew the domain", "Close the old tickets"}
	ids := map[string]string{}
	gone := titles[:3] // deleted (trash), deleted + trash purge (tombstone), live purge
	open := titles[3:]

	w.Step("day1/five-open-live-tasks-shared", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", jAgFolder).OK(t)
		lee.Lore(repo.Dir, "init", "--non-interactive", "--name=gone").OK(t)
		repo.CommitAll("lore init")
		repo.Git("push", "-q", "origin", "HEAD:main")
		for _, title := range titles {
			ids[title] = jAgTaskID(t, lee, repo.Dir, title)
		}
		lee.LoreIn(repo, "sync").OK(t) // the remote (and the tracking copy) hold all five open
		if n := jAmLiveTasks(t, repo.Git); n != len(titles) {
			t.Fatalf("%d tasks on the live ref, want %d", n, len(titles))
		}
	})

	w.Step("day2/three-leave-and-the-purge-is-not-pushed-yet", func() {
		w.AdvanceDays(1)
		lee.LoreIn(repo, "task", "delete", "--id", ids[gone[0]], "--yes").OK(t)
		lee.LoreIn(repo, "task", "delete", "--id", ids[gone[1]], "--yes").OK(t)
		lee.LoreIn(repo, "trash", "purge", "--id", ids[gone[1]], "--confirm").OK(t)
		lee.LoreIn(repo, "live", "purge", "--ids", ids[gone[2]], "--confirm", "--yes").OK(t)
		list := lee.LoreIn(repo, "task", "list").OK(t)
		for _, title := range gone {
			list.Never(t, title)
		}
	})

	adoptNever := func(r teamharness.Result) {
		for _, title := range gone {
			r.Never(t, title, ids[title])
		}
	}

	w.Step("day2/the-dry-run-counts-the-open-tasks-only", func() {
		r := lee.LoreIn(repo, "store", "adopt", "--target", "team/"+jAgFolder, "--move", "--dry-run").OK(t)
		// the two open tasks and their task list leave the live ref
		r.Says(t, "tasks 2", "would take 3 adopted rows off the repo's live ref", "tasks: 2 rows (deleted, in the trash or purged: never adopted)")
		adoptNever(r)
		j := lee.LoreIn(repo, "store", "adopt", "--target", "team/"+jAgFolder, "--move", "--dry-run", "--json").OK(t)
		j.Says(t, `"live": 3`).Never(t, `"duplicates"`)
	})

	w.Step("day2/the-move-copies-the-open-tasks-only", func() {
		adoptNever(lee.LoreIn(repo, "store", "adopt", "--target", "team/"+jAgFolder, "--move", "--yes").OK(t).Says(t, "✓ adopted "))
		lee.Lore(parent, "sync").OK(t)
		files := strings.Join(tt.store.FilesMatching("refs/heads/main", jAgFolder+"/tasks/"), "\n")
		for _, title := range gone {
			if strings.Contains(files, ids[title]) {
				t.Errorf("the folder holds the gone task %q (%s)", title, ids[title])
			}
		}
		for _, title := range open {
			if !strings.Contains(files, ids[title]) {
				t.Errorf("the open task %q (%s) was not adopted:\n%s", title, ids[title], files)
			}
		}
		list := lee.LoreIn(repo, "task", "list").OK(t)
		for _, title := range open {
			list.Says(t, title)
		}
		for _, title := range gone {
			list.Never(t, title)
		}
	})
}
