//go:build stories && !windows

package stories

// O_adopt_commit_test.go — devco:D-710 on the owner-shaped machine (story
// O): the owner adopted a repo of the org into a folder of the org store
// with `lore store adopt --remove-source`, and lore's own pre-commit then
// refused the commit of that removal ("deletes 204 lore rows by removing
// the file, not with lore"). Now the command adopt prints commits through
// the real hooks, the push goes, and the store folder holds every row.
// devco:D-713, D-716: the whole move of a second repo with open (live)
// rows is one command, `lore store adopt --move --commit`: the rows leave
// the repo's live ref and files, the repo is linked to the folder and the
// commit goes through the hooks; the repo then reads each id once.

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

func TestStoryOAdoptMoveOneCommand(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	ledger := s.Seed(s.Remote("github.com/"+oOwner+"/ledger-app"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	lc := ada.CloneTo(ledger, ada.OrgDir("ledger-app"))
	const folder = "ledger"
	place := oOrg + "/" + folder
	var task, rule string

	s.Step("D-713/a-repo-with-open-live-rows", func() {
		ada.Lore(lc.Dir, "init", "--non-interactive", "--name=ledger-app").OK(t)
		rule = idAfter(t, ada.Lore(lc.Dir, "rule", "add", "--body", "Run the linter before every push", "--severity", "must", "--to", "repo", "--yes", "--json").OK(t).Stdout, "id", "rul_")
		r := ada.Lore(lc.Dir, "task", "add", "reconcile the ledger", "--to", "repo", "--json").OK(t)
		i := strings.Index(r.Stdout, `"id": "tsk_`)
		if i < 0 {
			t.Fatalf("task add printed no id:\n%s", r.Stdout)
		}
		task = r.Stdout[i+len(`"id": "`) : i+len(`"id": "`)+36]
		lc.CommitAll("lore notes")
		lc.Git("push", "-q", "origin", "HEAD:main")
		if !strings.Contains(lc.Git("ls-tree", "-r", "--name-only", "refs/lore/live"), task) {
			t.Fatalf("the open task is not on the live ref: the step would prove nothing")
		}
		ada.Lore(pc.Dir, "store", "folder", "add", oOrg, folder).OK(t)
	})

	s.Step("D-713/one-command-moves-links-and-commits", func() {
		ada.Lore(lc.Dir, "store", "adopt", "--target", place, "--move", "--commit", "--yes").OK(t).
			Says(t, "off the repo's live ref", "and committed it (", "link: ")
		if got := lc.Git("log", "-1", "--format=%s"); got != "lore: move the notes to "+place {
			t.Fatalf("the repo's last commit is %q", got)
		}
		if strings.Contains(lc.Git("ls-tree", "-r", "--name-only", "refs/lore/live"), task) {
			t.Errorf("the moved task is still on the repo's live ref")
		}
		ada.Lore(lc.Dir, "show", "--id", task).OK(t).Never(t, "two live rows", "E_AMBIGUOUS_ID")
	})

	// D-713 (use pass 5): until the repo commit is pushed, the repo's
	// upstream still holds the moved row files; a bare id names the
	// folder's copy only, so show, edit and delete work before the push.
	s.Step("D-713/bare-id-of-a-moved-file-row-before-the-push", func() {
		ada.Lore(lc.Dir, "show", "--id", rule).OK(t).Says(t, "Run the linter before every push").Never(t, "two live rows", "E_AMBIGUOUS_ID")
		ada.Lore(lc.Dir, "rule", "edit", "--id", rule, "--body", "Run the linter before every push, always", "--yes").OK(t)
		ada.Lore(lc.Dir, "show", "--id", rule, "--store", place).OK(t).Says(t, "always")
		ada.Lore(lc.Dir, "rule", "delete", "--id", rule, "--yes").OK(t)
		lc.Git("push", "-q", "origin", "HEAD:main")
		if r := ada.Lore(lc.Dir, "rule", "list", "--json"); strings.Contains(r.Out(), rule) {
			t.Errorf("the deleted rule is still listed after the push:\n%s", r.Out())
		}
	})
}

// idAfter is the id in out that follows the json key `"<key>": "<prefix>`.
func idAfter(t *testing.T, out, key, prefix string) string {
	t.Helper()
	mark := `"` + key + `": "` + prefix
	i := strings.Index(out, mark)
	if i < 0 {
		t.Fatalf("no %s%s… in:\n%s", key, prefix, out)
	}
	rest := out[i+len(`"`+key+`": "`):]
	return rest[:strings.Index(rest, `"`)]
}

// TestStoryOAdoptListsNearDuplicates is devco:D-724: the owner adopted a
// repo into a folder that already held curated rows and got two "portless"
// rules. adopt --move now lists each near-duplicate (same name, or a body
// with most words in common) with both ids, the command that compares
// them and the one that drops one; the dry run lists them too; nothing is
// merged; and the printed commands run as printed in the repo.
func TestStoryOAdoptListsNearDuplicates(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	devbox := s.Seed(s.Remote("github.com/"+oOwner+"/devbox-app"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	dc := ada.CloneTo(devbox, ada.OrgDir("devbox-app"))
	const folder = "devbox"
	place := oOrg + "/" + folder
	var curated, copied string

	s.Step("D-724/a-curated-folder-and-a-repo-with-the-same-rule", func() {
		ada.Lore(pc.Dir, "store", "folder", "add", oOrg, folder).OK(t)
		curated = idAfter(t, ada.Lore(pc.Dir, "rule", "add", "--body", "Use portless for dev URLs.", "--severity", "must", "--to", place, "--yes", "--json").OK(t).Stdout, "id", "rul_")
		ada.Lore(dc.Dir, "init", "--non-interactive", "--name=devbox-app").OK(t)
		copied = idAfter(t, ada.Lore(dc.Dir, "rule", "add", "--body", "use portless for dev urls\nso every dev server has a stable name", "--severity", "must", "--to", "repo", "--yes", "--json").OK(t).Stdout, "id", "rul_")
		ada.Lore(dc.Dir, "rule", "add", "--body", "Run the linter before every push", "--severity", "must", "--to", "repo", "--yes").OK(t)
		dc.CommitAll("lore notes")
		dc.Git("push", "-q", "origin", "HEAD:main")
	})

	var pair, compare, drop string
	s.Step("D-724/dry-run-lists-the-pair", func() {
		pair = copied + " ~ " + curated
		compare = "lore show --id " + copied + " --store " + place + " && lore show --id " + curated + " --store " + place
		drop = "lore rule delete --id " + curated + " --yes (the folder's), or the moved one: lore rule delete --id " + copied + " --yes"
		// the dry run reads the moving row in the repo, where it still is
		// (a cd into its top first: D-724, see the --from step below)
		dryCompare := " && lore show --id " + copied + " --store repo && lore show --id " + curated + " --store " + place
		dryDrop := "lore rule delete --id " + curated + " --yes (the folder's), or the moved one: cd "
		ada.Lore(dc.Dir, "store", "adopt", "--target", place, "--move", "--dry-run").OK(t).
			Says(t, "would leave 1 near-duplicate", pair, "(same title)", "compare: cd ", dryCompare, dryDrop, " && lore rule delete --id "+copied+" --yes").
			Never(t, "Run the linter", "once the repo commit is pushed")
		ada.Lore(dc.Dir, "show", "--id", copied, "--store", "repo").OK(t).Says(t, "so every dev server has a stable name")
	})

	s.Step("D-724/dry-run-from-another-repo-reads-the-row-where-printed", func() {
		// run in another repo (--from names the one that moves, no --move):
		// the moving row's read was --store <folder>, where it is not yet;
		// now a cd into the moving repo, then --store repo, runs as printed
		out := ada.Lore(pc.Dir, "store", "adopt", "--target", place, "--from", dc.Dir, "--dry-run").OK(t).
			Says(t, "would leave 1 near-duplicate", pair, "compare: cd ", " && lore show --id "+copied+" --store repo && lore show --id "+curated+" --store "+place).
			Never(t, "--id "+copied+" --store "+place).Out()
		_, rest, _ := strings.Cut(out, "compare: cd ")
		dir, _, _ := strings.Cut(rest, " && ")
		ada.Lore(dir, "show", "--id", copied, "--store", "repo").OK(t).Says(t, "so every dev server has a stable name")
		ada.Lore(dir, "show", "--id", curated, "--store", place).OK(t).Says(t, "Use portless for dev URLs.")
	})

	s.Step("D-724/move-lists-both-kept-and-the-commands-run-as-printed", func() {
		ada.Lore(dc.Dir, "store", "adopt", "--target", place, "--move", "--commit", "--yes").OK(t).
			Says(t, "1 near-duplicate", pair, "both kept", compare, drop)
		// compare and drop, as printed, before the repo commit is pushed
		// (a moved row's bare id names the folder's copy: D-713)
		ada.Lore(dc.Dir, "show", "--id", copied, "--store", place).OK(t).Says(t, "so every dev server has a stable name")
		ada.Lore(dc.Dir, "show", "--id", curated, "--store", place).OK(t).Says(t, "Use portless for dev URLs.")
		ada.Lore(dc.Dir, "rule", "delete", "--id", copied, "--yes").OK(t)
		dc.Git("push", "-q", "origin", "HEAD:main")
		ada.Lore(dc.Dir, "show", "--id", curated).OK(t)
		if r := ada.Lore(dc.Dir, "show", "--id", copied); r.Code == 0 && !strings.Contains(r.Out(), "trash") {
			t.Errorf("the dropped copy still shows as live:\n%s", r.Out())
		}
		if r := ada.Lore(dc.Dir, "rule", "list", "--json"); strings.Count(r.Out(), "portless") != 1 {
			t.Errorf("want one portless rule after dropping the copy:\n%s", r.Out())
		}
	})
}
