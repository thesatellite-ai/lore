//go:build stories && !windows

package stories

// WU_up5whole_test.go — owner 2026-11-20 P-8 (devco D-758): reading a
// personal knowledge base took a second command per answer, because
//
//	search printed a ~100-character snippet and had no way to print the
//	       whole note: --full prints each hit's whole body under its line
//	       (--json adds it as text)
//	sql    cut every table cell at 60 characters: --wide prints them whole
//	       and a cut table says so (--json and --format csv|json were
//	       already whole)
//	sql    res_<kind>_all listed only the places that also hold the kind:
//	       a resource in a store whose kind lives in the repo was missing,
//	       though res__all and resource list named it
//
// One person, one plain repo, a personal store and a second store, both on
// local bare remotes.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

// Names of the whole-text story (made up).
const (
	wu5wMeSlug    = "github.com/pat-doe/lore-me"
	wu5wInfraSlug = "github.com/acme-co/lore-infra"
	wu5wStore     = "infra"
	wu5wTail      = "then parks the event in the dead letter queue where the on-call engineer replays it after checking the ledger totals"
	wu5wBody      = "The billing service retries webhooks five times with exponential backoff starting at two seconds, " + wu5wTail
)

func TestStoryWUUp5WholeText(t *testing.T) {
	s := New(t, "WU")
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	me, infra := s.Remote(wu5wMeSlug), s.Remote(wu5wInfraSlug)
	m := s.Person("Pat Doe", "pat@example.test")
	c := m.Clone(app, "platform")
	m.LoreIn(c, "init").OK(t)
	m.LoreIn(c, "store", "init", "--name", "me", "--url", me.SSHURL(), "--yes").OK(t)
	m.LoreIn(c, "store", "init", "--name", wu5wStore, "--url", infra.SSHURL(), "--yes").OK(t)
	m.LoreIn(c, "memory", "add", "--body", wu5wBody, "--to", "me", "--yes").OK(t)
	m.LoreIn(c, "resource", "kind", "add", "--kind", "server", "--yes").OK(t)
	m.LoreIn(c, "resource", "field", "add", "--kind", "server", "--field", "ip", "--type", "text", "--yes").OK(t)
	m.LoreIn(c, "resource", "add", "--kind", "server", "--name", "db_one", "--set", "ip=10.0.0.9", "--yes").OK(t)
	m.LoreIn(c, "resource", "add", "--kind", "server", "--name", "api_two", "--set", "ip=10.0.0.5", "--to", wu5wStore, "--yes").OK(t)

	s.Step("D-758/search-full-prints-the-whole-note", func() {
		plain := m.LoreIn(c, "search", "--query", "ledger totals").OK(t)
		if strings.Contains(plain.Out(), wu5wBody) {
			t.Errorf("[WU D-758] search without --full printed the whole body:\n%s", plain)
		}
		full := m.LoreIn(c, "search", "--query", "ledger totals", "--full").OK(t)
		if !strings.Contains(full.Out(), "    "+wu5wBody+"\n") {
			t.Errorf("[WU D-758] search --full does not print the whole note under its hit:\n%s", full)
		}
		js := m.LoreIn(c, "search", "--query", "ledger totals", "--full", "--json").OK(t)
		if !strings.Contains(js.Out(), `"text": "`+wu5wBody+`"`) {
			t.Errorf("[WU D-758] search --full --json has no whole text:\n%s", js)
		}
	})

	s.Step("D-758/sql-wide-prints-whole-cells", func() {
		q := "SELECT source, body FROM memories_all"
		cut := m.LoreIn(c, "sql", "--query", q).OK(t)
		if strings.Contains(cut.Out(), wu5wTail) || !strings.Contains(cut.Out(), "--wide") {
			t.Errorf("[WU D-758] sql cut a cell without naming --wide:\n%s", cut)
		}
		wide := m.LoreIn(c, "sql", "--query", q, "--wide").OK(t)
		if !strings.Contains(wide.Out(), wu5wBody) {
			t.Errorf("[WU D-758] sql --wide cut the body:\n%s", wide)
		}
		for _, f := range []string{"csv", "json"} {
			if r := m.LoreIn(c, "sql", "--query", q, "--format", f).OK(t); !strings.Contains(r.Out(), wu5wBody) {
				t.Errorf("[WU D-758] sql --format %s cut the body:\n%s", f, r)
			}
		}
	})

	s.Step("D-758/res-kind-view-lists-every-place", func() {
		list := m.LoreIn(c, "resource", "list", "--kind", "server").OK(t)
		r := m.LoreIn(c, "sql", "--query", "SELECT source, name, ip FROM res_server_all ORDER BY name").OK(t)
		for _, want := range []string{"api_two", "db_one"} {
			if !strings.Contains(list.Out(), want) || !strings.Contains(r.Out(), want) {
				t.Errorf("[WU D-758] res_server_all and resource list differ on %s:\nlist:\n%s\nsql:\n%s", want, list, r)
			}
		}
		if !strings.Contains(r.Out(), "10.0.0.5") {
			t.Errorf("[WU D-758] res_server_all has no ip for the store's server:\n%s", r)
		}
	})
}

