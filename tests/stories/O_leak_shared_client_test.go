//go:build stories && !windows

package stories

// O_leak_shared_client_test.go — devco:D-910 (public issues #133 and
// #135) on the owner-shaped machine (story O), replaying the issues'
// script. The org keeps two stores: its main store (internal: everyone
// in the org) and a second store shared with the client's team
// (`audience: team:@client`). The client's code repo is linked to a
// folder of the SHARED store.
//
//   - #133: a task of the shared store, its id and its text quoted in the
//     client repo, was refused as internal; the client's own team reads
//     that store, so the commit and the push go through.
//   - #135: a decision in the main store's client_ops folder (never
//     attached in that repo) committed with `lore check leaks` passing,
//     even after a second link to client_ops; every folder of every org
//     store whose audience is not the client's team is internal now.
//   - the same class in `lore check leaks --as-client` (a hand-over of an
//     own repo): every folder of the org's stores is read, not only the
//     folders the repo attaches.

import (
	"strings"
	"testing"
)

func TestStoryOLeakGuardSharedClientStore(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	shared := s.Remote("github.com/" + oOwner + "/lore-shared")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	const (
		internalFolder = "client_ops"
		sharedFolder   = "client_app"
		taskBody       = "Show the order received banner after the customer submits the checkout form on mobile."
		noteBody       = "The client is behind on two invoices so keep the banner work on hold for now."
	)
	app := s.Seed(s.Remote("github.com/acme-client/client-app"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	ac := ada.CloneTo(app, ada.OrgDir("client-app"))

	var task, dec string
	s.Step("D-910/a-client-repo-works-out-of-the-shared-store", func() {
		ada.LoreIn(pc, "store", "folder", "add", "team", internalFolder).OK(t)
		ada.LoreIn(pc, "team", "add", "--handle", "@client", "--members", oAdminOrg.Email, "--yes").OK(t)
		ada.LoreIn(pc, "org", "store", "add", "--org", oOrg, "--url", shared.SSHURL(), "--audience", "team:@client", "--push", "--yes").OK(t).Says(t, "team:@client")
		ada.LoreIn(pc, "store", "folder", "add", shared.SSHURL(), sharedFolder).OK(t)
		ada.LoreIn(pc, "sync").OK(t)
		ada.Lore(ac.Dir, "org", "link", "--org", oOrg, "--store", shared.SSHURL(), "--project", sharedFolder, "--yes").OK(t)
		task = oAddedID(t, ada.Lore(ac.Dir, "task", "add", "--title", "Banner after submit", "--body", taskBody, "--commitment", "accepted", "--json").OK(t))
		dec = oAddedID(t, ada.Lore(ac.Dir, "decision", "add", "--title", "Hold banner work", "--body", noteBody, "--to", oOrg+"/"+internalFolder, "--yes", "--json").OK(t))
		if task == "" || dec == "" {
			t.Fatalf("task add or decision add printed no id (task %q, decision %q)", task, dec)
		}
	})

	s.Step("D-910/#133-a-shared-store-row-commits-and-pushes", func() {
		ac.Git("checkout", "-q", "-b", "feature/banner")
		ac.WriteFile("main.go", "package main\n\n// "+task+": "+taskBody+"\nfunc banner() {}\n")
		ac.WriteFile("docs/banner.md", "# Banner\n\nTracked in "+task+".\n")
		ac.Git("add", "main.go", "docs/banner.md")
		r := ada.Lore(ac.Dir, "check", "leaks")
		oCheck(t, "D-910/shared-row-check-leaks-passes", r.Code == 0, "lore check leaks refused a row of the store shared with the client's team:\n%s", r)
		if r := ac.TryGit("commit", "-q", "-m", "Add the banner ("+task+")"); r.Code != 0 {
			t.Fatalf("the commit citing a shared-store task was refused: %s", r)
		}
		if r := ac.TryGit("push", "-q", "origin", "feature/banner"); r.Code != 0 {
			t.Fatalf("the push citing a shared-store task was refused: %s", r)
		}
	})

	s.Step("D-910/#135-an-internal-store-row-is-refused", func() {
		ac.WriteFile("main.go", "package main\n\n// "+dec+": "+noteBody+"\n")
		ac.Git("add", "main.go")
		r := ada.Lore(ac.Dir, "check", "leaks")
		oCheck(t, "D-910/internal-row-check-leaks-blocks", r.Code != 0 && strings.Contains(r.Out(), "E_LEAK_BLOCKED") && strings.Contains(r.Out(), "names a row of store "+oOrg+"/"+internalFolder),
			"lore check leaks let an internal decision about the client through:\n%s", r)
		r.Says(t, "matches internal note "+dec)
		if r := ac.TryGit("commit", "-q", "-m", "Add a note"); r.Code == 0 {
			t.Fatalf("the commit quoting an internal decision went through: %s", r)
		}
	})

	s.Step("D-910/#135-still-refused-with-a-second-link-to-the-internal-folder", func() {
		ada.Lore(ac.Dir, "org", "link", "--org", oOrg, "--store", team.SSHURL(), "--project", internalFolder, "--yes").OK(t)
		r := ada.Lore(ac.Dir, "check", "leaks")
		oCheck(t, "D-910/internal-row-blocks-with-two-links", r.Code != 0 && strings.Contains(r.Out(), "matches internal note "+dec),
			"with a second link to the internal folder, lore check leaks let the decision through:\n%s", r)
		if r := ac.TryGit("commit", "-q", "-m", "Add a note"); r.Code == 0 {
			t.Fatalf("with a second link, the commit quoting an internal decision went through: %s", r)
		}
		ac.Git("commit", "-q", "--no-verify", "-m", "Add a note")
		r = ac.TryGit("push", "-q", "origin", "feature/banner")
		if r.Code == 0 {
			t.Fatalf("the push of an internal decision went through: %s", r)
		}
		r.Says(t, "E_LEAK_BLOCKED")
	})

	s.Step("D-910/as-client-reads-every-folder-of-the-org-store", func() {
		// the hand-over check reads the tracked files as a client would:
		// main.go (committed above with --no-verify) quotes the decision
		// of client_ops, a folder this repo's index never attached
		r := ada.Lore(ac.Dir, "check", "leaks", "--as-client")
		oCheck(t, "D-910/as-client-blocks-unattached-internal-folder", r.Code != 0 && strings.Contains(r.Out(), "E_LEAK_BLOCKED") && strings.Contains(r.Out(), dec),
			"lore check leaks --as-client let an internal decision of another folder through:\n%s", r)
	})
}
