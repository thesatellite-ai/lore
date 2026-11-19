//go:build stories && !windows

package stories

// WU_folderscope_test.go — story WU, devco D-583..D-585 (owner use pass 2):
// resources read through one folder of an org store, and the own/repos
// overlap of the org config.
//
//	day1: StartTeam (acme, store folders org + sync + tools), a code_repo
//	      kind defined in acme/org, three resources in acme/org (one with
//	      project = sync, one with project = tools, one org-wide) and one
//	      stored in acme/sync
//	D-583: `resource list|kinds|kind show|show --store acme/sync` from the
//	       home folder sees the kinds of acme/org and lists the resources
//	       of the folder (stored there, or in acme/org with project = sync),
//	       saying the org-wide ones are left out; the same from inside the
//	       sync folder of the store clone
//	D-584: the default view is KIND NAME PROJECT URL (CHECKED only when a
//	       row was checked); long text is cut like other lists
//	D-650: `resource list --kind server` (a kind with ten fields) keeps the
//	       narrow view; --wide prints every field
//	D-647: team list names the roster "the roster of acme (acme/org)",
//	       never "acme org/'s roster" (nor does member show of no one)
//	D-585: `org config get --org acme` and `store doctor` name an own
//	       pattern that overlaps exact repos entries, and that the exact
//	       entry wins

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

