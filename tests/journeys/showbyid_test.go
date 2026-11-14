//go:build journeys && !windows

package journeys

// showbyid_test.go — a row written with `--to <org>/<folder>` is shown and
// edited by id from the repo it was written in (B2-21). The folder is not
// attached to the repo; the write attached it for that one command, and
// show/edit by id used to answer E_NOT_FOUND "it may have landed on main".

import (
	"path/filepath"
	"regexp"
	"testing"
)

// sbTaskID matches the id `task add` prints.
var sbTaskID = regexp.MustCompile(`tsk_[0-9a-f]+`)

func TestJourneyShowByIDInAnOrgFolder(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	var id string

	w.Step("day1/a-task-in-a-folder-the-repo-does-not-attach", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		// the org's own repos, and app linked to its own folder "core" (so
		// the team store is attached at core/, not at its root)
		s := lee.Clone(tt.store, "team-store")
		jWrite(t, filepath.Join(s.Dir, filepath.FromSlash(jManyOrgFile)), "name: "+tlOrg+"\nstores:\n"+
			"    - url: "+tt.store.SSHURL()+"\n      org_folder: org\nown:\n    - github.com/acme-co/*\n")
		s.CommitAll("the org's own repos")
		s.Git("push", "-q", "origin", "HEAD:main")
		parent := filepath.Join(lee.Home, "code")
		lee.Lore(parent, "org", "refresh", tlOrg).OK(t)
		lee.Lore(parent, "store", "folder", "add", "team", "core").OK(t)
		lee.Lore(parent, "store", "folder", "add", "team", "sync").OK(t)
		lee.Lore(parent, "sync").OK(t) // C5: the folders reach the store with Lee's sync
		tt.lore(tlLee, "init", "--non-interactive", "--name=app").OK(t)
		tt.lore(tlLee, "org", "link", tlOrg, "--project", "core", "--own", "--yes").OK(t)
		r := tt.lore(tlLee, "task", "add", "wire the sync worker", "--to", tlOrg+"/sync", "--yes").OK(t)
		id = sbTaskID.FindString(r.Out())
		if id == "" {
			t.Fatalf("task add printed no id: %s", r)
		}
		tt.lore(tlLee, "sync").OK(t) // C5: the task reaches the store with Lee's sync
	})

	w.Step("day1/show-and-edit-it-by-id", func() {
		tt.lore(tlLee, "task", "show", id).OK(t).Says(t, "wire the sync worker")
		if f := tt.store.FilesMatching("refs/heads/main", "sync/tasks/"); len(f) != 1 {
			t.Fatalf("the task is not in the sync folder: %v", f)
		}
		tt.lore(tlLee, "task", "edit", id, "--title", "wire the sync worker (v2)", "--yes").OK(t)
		tt.lore(tlLee, "task", "show", id).OK(t).Says(t, "wire the sync worker (v2)")
		// an id that is nowhere says where lore looked (it used to guess
		// "it may have landed on main")
		tt.lore(tlLee, "task", "show", "tsk_01a1d1e955aa77ec8b5385a6740c1799").Says(t, "E_NOT_FOUND", "looked in the stores attached here", tlOrg+"/sync")
	})
}
