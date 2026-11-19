//go:build stories && !windows

package stories

// WUL_up4label_test.go — owner use pass 4 weak 1, 3, 6 and 7 (devco D-685,
// D-688, D-689, D-690): the names and ids people read, on two own repos
// each linked to its own folder of one org store.
//
//	D-685: `lore sql` gives one folder one `source` name in every placement
//	       (the linked repo said "<org>", the home folder "<org>/<folder>");
//	       every store commit subject is the command and the id, never a
//	       row's title
//	D-689: `policy set` of one key in two linked folders gives two ids, and
//	       the result line names the folder
//	D-690: `--to me` with no personal store offers only targets that work
//	       in a linked own repo (never --to repo), first one first
//	D-688: doctor's Team versions never print a bare act_ id

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Names of the label story (made up).
const (
	wulAPISlug  = "github.com/acme-co/api"
	wulWeb      = "web"
	wulAPI      = "api"
	wulKey      = "trash.retention_days"
	wulTitle    = "Question: renewal price for acme-co"
	wulTitleBit = "renewal price"
)

// wulPolID finds a full policy id.
var wulPolID = regexp.MustCompile(`pol_[0-9a-f]{32}`)

// wulBareActor is a Team versions line that starts with a bare actor id.
var wulBareActor = regexp.MustCompile(`(?m)^\s*act_[0-9a-f]+ on `)

func TestStoryWULUp4Labels(t *testing.T) {
	t.Parallel()
	s := &Story{World: journeys.NewWorld(t), ID: "WUL"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote(wWebSlug), func(dir string) { PlainRepo(t, dir) })
	api := s.Seed(s.Remote(wulAPISlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", "github.com/acme-co/*", "--yes").OK(t)
	setup := []string{"--setup", wSetup}
	inWeb, inAPI := wOrg+"/"+wulWeb, wOrg+"/"+wulAPI
	link := func(rem *teamharness.Remote, folder string) string {
		m.Lore(m.Home, append([]string{"store", "folder", "add", "--store", wOrg, "--folder", folder}, setup...)...).OK(t)
		c := m.Clone(rem, folder)
		m.MoveClone(t, c, filepath.Join(m.Home, "code", folder))
		m.Lore(c.Dir, "org", "link", "--org", wOrg, "--project", folder, "--own", "--yes").OK(t)
		return c.Dir
	}
	wd := link(web, wulWeb)
	ad := link(api, wulAPI)

	s.Step("D-685/sql-source-is-the-folder-in-every-placement", func() {
		m.Lore(wd, "memory", "add", "--body", "the web app keeps sessions for a day", "--yes").OK(t)
		q := "SELECT DISTINCT source FROM memories_all WHERE source <> 'repo'"
		linked := m.Lore(wd, "sql", "run", "--query", q, "--format", "csv").OK(t).Out()
		home := m.Lore(m.Home, append([]string{"sql", "run", "--store", inWeb, "--query", q, "--format", "csv"}, setup...)...).OK(t).Out()
		for where, out := range map[string]string{"linked repo": linked, "home folder": home} {
			if !strings.Contains(out, inWeb) {
				t.Errorf("devco:D-685: sql source in the %s does not name %s:\n%s", where, inWeb, out)
			}
			for _, line := range strings.Split(out, "\n") {
				if strings.TrimSpace(line) == wOrg {
					t.Errorf("devco:D-685: sql source in the %s is the bare org %q:\n%s", where, wOrg, out)
				}
			}
		}
	})

	s.Step("D-685/store-subjects-carry-no-title", func() {
		m.Lore(wd, "task", "add", "--title", wulTitle, "--yes").OK(t)
		m.Lore(wd, "sync").OK(t)
		for _, subj := range store.Log("main") {
			if strings.Contains(subj, wulTitleBit) {
				t.Errorf("devco:D-685: a store commit subject carries a row title: %q", subj)
				break
			}
		}
	})

	s.Step("D-689/policy-ids-unique-per-folder", func() {
		w := m.Lore(wd, "policy", "set", "--key", wulKey, "--value", "365", "--to", inWeb, "--yes").OK(t)
		if !strings.Contains(w.Out(), "("+inWeb+", pol_") {
			t.Errorf("devco:D-689: the result line does not name the folder %s:\n%s", inWeb, w)
		}
		m.Lore(ad, "policy", "set", "--key", wulKey, "--value", "365", "--to", inAPI, "--yes").OK(t)
		out := m.Lore(m.Home, append([]string{"sql", "run", "--store", wOrg, "--query", "SELECT source, id FROM policies_all WHERE key = '" + wulKey + "'", "--format", "csv"}, setup...)...).OK(t).Out()
		got := wulPolID.FindAllString(out, -1)
		if len(got) != 2 || got[0] == got[1] {
			t.Errorf("devco:D-689: one key in two linked folders gave ids %v:\n%s", got, out)
		}
		if !strings.Contains(out, inWeb) || !strings.Contains(out, inAPI) {
			t.Errorf("devco:D-689: the policy rows do not name both folders:\n%s", out)
		}
	})

	s.Step("D-690/to-me-hint-offers-what-works-here", func() {
		m.Lore(m.Home, "setup", "edit", "--name", wSetup, "--personal-store", "none", "--yes").OK(t)
		r := m.Lore(wd, "memory", "add", "--body", "a draft for me", "--to", "me", "--yes")
		if r.Code == 0 {
			t.Fatalf("devco:D-690: --to me with no personal store wrote:\n%s", r)
		}
		if strings.Contains(r.Out(), "--to repo") || !strings.Contains(r.Out(), "--to "+inWeb) {
			t.Fatalf("devco:D-690: the refusal in a linked own repo must offer --to %s and never --to repo:\n%s", inWeb, r)
		}
		// the offered target works exactly as printed
		m.Lore(wd, "memory", "add", "--body", "a draft for the folder", "--to", inWeb, "--yes").OK(t)
	})

	s.Step("D-688/team-versions-print-names", func() {
		out := m.Lore(wd, "doctor").Out()
		if wulBareActor.MatchString(out) {
			t.Fatalf("devco:D-688: doctor prints a bare actor id in Team versions:\n%s", out)
		}
	})
}
