//go:build stories && !windows

package stories

// O_leak_meta_date_test.go — devco:D-916 (public issue #138) on the
// owner-shaped machine (story O), replaying the issue's script. The org
// keeps its main store (internal: everyone in the org) and a second store
// shared with the client's team; the client's code repo is linked to a
// folder of the shared store. A memory in the main store's client_ops
// folder carries fact_verified_at=2026-11-22 (an auto-registered key:
// internal, type text).
//
//   - a code comment naming that plain date commits: a date is never an
//     exact-token leak, and the metadata values of a store folder the repo
//     is not linked to do not feed its guard;
//   - the memory's id from that folder still blocks (ids and 8-word text
//     of every internal store stay in the guard);
//   - a distinctive value (a host) kept as metadata in the personal store
//     still blocks by exact token; the refusal names the row id, and the
//     `lore leaks allow` line it prints, run as printed, lets it through.

import (
	"regexp"
	"strings"
	"testing"
)

// oMetaAllowRe is the `lore leaks allow` line a metadata finding prints.
var oMetaAllowRe = regexp.MustCompile(`lore leaks allow --note [a-z]+_[0-9a-f]+ --file \S+ --match-hash [0-9]+`)

func TestStoryOLeakGuardMetaDate(t *testing.T) {
	s := New(t, "O")
	team := s.Remote("github.com/" + oOwner + "/lore-team")
	shared := s.Remote("github.com/" + oOwner + "/lore-shared")
	platform := s.Seed(s.Remote("github.com/"+oOwner+"/platform"), func(dir string) { PlainRepo(t, dir) })
	const (
		internalFolder = "client_ops"
		sharedFolder   = "client_app"
		day            = "2026-11-22"
		host           = "vpn-7.internal.acme-co.example"
	)
	app := s.Seed(s.Remote("github.com/acme-client/client-app"), func(dir string) { PlainRepo(t, dir) })

	ada := s.OwnerShaped(OwnerShape{Personal: oAdminPersonal, Org: oAdminOrg, OrgFolder: oOrg, Alias: oAlias, OrgOwners: []string{oOwner}})
	pc := ada.CloneTo(platform, ada.OrgDir("platform"))
	oSetupFlow(t, ada, pc.Dir, oSetup, "admin", oAdminOrg, oAlias,
		map[string]any{"org": oOrg, "store_url": team.SSHURL()}, []string{"~/code/" + oOrg + "/**"})
	ac := ada.CloneTo(app, ada.OrgDir("client-app"))

	var mem string
	s.Step("D-916/a-dated-memory-in-an-unlinked-internal-folder", func() {
		ada.LoreIn(pc, "store", "folder", "add", "team", internalFolder).OK(t)
		ada.LoreIn(pc, "team", "add", "--handle", "@client", "--members", oAdminOrg.Email, "--yes").OK(t)
		ada.LoreIn(pc, "org", "store", "add", "--org", oOrg, "--url", shared.SSHURL(), "--audience", "team:@client", "--push", "--yes").OK(t)
		ada.LoreIn(pc, "store", "folder", "add", shared.SSHURL(), sharedFolder).OK(t)
		ada.LoreIn(pc, "sync").OK(t)
		ada.Lore(ac.Dir, "org", "link", "--org", oOrg, "--store", shared.SSHURL(), "--project", sharedFolder, "--yes").OK(t)
		mem = oAddedID(t, ada.Lore(ac.Dir, "memory", "add", "--body", "The billing service runs nightly at two.", "--meta", "fact_verified_at="+day,
			"--to", oOrg+"/"+internalFolder, "--yes", "--json").OK(t))
		if mem == "" {
			t.Fatal("memory add printed no id")
		}
	})

	s.Step("D-916/#138-a-plain-date-commits", func() {
		ac.Git("checkout", "-q", "-b", "feature/cleanup")
		ac.WriteFile("main.go", "package main\n\n// written before "+day+", when the retry loop moved to the worker\nfunc cleanup() {}\n")
		ac.Git("add", "main.go")
		r := ada.Lore(ac.Dir, "check", "leaks", "--explain")
		oCheck(t, "D-916/date-check-leaks-passes", r.Code == 0, "lore check leaks refused a plain date:\n%s", r)
		if r := ac.TryGit("commit", "-q", "-m", "Add cleanup"); r.Code != 0 {
			t.Fatalf("the commit naming a plain date was refused: %s", r)
		}
	})

	s.Step("D-916/the-unlinked-folder's-row-id-still-blocks", func() {
		ac.WriteFile("main.go", "package main\n\n// see "+mem+" for the schedule\n")
		ac.Git("add", "main.go")
		r := ada.Lore(ac.Dir, "check", "leaks")
		oCheck(t, "D-916/row-id-blocks", r.Code != 0 && strings.Contains(r.Out(), "names a row of store "+oOrg+"/"+internalFolder),
			"lore check leaks let the id of an internal row through:\n%s", r)
		ac.Git("checkout", "-q", "HEAD", "--", "main.go")
	})

	s.Step("D-916/a-host-in-personal-metadata-blocks-and-its-allow-clears-it", func() {
		pm := oAddedID(t, ada.Lore(ac.Dir, "memory", "add", "--to", "me", "--body", "The staging VPN host for the billing job.",
			"--meta", "vpn_host="+host, "--meta", "checked_on="+day, "--yes", "--json").OK(t))
		if pm == "" {
			t.Fatal("memory add --to me printed no id")
		}
		ac.WriteFile("main.go", "package main\n\n// connect through "+host+", checked "+day+"\nfunc retry() {}\n")
		ac.Git("add", "main.go")
		r := ada.Lore(ac.Dir, "check", "leaks", "--explain")
		oCheck(t, "D-916/host-blocks-naming-the-row", r.Code != 0 && strings.Contains(r.Out(), "[exact-token rule]") && strings.Contains(r.Out(), "of row "+pm),
			"lore check leaks let an internal host through, or did not name its row:\n%s", r)
		if strings.Contains(r.Out(), "key checked_on") {
			t.Fatalf("the date of checked_on was reported as an exact token:\n%s", r)
		}
		line := oMetaAllowRe.FindString(r.Out())
		if line == "" {
			t.Fatalf("the refusal printed no lore leaks allow line:\n%s", r)
		}
		args := append(strings.Fields(strings.TrimPrefix(line, "lore ")), "--reason", "the public status page names this host")
		ada.Lore(ac.Dir, args...).OK(t)
		r = ada.Lore(ac.Dir, "check", "leaks")
		oCheck(t, "D-916/allowed-host-passes", r.Code == 0 && strings.Contains(r.Out(), "allowed by sal_"),
			"the printed allow did not let the host through:\n%s", r)
		if r := ac.TryGit("commit", "-q", "-m", "Add retry"); r.Code != 0 {
			t.Fatalf("the commit with the allowed host was refused: %s", r)
		}
	})
}
