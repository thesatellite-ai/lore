//go:build stories && !windows

package stories

// WM_more_test.go — story WM (tests/stories/WM-more.md): the reads of one
// org from the home folder that still answered from one folder after use
// pass 2's fixes (devco D-586, D-606).
//
//	day1: StartTeam (acme, store folders org + sync + tools), a code_repo
//	      kind in acme/org, resources in acme/org (project = sync, project
//	      = tools, org-wide) and one stored in acme/sync; a memory of
//	      acme/sync deleted
//	D-606: `trash list --store acme` from the home folder lists the row
//	       trashed in acme/sync (--store <org> reads every folder)
//	D-586: `resource check|stale|report|graph --store acme[/sync]` from the
//	       home folder work like resource list: the whole org, or the
//	       folder (stored there + acme/org rows with project = sync); no
//	       answer says "--store does not apply"
//	D-618: `resource graph|impact` through acme/sync (from the home folder
//	       and inside the store clone's sync folder) walk the folder's
//	       resources: an org-wide one is named "outside this folder" and
//	       never walked, with the whole-org form of the read
//	D-621: graph and impact --json name each node's store

import (
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

func TestStoryWMMore(t *testing.T) {
	s := &Story{World: journeys.NewWorld(t), ID: "WM"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})

	inOrg, inSync, inTools := wOrg+"/org", wOrg+"/sync", wOrg+"/tools"
	setup := []string{"--setup", wSetup, "--yes"}
	lore := func(args ...string) string { return m.Lore(m.Home, append(args, setup...)...).OK(t).Out() }
	lore("resource", "kind", "add", "--kind", "code_repo", "--to", inOrg)
	for _, f := range []string{"project", "url"} {
		lore("resource", "field", "add", "--kind", "code_repo", "--field", f, "--type", "text", "--to", inOrg)
	}
	lore("resource", "field", "add", "--kind", "code_repo", "--field", "uses", "--type", "ref", "--ref-kind", "code_repo", "--to", inOrg)
	lore("memory", "add", "--body", "tools folder", "--to", inTools)
	lore("resource", "add", "--kind", "code_repo", "--name", "shared_ci", "--to", inOrg)
	lore("resource", "add", "--kind", "code_repo", "--name", "acme_app", "--set", "project=sync", "--set", "url=github.com/acme-co/sync-go", "--set", "uses=shared_ci", "--to", inOrg)
	lore("resource", "add", "--kind", "code_repo", "--name", "tool_cli", "--set", "project=tools", "--to", inOrg)
	lore("resource", "add", "--kind", "code_repo", "--name", "sync_web", "--set", "uses=acme_app", "--to", inSync)
	// org-wide, using a resource of the sync folder (graph --up, impact)
	lore("resource", "add", "--kind", "code_repo", "--name", "ops_dash", "--set", "uses=acme_app", "--to", inOrg)

	s.Step("D-606/trash-list-store-org-reads-every-folder", func() {
		id := wuMemID.FindString(lore("memory", "add", "--body", "a sync note to throw away", "--to", inSync))
		lore("memory", "delete", "--id", id)
		m.Lore(m.Home, "trash", "list", "--store", inSync).OK(t).Says(t, id)
		m.Lore(m.Home, "trash", "list", "--store", wOrg).OK(t).Says(t, id).Never(t, "the trash is empty")
	})

	s.Step("D-586/stale-report-graph-folder-narrowed", func() {
		m.Lore(m.Home, "resource", "stale", "--store", inSync).OK(t).Says(t, "sync_web", "lore resource stale --store "+wOrg).Never(t, "tool_cli", "shared_ci")
		m.Lore(m.Home, "resource", "stale", "--store", wOrg).OK(t).Says(t, "tool_cli", "sync_web")
		m.Lore(m.Home, "resource", "report", "--store", inSync).OK(t).Says(t, "sync_web", "lore resource report --store "+wOrg).Never(t, "tool_cli", "shared_ci")
		m.Lore(m.Home, "resource", "graph", "--id", "acme_app", "--store", inSync).OK(t).Says(t, "acme_app")
		m.Lore(m.Home, "resource", "graph", "--id", "tool_cli", "--store", inSync).Refused(t, "lore resource graph --id tool_cli --store "+wOrg).Never(t, "does not apply")
		m.Lore(m.Home, "resource", "graph", "--id", "tool_cli", "--store", wOrg).OK(t).Says(t, "tool_cli")
	})
	s.Step("D-618/graph-impact-walk-the-folder", func() {
		outside := "shared_ci (" + inOrg + ", outside this folder)"
		// down: the org-wide resource it uses is named, marked, never walked
		m.Lore(m.Home, "resource", "graph", "--id", "sync_web", "--store", inSync).OK(t).
			Says(t, "acme_app", outside, "lore resource graph --id sync_web --store "+wOrg)
		m.Lore(m.Home, "resource", "graph", "--id", "sync_web", "--store", wOrg).OK(t).Says(t, "shared_ci").Never(t, "outside this folder")
		// up and impact: the org-wide user of acme_app is left out of the folder's walk
		m.Lore(m.Home, "resource", "graph", "--id", "acme_app", "--up", "--store", inSync).OK(t).Says(t, "sync_web").Never(t, "ops_dash")
		m.Lore(m.Home, "resource", "impact", "--id", "acme_app", "--store", inSync).OK(t).
			Says(t, "sync_web", "lore resource impact --id acme_app --store "+wOrg).Never(t, "ops_dash")
		m.Lore(m.Home, "resource", "impact", "--id", "acme_app", "--store", wOrg).OK(t).Says(t, "sync_web", "ops_dash")
		// a resource of another folder is refused naming the whole-org form, as graph is
		m.Lore(m.Home, "resource", "impact", "--id", "tool_cli", "--store", inSync).
			Refused(t, "E_NOT_FOUND", "lore resource impact --id tool_cli --store "+wOrg)
		// the same inside the sync folder of the store clone
		in := filepath.Join(storeCloneDir(t, m), "sync")
		m.Lore(in, "resource", "graph", "--id", "sync_web").OK(t).Says(t, outside)
		m.Lore(in, "resource", "impact", "--id", "acme_app").OK(t).Says(t, "sync_web").Never(t, "ops_dash")
		m.Lore(in, "resource", "stale").OK(t).Says(t, "sync_web", "lore resource stale --store "+wOrg).Never(t, "tool_cli", "shared_ci")
		m.Lore(in, "resource", "report").OK(t).Says(t, "sync_web", "lore resource report --store "+wOrg).Never(t, "tool_cli", "shared_ci")
	})

	s.Step("D-621/routed-json-names-each-rows-place", func() {
		// graph and impact nodes name their store, as resource list rows do
		g := m.Lore(m.Home, "resource", "graph", "--id", "sync_web", "--store", inSync, "--json").OK(t)
		g.Says(t, `"store": "`+inOrg+`"`, `"outside": true`)
		m.Lore(m.Home, "resource", "impact", "--id", "acme_app", "--store", inSync, "--json").OK(t).Says(t, `"store": "`+inSync+`"`)
		// every other read routed to a folder already names it per row
		id := wuMemID.FindString(lore("memory", "add", "--body", "a sync note kept", "--to", inSync))
		label := `"_label": "[` + inSync + `]"`
		m.Lore(m.Home, "memory", "list", "--store", inSync, "--json").OK(t).Says(t, label)
		m.Lore(m.Home, "memory", "show", "--id", id, "--json").OK(t).Says(t, label)
		m.Lore(m.Home, "search", "--query", "kept", "--store", inSync, "--json").OK(t).Says(t, `"folder": "sync"`)
		m.Lore(m.Home, "resource", "list", "--store", inSync, "--json").OK(t).Says(t, `"store": "`+inSync+`"`, `"store": "`+inOrg+`"`)
	})

	s.Step("D-586/resource-check-takes-the-store", func() {
		m.Lore(m.Home, "resource", "check", "--id", "acme_app", "--store", inSync, "--yes").OK(t).Never(t, "does not apply")
		m.Lore(m.Home, "resource", "check", "--id", "shared_ci", "--store", wOrg, "--yes").OK(t).Never(t, "does not apply")
		// a row stored in a folder, found and written there through the whole org
		m.Lore(m.Home, "resource", "check", "--id", "sync_web", "--store", wOrg, "--yes").OK(t)
		m.Lore(m.Home, "resource", "stale", "--store", inSync).OK(t).Never(t, "sync_web", "acme_app").Says(t, "(no resources)")
		m.Lore(m.Home, "resource", "list", "--store", wOrg).OK(t).Says(t, "CHECKED")
		// a resource of another folder is refused naming the whole-org form
		m.Lore(m.Home, "resource", "check", "--id", "tool_cli", "--store", inSync, "--yes").
			Refused(t, "E_NOT_FOUND", "lore resource check --id tool_cli --store "+wOrg).Never(t, "does not apply")
		// without a store from the home folder the hint names --store, never "does not apply"
		m.Lore(m.Home, "resource", "check", "--id", "acme_app", "--yes").Refused(t).Says(t, "--store <org>").Never(t, "does not apply")
		m.Lore(m.Home, "resource", "check", "--id", "acme_app", "--setup", wSetup, "--yes").Never(t, "does not apply")
	})

}
