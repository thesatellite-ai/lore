//go:build stories && !windows

package stories

// O_supersedes_linked_test.go — devco:D-911 (bug hunt H-169, public issue
// #134) on the owner-shaped machine (story O): a store folder got its own
// project row when a note was filed into it from another repo (`--to
// <org>/<folder>`), then a code repo with its own lore project (`lore
// init`) was linked to that folder. The repo's writes land in the folder
// with the folder's project id, not the repo's, and `decision add
// --supersedes <id>` answered "no decision with that id in this project"
// for the row `decision show` and `decision edit` found. --supersedes
// counts the linked folder's project as this repo's, for every noun that
// takes it (a knowledge noun, memory, pattern); a row of another project
// is still refused.

import (
	"regexp"
	"testing"
)

// oSupersedesPatID finds a pattern id (aIDs knows no pat_ prefix).
var oSupersedesPatID = regexp.MustCompile(`\bpat_[0-9a-f]{32}\b`)

func TestStoryOSupersedesInALinkedFolder(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	app := s.Seed(s.Remote("github.com/"+oOwner+"/supersede-app"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	ac := ada.CloneTo(app, ada.OrgDir("supersede-app"))
	const folder = "supersede_app"
	place := oOrg + "/" + folder

	s.Step("D-911/folder-project-first-then-own-repo-linked", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", oOrg, folder).OK(t)
		ada.Lore(pc.Dir, "decision", "add", "--title", "Folder for the app", "--body", "Notes about the app live here.", "--to", place).OK(t)
		ada.Lore(ac.Dir, "init", "--non-interactive", "--name=supersede-app").OK(t)
		ada.Lore(ac.Dir, "org", "link", "--org", oOrg, "--project", folder, "--yes").OK(t)
	})

	s.Step("D-911/decision-supersedes-a-row-of-the-linked-folder", func() {
		a := aID(t, ada.Lore(ac.Dir, "decision", "add", "--title", "Use SQLite", "--body", "Local first.", "--json").OK(t), "dec")
		ada.Lore(ac.Dir, "decision", "show", a).Says(t, place)
		r := ada.Lore(ac.Dir, "decision", "add", "--title", "Use Postgres", "--body", "Need concurrent writers.", "--supersedes", a)
		if r.Code != 0 {
			t.Fatalf("decision add --supersedes a row decision show finds (exit %d):\n%s", r.Code, r.Out())
		}
		ada.Lore(ac.Dir, "decision", "show", a).Says(t, "superseded")
	})

	s.Step("D-911/memory-and-pattern-supersede-too", func() {
		m := aID(t, ada.Lore(ac.Dir, "memory", "add", "--body", "the cache warms at boot", "--json").OK(t), "mem")
		if r := ada.Lore(ac.Dir, "memory", "add", "--body", "the cache warms on first use", "--supersedes", m); r.Code != 0 {
			t.Fatalf("memory add --supersedes in a linked repo (exit %d):\n%s", r.Code, r.Out())
		}
		p := oSupersedesPatID.FindString(ada.Lore(ac.Dir, "pattern", "add", "--title", "Retry", "--body", "retry three times", "--json").OK(t).Out())
		if p == "" {
			t.Fatalf("pattern add --json printed no pat_ id")
		}
		if r := ada.Lore(ac.Dir, "pattern", "add", "--title", "Retry with backoff", "--body", "retry with backoff", "--supersedes", p); r.Code != 0 {
			t.Fatalf("pattern add --supersedes in a linked repo (exit %d):\n%s", r.Code, r.Out())
		}
	})

	s.Step("D-911/another-projects-row-is-still-refused", func() {
		ada.Lore(pc.Dir, "init", "--non-interactive", "--name=platform") // the setup may have made it already
		other := aID(t, ada.Lore(pc.Dir, "decision", "add", "--title", "Platform only", "--body", "Not the app's.", "--to", "repo", "--json").OK(t), "dec")
		r := ada.Lore(pc.Dir, "decision", "add", "--title", "Use Postgres", "--body", "b", "--to", place, "--supersedes", other)
		if r.Code == 0 {
			t.Fatalf("--supersedes accepted a decision of another project:\n%s", r.Out())
		}
	})
}
