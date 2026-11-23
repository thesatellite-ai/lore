//go:build stories && !windows

package stories

// O_neardup_lint_test.go — devco:D-926 (owner 440-import item 6, bug hunt
// H-184) on the owner-shaped machine (story O): the owner imported a
// project's notes, rewrote them a month later and left the import live, so
// about 120 rows existed twice and every pinned rule was rendered twice;
// lore lint said "no findings" and doctor said nothing. Now plain lint
// lists each pair on the older row with the command that archives it,
// doctor counts the pairs and names the lint command, render pins the
// newer row once and says how many it left out, and every printed command
// runs where printed.

import (
	"regexp"
	"testing"
)

func TestStoryONearDuplicatesImportThenRewrite(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	var oldRule, newRule, oldFix, newFix, oldMem, newMem string

	s.Step("D-926/import-then-rewrite-both-live", func() {
		// the repo keeps its notes in a folder of the org store (no .lore folder)
		ada.Lore(pc.Dir, "store", "folder", "add", "--store", oOrg, "--folder", "billing").OK(t)
		ada.Lore(pc.Dir, "org", "config", "set", "--org", oOrg, "--key", "own", "--add", "github.com/"+oOwner+"/*", "--yes").OK(t)
		ada.Lore(pc.Dir, "org", "link", "--org", oOrg, "--project", "billing", "--own", "--yes").OK(t)
		oldRule = oAddedID(t, ada.Lore(pc.Dir, "rule", "add", "--severity", "must", "--source", "imported", "--source-ref", "notes/import.md:12",
			"--body", "Money amounts are stored as integer cents and never as floats in any service or table.", "--json").OK(t))
		oldFix = oAddedID(t, ada.Lore(pc.Dir, "hotfix", "add", "--severity", "high", "--title", "Staging deploy needs a cache flush",
			"--body", "After a staging deploy run the cache flush job, or old prices show for an hour.", "--json").OK(t))
		oldMem = oAddedID(t, ada.Lore(pc.Dir, "memory", "add", "--body", "The invoice export runs at two at night and takes about forty minutes on the big accounts.", "--json").OK(t))
		// the rewrite: new wording, the import left live
		newRule = oAddedID(t, ada.Lore(pc.Dir, "rule", "add", "--severity", "must", "--source-ref", oldRule,
			"--body", "Store every money amount as integer cents in every service and table; never as floats.", "--json").OK(t))
		newFix = oAddedID(t, ada.Lore(pc.Dir, "hotfix", "add", "--severity", "high", "--title", "Staging deploy needs a cache flush",
			"--body", "Flush the cache after each staging deploy (the flush job), otherwise prices are stale for an hour.", "--json").OK(t))
		newMem = oAddedID(t, ada.Lore(pc.Dir, "memory", "add", "--body", "The invoice export runs at two at night and takes about forty minutes for the big accounts.", "--json").OK(t))
		for _, id := range []string{oldRule, newRule, oldFix, newFix, oldMem, newMem} {
			if id == "" {
				t.Fatal("an add --json printed no id")
			}
		}
	})

	s.Step("D-926/render-pins-each-once", func() {
		ada.Lore(pc.Dir, "render").OK(t).Says(t, "2 pinned rows left out", "lore lint --code near_duplicate")
		// the session's pinned rows come from that render: one block each
		out := ada.Lore(pc.Dir, "context", "--for-prompt", "money amounts staging deploy").OK(t).Stdout
		if n := regexp.MustCompile(`integer cents`).FindAllStringIndex(out, -1); len(n) > 1 {
			t.Errorf("the rule is in the context %d times:\n%s", len(n), out)
		}
	})

	s.Step("D-926/lint-and-doctor-name-each-pair", func() {
		ada.Lore(pc.Dir, "lint").OK(t).Says(t, "3 finding(s)",
			"near_duplicate "+oldRule, "near duplicate of the newer "+newRule, "rewrite of", "lore rule archive --id "+oldRule,
			"near_duplicate "+oldFix, "same title", "lore hotfix archive --id "+oldFix,
			"near_duplicate "+oldMem, "similar body", "lore memory archive --id "+oldMem)
		ada.Lore(pc.Dir, "lint", "--context", "--code", "near_duplicate").OK(t).Says(t, "lore rule archive --id "+oldRule, "lore hotfix archive --id "+oldFix)
		ada.Lore(pc.Dir, "doctor").Says(t, "near_duplicate: 3 pair(s)", "lore lint --code near_duplicate")
	})

	s.Step("D-926/printed-commands-run-where-printed", func() {
		for _, id := range []string{oldRule, newRule} {
			ada.Lore(pc.Dir, "show", "--id", id).OK(t)
		}
		ada.Lore(pc.Dir, "lint", "--code", "near_duplicate").OK(t).Says(t, "3 finding(s)")
		ada.Lore(pc.Dir, "rule", "archive", "--id", oldRule).OK(t)
		ada.Lore(pc.Dir, "hotfix", "archive", "--id", oldFix).OK(t)
		ada.Lore(pc.Dir, "memory", "archive", "--id", oldMem).OK(t)
		ada.Lore(pc.Dir, "lint", "--code", "near_duplicate").OK(t).Says(t, "lint: no findings")
		ada.Lore(pc.Dir, "render").OK(t).Never(t, "pinned rows left out")
	})
}
