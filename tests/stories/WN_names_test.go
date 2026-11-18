//go:build stories && !windows

package stories

// WN_names_test.go — story WN (tests/stories/WN-names.md): the writes that
// name their row by name take --store <org>[/<folder>] (alias --to) from
// the home folder as reads do (devco D-615), and inside a code repo a
// --store naming a store or folder the repo does not attach is refused,
// never written elsewhere (devco D-616), and a --store naming an attached
// store keeps the command to it (devco D-617).
//
//	day1: StartTeam (acme), the kind code_repo with fields in acme/org, a
//	      meta key and a memory in acme/sync, a repo ~/code/web linked to
//	      acme/web
//	D-615: from the home folder: resource kind rename/edit, resource field
//	       rename/retype/remove, meta rename/edit/set, project archive and
//	       unarchive with --store (or --to); without it the hint names
//	       --store <org>
//	D-616: in ~/code/web: --store acme/tools (not attached) is refused
//	       naming what the repo attaches and the home-folder command; the
//	       row is unchanged; that command works from the home folder

import (
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestStoryWNNames(t *testing.T) {
	s := &Story{World: journeys.NewWorld(t), ID: "WN"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote(wWebSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})

	inOrg, inSync, inTools := wOrg+"/org", wOrg+"/sync", wOrg+"/tools"
	setup := []string{"--setup", wSetup, "--yes"}
	lore := func(args ...string) string { return m.Lore(m.Home, append(args, setup...)...).OK(t).Out() }
	lore("resource", "kind", "add", "--kind", "code_repo", "--to", inOrg)
	for _, f := range []string{"project", "url", "notes"} {
		lore("resource", "field", "add", "--kind", "code_repo", "--field", f, "--type", "text", "--to", inOrg)
	}
	lore("resource", "add", "--kind", "code_repo", "--name", "acme_app", "--set", "project=sync", "--set", "url=github.com/acme-co/sync-go", "--to", inOrg)
	lore("memory", "add", "--body", "tools folder", "--to", inTools)
	lore("meta", "add", "--name", "owner_team", "--type", "text", "--to", inSync)
	memID := wuMemID.FindString(lore("memory", "add", "--body", "a sync note", "--to", inSync))
	wc := m.Clone(web, "web")
	m.MoveClone(t, wc, filepath.Join(m.Home, "code", "web"))
	m.LoreIn(wc, "org", "link", "--org", wOrg, "--project", wFolder, "--yes").OK(t)

	s.Step("D-615/by-name-writes-take-the-store-from-home", func() {
		home := func(args ...string) teamharness.Result { return m.Lore(m.Home, append(args, "--yes")...) }
		home("resource", "kind", "rename", "--id", "code_repo", "--new-name", "code_repos", "--store", inOrg).OK(t).Never(t, "unknown flag")
		home("resource", "kind", "edit", "--id", "code_repos", "--description", "a code repo", "--to", inOrg).OK(t)
		home("resource", "field", "rename", "--kind", "code_repos", "--field", "url", "--new-name", "link", "--store", inOrg).OK(t)
		home("resource", "field", "retype", "--kind", "code_repos", "--field", "notes", "--type", "url", "--store", inOrg).OK(t)
		home("resource", "field", "remove", "--kind", "code_repos", "--field", "notes", "--store", wOrg).OK(t)
		m.Lore(m.Home, "resource", "kind", "show", "--id", "code_repos", "--store", inOrg).OK(t).Says(t, "a code repo", "link").Never(t, "notes")
		home("meta", "rename", "--id", "owner_team", "--new-name", "owning_team", "--store", inSync).OK(t)
		home("meta", "edit", "--id", "owning_team", "--strict", "--store", inSync).OK(t)
		home("meta", "set", "--id", memID, "--pair", "owning_team=core", "--store", inSync).OK(t)
		m.Lore(m.Home, "memory", "show", "--id", memID).OK(t).Says(t, "owning_team")
		home("project", "archive", "--id", "sync", "--store", inSync).OK(t)
		home("project", "unarchive", "--id", "sync", "--store", inSync).OK(t)
		// no store from the home folder: the hint names --store, never "takes no store flag"
		home("resource", "kind", "rename", "--id", "code_repos", "--new-name", "repos").Refused(t).Says(t, "--store <org>").Never(t, "takes no store flag", "does not apply")
		home("meta", "rename", "--id", "owning_team", "--new-name", "team_x").Refused(t).Says(t, "--store <org>").Never(t, "takes no store flag", "does not apply")
	})

	s.Step("D-616/in-a-repo-an-unattached-store-is-refused", func() {
		m.LoreIn(wc, "resource", "kind", "edit", "--id", "code_repos", "--description", "changed in web", "--store", inTools, "--yes").
			Refused(t, "E_NOT_FOUND", "--store "+inTools+" is not attached in this repo", "acme/web", "nothing was written",
				"cd ~ && lore resource kind edit --id code_repos --description 'changed in web' --store "+inTools)
		m.Lore(m.Home, "resource", "kind", "show", "--id", "code_repos", "--store", inOrg).OK(t).Says(t, "a code repo").Never(t, "changed in web")
		m.LoreIn(wc, "meta", "rename", "--id", "owning_team", "--new-name", "team_y", "--store", "nosuch", "--yes").
			Refused(t, "--store nosuch is not attached in this repo").Never(t, "renamed")
		// a store the repo attaches runs as before
		m.LoreIn(wc, "resource", "kind", "edit", "--id", "code_repos", "--description", "edited in web", "--store", wOrg, "--yes").OK(t)
		m.Lore(m.Home, "resource", "kind", "show", "--id", "code_repos", "--store", inOrg).OK(t).Says(t, "edited in web")
		// the repo's own folder named <org>/<folder> is attached (as "acme" at folder web)
		m.LoreIn(wc, "memory", "add", "--body", "a web note").OK(t)
		m.LoreIn(wc, "project", "archive", "--id", wFolder, "--store", wOrg+"/"+wFolder, "--yes").OK(t).Never(t, "not attached")
		m.LoreIn(wc, "project", "unarchive", "--id", wFolder, "--store", wOrg+"/"+wFolder, "--yes").OK(t)
	})

	s.Step("D-617/in-a-repo-an-attached-store-narrows-the-lookup", func() {
		inWeb := wOrg + "/" + wFolder
		// by name: the kind lives in acme/org, --store names acme/web
		m.LoreIn(wc, "resource", "kind", "edit", "--id", "code_repos", "--description", "web only", "--store", inWeb, "--yes").
			Refused(t, "E_NOT_FOUND", "is not in "+inWeb+"; it is in "+inOrg+" (use --store "+inOrg+")").Never(t, "updated")
		m.Lore(m.Home, "resource", "kind", "show", "--id", "code_repos", "--store", inOrg).OK(t).Says(t, "edited in web").Never(t, "web only")
		m.LoreIn(wc, "resource", "kind", "edit", "--id", "code_repos", "--description", "org edit", "--store", inOrg, "--yes").OK(t)
		// by id: a memory of acme/org, read and written with --store acme/web
		orgMem := wuMemID.FindString(m.LoreIn(wc, "memory", "add", "--body", "an org note", "--to", inOrg).OK(t).Out())
		m.LoreIn(wc, "memory", "show", "--id", orgMem, "--store", inWeb).
			Refused(t, "E_NOT_FOUND", " in "+inWeb+"; it is in "+inOrg, "--store "+inOrg).Never(t, "an org note")
		m.LoreIn(wc, "meta", "set", "--id", orgMem, "--pair", "owning_team=web", "--store", inWeb, "--yes").
			Refused(t, "is not in "+inWeb+"; it is in "+inOrg)
		m.LoreIn(wc, "memory", "show", "--id", orgMem, "--store", inOrg).OK(t).Says(t, "an org note").Never(t, "owning_team")
		// the org name covers every folder the repo attaches
		m.LoreIn(wc, "memory", "show", "--id", orgMem, "--store", wOrg).OK(t).Says(t, "an org note")
	})
}
