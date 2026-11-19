//go:build stories && !windows

package stories

// O_linked_default_test.go — devco:D-723 on the owner-shaped machine
// (story O): the owner moved a repo's notes into a folder of the org store
// (`lore store adopt --remove-source`, then `lore org link`), and the next
// `lore memory add` with no --to wrote the memory, and an actor row for
// its author, back into the code repo's .lore/data. A repo linked to a
// store folder whose notes moved out writes, with no --to, to its linked
// folder: the row and its actor land in the store, the repo stays clean.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryOLinkedRepoWritesToItsFolder(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	notes := s.Seed(s.Remote("github.com/"+oOwner+"/notes-app"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	nc := ada.CloneTo(notes, ada.OrgDir("notes-app"))
	const folder = "client_notes"
	place := oOrg + "/" + folder
	clone := oStoreClone(ada, oOwner, "lore-team")

	s.Step("D-723/notes-moved-out-and-linked", func() {
		ada.Lore(nc.Dir, "init", "--non-interactive", "--name=notes-app").OK(t)
		ada.Lore(nc.Dir, "memory", "add", "--body", "the cache warms at boot", "--to", "repo").OK(t)
		nc.CommitAll("lore notes")
		ada.Lore(pc.Dir, "store", "folder", "add", oOrg, folder).OK(t)
		ada.Lore(nc.Dir, "store", "adopt", place, "--remove-source", "--yes").OK(t)
		nc.Git("add", "-A", ".lore/data")
		nc.Git("commit", "-q", "-m", "lore: move the notes to "+place)
		ada.Lore(nc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(nc.Dir, "org", "link", "--org", oOrg, "--project", folder, "--own", "--yes").OK(t)
	})

	s.Step("D-723/memory-add-without-to-lands-in-the-linked-folder", func() {
		const body = "the nightly export runs at two"
		r := ada.Lore(nc.Dir, "memory", "add", "--body", body).OK(t)
		if st := nc.Git("status", "--porcelain", "--untracked-files=all", ".lore"); strings.TrimSpace(st) != "" {
			t.Fatalf("memory add with no --to wrote into the code repo of a linked repo:\n%s\nadd said:\n%s", st, r.Out())
		}
		if !strings.Contains(r.Out(), place) {
			t.Errorf("memory add does not name the linked folder %s it wrote to:\n%s", place, r.Out())
		}
		if !dirHolds(t, filepath.Join(clone, folder), body) {
			t.Errorf("the memory is not in the store folder %s", place)
		}
		l := ada.Lore(nc.Dir, "memory", "list").OK(t)
		if !strings.Contains(l.Out(), body) {
			t.Errorf("memory list in the linked repo does not show the new memory:\n%s", l.Out())
		}
	})

	s.Step("D-723/to-repo-still-writes-the-repo", func() {
		ada.Lore(nc.Dir, "memory", "add", "--body", "kept in the repo on purpose", "--to", "repo").OK(t)
		if st := nc.Git("status", "--porcelain", "--untracked-files=all", ".lore/data/memories"); strings.TrimSpace(st) == "" {
			t.Errorf("--to repo no longer writes the repo")
		}
	})
}

// dirHolds reports whether a file under dir contains text.
func dirHolds(t *testing.T, dir, text string) bool {
	t.Helper()
	found := false
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || found {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr == nil && strings.Contains(string(b), text) {
			found = true
		}
		return nil
	})
	return found
}