func TestStoryWUFolderScope(t *testing.T) {
	s := &Story{World: journeys.NewWorld(t), ID: "WU"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})

	inOrg, inSync, inTools := wOrg+"/org", wOrg+"/sync", wOrg+"/tools"
	long := strings.Repeat("a very long purpose text that goes on ", 12)
	setup := []string{"--setup", wSetup, "--yes"}
	lore := func(args ...string) { m.Lore(m.Home, append(args, setup...)...).OK(t) }
	lore("resource", "kind", "add", "--kind", "code_repo", "--to", inOrg)
	for _, f := range []string{"project", "url", "purpose"} {
		lore("resource", "field", "add", "--kind", "code_repo", "--field", f, "--type", "text", "--to", inOrg)
	}
	// tools first: the folder a bare "first other folder" pick would take
	lore("memory", "add", "--body", "tools folder", "--to", inTools)
	lore("resource", "add", "--kind", "code_repo", "--name", "sync_go", "--set", "project=sync", "--set", "url=github.com/acme-co/sync-go", "--set", "purpose="+long, "--to", inOrg)
	lore("resource", "add", "--kind", "code_repo", "--name", "tool_cli", "--set", "project=tools", "--set", "url=github.com/acme-co/tool-cli", "--to", inOrg)
	lore("resource", "add", "--kind", "code_repo", "--name", "shared_ci", "--set", "url=github.com/acme-co/ci", "--to", inOrg)
	lore("resource", "add", "--kind", "code_repo", "--name", "sync_web", "--set", "url=github.com/acme-co/sync-web", "--to", inSync)

	folderNote := "folder " + inSync + ": the resources stored there and those of " + inOrg + " whose project is sync; 2 more resources (org-wide or another folder's) are not listed: lore resource list --store " + wOrg
	s.Step("D-583/folder-store-sees-org-kinds-and-its-resources", func() {
		for _, dir := range []string{m.Home, pc.Dir} {
			r := m.Lore(dir, "resource", "list", "--store", inSync).OK(t)
			r.Says(t, "sync_go", "sync_web", folderNote).Never(t, "(no resources)", "tool_cli", "shared_ci")
			r = m.Lore(dir, "resource", "list", "--kind", "code_repo", "--store", inSync).OK(t)
			r.Says(t, "sync_go", "sync_web").Never(t, "E_KIND_UNKNOWN", "tool_cli", "shared_ci")
			m.Lore(dir, "resource", "kinds", "--store", inSync).OK(t).Says(t, "code_repo")
			m.Lore(dir, "resource", "kind", "show", "--id", "code_repo", "--store", inSync).OK(t).Says(t, "purpose")
			m.Lore(dir, "resource", "show", "--id", "sync_go", "--store", inSync).OK(t).Says(t, "github.com/acme-co/sync-go")
		}
		// the whole org still lists every resource
		m.Lore(m.Home, "resource", "list", "--store", wOrg).OK(t).Says(t, "sync_go", "tool_cli", "shared_ci", "sync_web").Never(t, folderNote)
	})

	s.Step("D-583/same-inside-the-folder-of-the-store-clone", func() {
		clone := storeCloneDir(t, m)
		r := m.Lore(filepath.Join(clone, "sync"), "resource", "list").OK(t)
		r.Says(t, "sync_go", "sync_web", folderNote).Never(t, "tool_cli", "shared_ci")
	})

	s.Step("D-584/narrow-default-view", func() {
		r := m.Lore(m.Home, "resource", "list", "--store", wOrg).OK(t)
		r.Says(t, "KIND", "NAME", "PROJECT", "URL", "github.com/acme-co/sync-go").Never(t, "CHECKED", "PURPOSE")
		r = m.Lore(m.Home, "resource", "list", "--kind", "code_repo", "--store", wOrg).OK(t)
		r.Says(t, "PURPOSE", "…").Never(t, long)
		for _, line := range strings.Split(r.Out(), "\n") {
			if len(line) > 200 {
				t.Fatalf("resource list --kind printed a %d-char line: %q", len(line), line)
			}
		}
		m.Lore(filepath.Join(storeCloneDir(t, m), "org"), "resource", "check", "--id", "sync_go", "--yes").OK(t)
		m.Lore(m.Home, "resource", "list", "--store", wOrg).OK(t).Says(t, "CHECKED")
	})

	s.Step("D-650/kind-list-is-narrow-by-default", func() {
		// a server kind with many fields: --kind keeps the narrow view
		// (KIND NAME, project/url, at most two key fields), --wide and
		// --json carry every field
		fields := []string{"project", "url", "ip", "access", "config_repo", "owner_team", "os", "region", "rack", "notes"}
		lore("resource", "kind", "add", "--kind", "server", "--to", inOrg)
		for _, f := range fields {
			lore("resource", "field", "add", "--kind", "server", "--field", f, "--type", "text", "--to", inOrg)
		}
		args := []string{"resource", "add", "--kind", "server", "--name", "web1", "--to", inOrg}
		for _, f := range fields {
			args = append(args, "--set", f+"="+strings.Repeat(f+"-value ", 3))
		}
		lore(args...)
		r := m.Lore(m.Home, "resource", "list", "--kind", "server", "--store", wOrg).OK(t)
		head := strings.Fields(strings.SplitN(r.Out(), "\n", 2)[0])
		if len(head) > 6 || head[2] != "PROJECT" || head[3] != "URL" {
			t.Fatalf("resource list --kind server head %v: want KIND NAME PROJECT URL and at most two more\n%s", head, r.Out())
		}
		r.Says(t, "web1", "more fields not shown: add --wide for every field, or --json")
		m.Lore(m.Home, "resource", "list", "--kind", "server", "--store", wOrg, "--wide").OK(t).
			Says(t, "OWNER_TEAM", "CONFIG_REPO", "RACK", "NOTES").Never(t, "not shown")
		m.Lore(m.Home, "resource", "list", "--kind", "server", "--store", wOrg, "--json").OK(t).Says(t, "rack-value")
	})

	s.Step("D-647/roster-named-by-org-entry-and-folder", func() {
		// "no teams on acme org/'s roster" glued a possessive onto a
		// folder path: the roster is "the roster of acme (acme/org)"
		m.Lore(m.Home, "member", "show", "--who", "nobody@example.test", "--setup", wSetup).Refused(t, "nobody@example.test").Never(t, "/'s roster")
		r := m.Lore(m.Home, "team", "list", "--setup", wSetup).OK(t).Never(t, "/'s roster")
		if strings.Contains(r.Out(), "no teams") {
			r.Says(t, "no teams on the roster of acme (acme/org)")
		}
	})

	s.Step("D-585/own-pattern-overlapping-repos-entries", func() {
		m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", "github.com/acme-co/*", "--yes").OK(t)
		m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "repos", "--add", "github.com/acme-co/partner-a", "--yes").OK(t)
		m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "repos", "--add", "github.com/acme-co/partner-b", "--yes").OK(t)
		want := "own github.com/acme-co/* overlaps 2 repos entries (github.com/acme-co/partner-a, github.com/acme-co/partner-b); repos entries win (client)"
		m.Lore(m.Home, "org", "config", "get", "--org", wOrg).OK(t).Says(t, want)
		m.Lore(m.Home, "org", "config", "get", "--org", wOrg, "--key", "stores").OK(t).Never(t, "overlaps")
		m.LoreIn(pc, "store", "doctor").Says(t, "org "+wOrg+": "+want)
	})
}

// storeCloneDir is the one org store clone under the machine's lore home.
func storeCloneDir(t *testing.T, m *journeys.Machine) string {
	t.Helper()
	got, _ := filepath.Glob(filepath.Join(m.Home, ".lore", "stores", "acme-co", "*"))
	if len(got) != 1 {
		t.Fatalf("want one store clone, got %v", got)
	}
	return got[0]
}
