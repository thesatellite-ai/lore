//go:build stories && !windows

package stories

// O_adopt_commit_test.go — devco:D-710 on the owner-shaped machine (story
// O): the owner adopted a repo of the org into a folder of the org store
// with `lore store adopt --remove-source`, and lore's own pre-commit then
// refused the commit of that removal ("deletes 204 lore rows by removing
// the file, not with lore"). Now the command adopt prints commits through
// the real hooks, the push goes, and the store folder holds every row.

import (
	"strings"
	"testing"
)

func TestStoryOAdoptRemoveSourceCommits(t *testing.T) {
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

	s.Step("D-710/a-repo-with-its-own-rows", func() {
		ada.Lore(nc.Dir, "init", "--non-interactive", "--name=notes-app").OK(t)
		ada.Lore(nc.Dir, "rule", "add", "--body", "Run the linter before every push", "--severity", "must", "--to", "repo", "--yes").OK(t)
		ada.Lore(nc.Dir, "memory", "add", "--body", "the cache warms at boot", "--to", "repo").OK(t)
		nc.CommitAll("lore notes")
		nc.Git("push", "-q", "origin", "HEAD:main")
		ada.Lore(pc.Dir, "store", "folder", "add", oOrg, folder).OK(t)
	})

	s.Step("D-710/adopt-and-commit-as-printed", func() {
		r := ada.Lore(nc.Dir, "store", "adopt", place, "--remove-source", "--yes").OK(t)
		want := "git -C " + nc.Dir + " add -A .lore/data && git -C " + nc.Dir + " commit -m \"lore: move the notes to " + place + "\""
		if !strings.Contains(r.Out(), want) {
			t.Fatalf("adopt does not print the commit that works:\n%s\nwant %s", r.Out(), want)
		}
		nc.Git("add", "-A", ".lore/data")
		c := nc.TryGit("commit", "-m", "lore: move the notes to "+place)
		if c.Code != 0 || !strings.Contains(c.Out(), "moved to "+place) {
			t.Fatalf("lore's hook on adopt's removal: exit %d\n%s", c.Code, c.Out())
		}
		nc.Git("push", "-q", "origin", "HEAD:main")
		if out := nc.Git("ls-files", ".lore/data/rules", ".lore/data/memories"); out != "" {
			t.Errorf("the repo still tracks adopted row files:\n%s", out)
		}
	})
}