// TestStoryWUUp5WholeTextOrg is the org-shaped cell of D-758's view: the
// kind lives in the org folder and the servers in two project folders, as
// a company keeps them. res_server_all lists the servers resource list
// lists: the linked folder's in a repo linked to it (--own), every
// folder's with --store <org> from the home folder.
func TestStoryWUUp5WholeTextOrg(t *testing.T) {
	s := New(t, "WU")
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote(wWebSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", "github.com/acme-co/*", "--yes").OK(t)
	for _, f := range []string{wu5Folder, wu5Folder2} {
		m.Lore(pc.Dir, "store", "folder", "add", "team", f).OK(t)
	}
	org := wOrg + "/org"
	m.Lore(pc.Dir, "resource", "kind", "add", "--kind", "server", "--to", org, "--yes").OK(t)
	m.Lore(pc.Dir, "resource", "field", "add", "--kind", "server", "--field", "ip", "--type", "text", "--to", org, "--yes").OK(t)
	m.Lore(pc.Dir, "resource", "add", "--kind", "server", "--name", "web_one", "--set", "ip=10.0.1.1", "--to", wOrg+"/"+wu5Folder, "--yes").OK(t)
	m.Lore(pc.Dir, "resource", "add", "--kind", "server", "--name", "api_one", "--set", "ip=10.0.2.1", "--to", wOrg+"/"+wu5Folder2, "--yes").OK(t)
	wc := m.Clone(web, "web")
	m.MoveClone(t, wc, filepath.Join(m.Home, "code", "web"))
	m.Lore(wc.Dir, "org", "link", "--org", wOrg, "--project", wu5Folder, "--own", "--yes").OK(t)

	s.Step("D-758/res-kind-view-org-folders", func() {
		q := "SELECT name, ip FROM res_server_all ORDER BY name"
		for _, cell := range []struct {
			name, dir string
			want      []string
			extra     []string
		}{
			{"linked-repo", wc.Dir, []string{"web_one", "10.0.1.1"}, nil},
			{"home-store-org", m.Home, []string{"web_one", "10.0.1.1", "api_one", "10.0.2.1"}, []string{"--store", wOrg}},
		} {
			r := m.Lore(cell.dir, append([]string{"sql", "--query", q}, cell.extra...)...)
			list := m.Lore(cell.dir, append([]string{"resource", "list", "--kind", "server"}, cell.extra...)...)
			for _, w := range cell.want {
				if r.Code != 0 || !strings.Contains(r.Out(), w) {
					t.Errorf("[WU D-758 %s] res_server_all does not list %s (resource list there:\n%s):\n%s", cell.name, w, list, r)
				}
			}
		}
	})
}
