//go:build stories && !windows

package stories

// O_linked_leftover_test.go — devco:D-729 on the owner-shaped machine
// (story O): an own repo linked to a store folder (`lore whoami`: reads
// <org>/<folder>, this repo's link) that still holds old notes in its
// .lore/data. `lore task add` with no --to wrote the task into the repo
// ("store": "repo"), and it went out in the repo's next pull request: the
// D-723 rule moved the default only for a repo with no notes left. Now
// every write with no --to in a linked repo goes to the linked folder,
// whatever the repo still holds; --to repo is the one way into the repo;
// `lore doctor` names the leftover notes and the one command that moves
// them, and that command runs as printed. Cells: linked with leftover
// notes, linked with none left (after the move), and unlinked (writes the
// repo as before).

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// leftoverMoveCmd is the command doctor prints for the leftover notes.
var leftoverMoveCmd = regexp.MustCompile(`lore store adopt --target \S+ --move( --own)? --yes`)

func TestStoryOLinkedRepoWithLeftoverNotes(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	syncApp := s.Seed(s.Remote("github.com/"+oOwner+"/sync-app"), func(dir string) { PlainRepo(t, dir) })
	solo := s.Seed(s.Remote("github.com/"+oOwner+"/solo-app"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	sc := ada.CloneTo(syncApp, ada.OrgDir("sync-app"))
	uc := ada.CloneTo(solo, ada.OrgDir("solo-app"))
	const folder = "sync"
	place := oOrg + "/" + folder
	clone := oStoreClone(ada, oOwner, "lore-team")
	clean := func(c interface{ Git(...string) string }) string {
		return strings.TrimSpace(c.Git("status", "--porcelain", "--untracked-files=all", ".lore"))
	}

	s.Step("D-729/linked-with-leftover-notes", func() {
		ada.Lore(sc.Dir, "init", "--non-interactive", "--name=sync-app").OK(t)
		ada.Lore(sc.Dir, "memory", "add", "--body", "the old cache note", "--to", "repo").OK(t)
		ada.Lore(sc.Dir, "hotfix", "add", "--title", "pin the queue client", "--body", "the new client drops messages", "--to", "repo").OK(t)
		sc.CommitAll("lore notes")
		ada.Lore(pc.Dir, "store", "folder", "add", oOrg, folder).OK(t)
		ada.Lore(sc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(sc.Dir, "org", "link", "--org", oOrg, "--project", folder, "--own", "--yes").OK(t)
		if st := clean(sc); st != "" {
			t.Fatalf("setup left the repo dirty:\n%s", st)
		}
	})

	s.Step("D-729/task-and-memory-add-without-to-go-to-the-store", func() {
		for _, c := range []struct {
			args []string
			text string
		}{
			{[]string{"task", "add", "--title", "rotate the sync token"}, "rotate the sync token"},
			{[]string{"memory", "add", "--body", "the sync runs every ten minutes"}, "the sync runs every ten minutes"},
			{[]string{"behaviour", "add", "--title", "sync retries", "--body", "retry a sync three times"}, "retry a sync three times"},
		} {
			r := ada.Lore(sc.Dir, c.args...).OK(t)
			if st := clean(sc); st != "" {
				t.Fatalf("lore %s with no --to wrote into a linked repo that still holds notes:\n%s\nit said:\n%s", c.args[0], st, r.Out())
			}
			if !strings.Contains(r.Out(), place) {
				t.Errorf("lore %s add does not name the linked folder %s it wrote to:\n%s", c.args[0], place, r.Out())
			}
			if !dirHolds(t, filepath.Join(clone, folder), c.text) {
				t.Errorf("the %s is not in the store folder %s", c.args[0], place)
			}
		}
	})

	s.Step("D-729/help-names-the-linked-default", func() {
		for _, cmd := range [][]string{{"task", "add", "--help"}, {"memory", "add", "--help"}, {"rule", "add", "--help"}} {
			out := ada.Lore(sc.Dir, cmd...).OK(t).Out()
			if !strings.Contains(out, "default: the store folder this repo is linked to") {
				t.Errorf("lore %s %s --help does not say a linked repo writes its store folder by default:\n%s", cmd[0], cmd[1], out)
			}
		}
	})

	s.Step("D-729/to-repo-still-writes-the-repo", func() {
		ada.Lore(sc.Dir, "task", "add", "--title", "kept in the repo on purpose", "--to", "repo", "--no-live").OK(t)
		if st := clean(sc); !strings.Contains(st, ".lore/data/tasks/") {
			t.Fatalf("--to repo no longer writes the repo:\n%s", st)
		}
		sc.CommitAll("lore task on purpose")
	})

	var printed string
	s.Step("D-729/doctor-names-the-leftover-notes-and-the-move", func() {
		out := ada.Lore(sc.Dir, "doctor").Out()
		if !strings.Contains(out, "still holds notes of its own") {
			t.Fatalf("doctor does not warn about the notes left in a linked repo:\n%s", out)
		}
		printed = leftoverMoveCmd.FindString(out)
		if printed == "" {
			t.Fatalf("doctor prints no adopt --move command for the leftover notes:\n%s", out)
		}
	})

	s.Step("D-729/the-printed-move-runs-as-printed", func() {
		args := strings.Fields(strings.TrimPrefix(printed, "lore "))
		ada.Lore(sc.Dir, args...).OK(t)
		sc.CommitAll("lore: move the notes to " + place)
		if dirHolds(t, filepath.Join(sc.Dir, ".lore", "data", "memories"), "the old cache note") {
			t.Errorf("the leftover memory is still in the repo after %q", printed)
		}
		if !dirHolds(t, filepath.Join(clone, folder), "the old cache note") {
			t.Errorf("the leftover memory did not reach %s after %q", place, printed)
		}
		if out := ada.Lore(sc.Dir, "doctor").Out(); strings.Contains(out, "still holds notes of its own") {
			t.Errorf("doctor still warns after the move ran:\n%s", out)
		}
	})

	s.Step("D-729/linked-with-no-notes-left-writes-the-store", func() {
		r := ada.Lore(sc.Dir, "task", "add", "--title", "after the move").OK(t)
		if st := clean(sc); st != "" {
			t.Fatalf("task add with no --to wrote into a linked repo with no notes left:\n%s\n%s", st, r.Out())
		}
	})

	s.Step("D-736/doctor-fix-as-printed-leaves-no-rows", func() {
		// owner item 29: a linked repo's .lore/data holds no rows; its
		// identity lives in the store link (_meta.json stays, not a row)
		out := ada.Lore(sc.Dir, "doctor").Out()
		if !strings.Contains(out, "rows the folder already holds") || !strings.Contains(out, "lore doctor --fix") {
			t.Fatalf("doctor does not offer to clean the linked repo's rows:\n%s", out)
		}
		// doctor exits 1 while other findings stay (a skill not installed
		// here); the clean is its own line
		ada.Lore(sc.Dir, "doctor", "--fix", "--yes").Says(t, "this linked repo's .lore/data: removed ").Never(t, "could not clean")
		sc.CommitAll("lore: clean the linked repo")
		for _, f := range strings.Fields(sc.Git("ls-files", ".lore/data")) {
			if f != ".lore/data/_meta.json" && f != ".lore/data/_purged.json" {
				t.Errorf("doctor --fix left %s in the linked repo", f)
			}
		}
		if out := ada.Lore(sc.Dir, "doctor").Out(); strings.Contains(out, "rows the folder already holds") {
			t.Errorf("doctor still offers to clean after doctor --fix ran:\n%s", out)
		}
	})

	s.Step("D-736/cleaned-linked-repo-reads-writes-hooks-render-sync", func() {
		ada.Lore(sc.Dir, "memory", "list").OK(t).Says(t, "the old cache note")
		ada.Lore(sc.Dir, "task", "add", "--title", "after the clean").OK(t).Says(t, place)
		ada.Lore(sc.Dir, "branch", "mode").OK(t)
		ada.Lore(sc.Dir, "render", "--add-target", "cursor", "--yes").OK(t)
		ada.Lore(sc.Dir, "sync").OK(t)
		ada.Lore(sc.Dir, "whoami").OK(t).Says(t, place)
		if st := clean(sc); st != "" {
			for _, l := range strings.Split(st, "\n") {
				if strings.Contains(l, ".lore/data/") && !strings.HasSuffix(l, "/_meta.json") { // LORE.md is render's output, not a row
					t.Errorf("a read, write, render or sync wrote rows into the cleaned linked repo:\n%s", st)
					break
				}
			}
		}
		if !dirHolds(t, filepath.Join(clone, folder), "after the clean") {
			t.Errorf("the task written after the clean is not in %s", place)
		}
	})

	s.Step("D-729/unlinked-repo-writes-the-repo", func() {
		ada.Lore(uc.Dir, "init", "--non-interactive", "--name=solo-app").OK(t)
		uc.CommitAll("lore init")
		ada.Lore(uc.Dir, "task", "add", "--title", "a task of an unlinked repo", "--no-live").OK(t)
		if st := clean(uc); !strings.Contains(st, ".lore/data/tasks/") {
			t.Fatalf("task add with no --to in an unlinked repo did not write the repo:\n%s", st)
		}
		if out := ada.Lore(uc.Dir, "doctor").Out(); strings.Contains(out, "still holds notes of its own") {
			t.Errorf("doctor warns about leftover notes in an unlinked repo:\n%s", out)
		}
	})
}
