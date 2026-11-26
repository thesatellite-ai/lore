//go:build stories && !windows

package stories

// O_one_author_test.go — devco:D-714 on the owner-shaped machine (story
// O): the owner's org-store commits had two authors. `lore org link`
// signed as the org identity (orgs[<org>].identity) while `lore store
// adopt` let the clone's git user sign, whose user.name was the owner's
// git handle beside the org email. One rule now, for every op: every
// commit lore makes in an org store is authored and committed by the org
// identity, whatever the clone's git user.name says.

import (
	"strings"
	"testing"
)

func TestStoryOOneAuthorForEveryStoreCommit(t *testing.T) {
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
	const handle = "ada-git-handle" // the person's git user.name, beside the org email
	var base string

	s.Step("D-714/the-clone-has-a-git-user-name-of-its-own", func() {
		ada.Lore(nc.Dir, "init", "--non-interactive", "--name=notes-app").OK(t)
		ada.Lore(nc.Dir, "memory", "add", "--body", "the queue drains at midnight", "--to", "repo").OK(t)
		nc.CommitAll("lore notes")
		ada.Lore(pc.Dir, "store", "folder", "add", oOrg, folder).OK(t)
		s.Git(clone, "config", "user.name", handle)
		s.Git(clone, "config", "user.email", oAdminOrg.Email)
		base = strings.TrimSpace(s.Git(clone, "rev-parse", "HEAD"))
	})

	s.Step("D-714/org-link-and-adopt-sign-as-the-org-identity", func() {
		ada.Lore(nc.Dir, "org", "link", "--org", oOrg, "--project", folder, "--yes").OK(t)
		ada.Lore(nc.Dir, "store", "adopt", place, "--yes").OK(t)
		// the ops' own commits, before the sync folds them into one batch
		// per author (devco:D-1000), which keeps that author
		local := strings.ToLower(s.Git(clone, "log", "--format=%s", "--branches", "^"+base))
		if !strings.Contains(local, "adopt") || !strings.Contains(local, "link") {
			t.Fatalf("fixture: want an org link commit and an adopt commit in the store since the step began, got:\n%s", local)
		}
		ada.Lore(pc.Dir, "sync").OK(t)
		want := oAdminOrg.Name + "|" + oAdminOrg.Email + "|" + oAdminOrg.Name + "|" + oAdminOrg.Email
		log := strings.TrimSpace(s.Git(clone, "log", "--format=%an|%ae|%cn|%ce|%s", "--branches", "--remotes", "^"+base))
		var subjects []string
		for line := range strings.SplitSeq(log, "\n") {
			if line == "" {
				continue
			}
			f := strings.SplitN(line, "|", 5)
			subjects = append(subjects, f[4])
			if got := strings.Join(f[:4], "|"); got != want {
				t.Errorf("the store commit %q is by %s, want the org identity %s (one author for every op)", f[4], got, want)
			}
		}
		if len(subjects) == 0 {
			t.Fatal("fixture: no store commit since the step began")
		}
	})
}
