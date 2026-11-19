//go:build stories && !windows

package stories

// WP_polish_test.go — story WP (tests/stories/WP-polish.md): the weak
// answers of the owner's second use pass in a repo linked to an org
// folder, each checked the way the owner met it (devco D-599..D-604).

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

// wpFolder is the store folder the web repo is linked to.
const wpFolder = "web"

// wpPushCounts reads the counts a push line prints.
var wpPushCounts = regexp.MustCompile(`commit [0-9a-f]+ to branch main \((\d+) commits?, (\d+) row files?\)`)

func TestStoryWPPolish(t *testing.T) {
	s := &Story{World: journeys.NewWorld(t), ID: "WP"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote(wWebSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", "github.com/acme-co/*", "--yes").OK(t)
	wc := m.Clone(web, "web")
	m.MoveClone(t, wc, filepath.Join(m.Home, "code", "web"))
	m.Lore(wc.Dir, "org", "link", "--org", wOrg, "--project", wpFolder, "--own", "--yes").OK(t)
	inWeb := wOrg + "/" + wpFolder

	s.Step("D-599/for-file-skips-generic-stems", func() {
		branch := wuMemID.FindString(m.Lore(wc.Dir, "memory", "add", "--body", "the release branch main is protected and needs two reviews").OK(t).Out())
		invoice := wuMemID.FindString(m.Lore(wc.Dir, "memory", "add", "--body", "invoice totals are rounded per line, never per invoice").OK(t).Out())
		m.Lore(wc.Dir, "context", "--for-file", "main.go").OK(t).Never(t, branch, "words main")
		m.Lore(wc.Dir, "context", "--for-file", "cmd/main.go").OK(t).Never(t, branch, "words main")
		// a meaningful word of the path still finds its note
		m.Lore(wc.Dir, "context", "--for-file", "billing/invoice.go").OK(t).Says(t, invoice, "words billing invoice")
	})

	s.Step("D-600/one-label-for-the-folder", func() {
		m.Lore(wc.Dir, "rule", "add", "--body", "deploys go through the pipeline").OK(t).
			Says(t, "committed locally to store "+inWeb, "(store "+inWeb+")").Never(t, "(store "+wOrg+")")
		m.Lore(wc.Dir, "sql", "--query", "select distinct place from rules_all").OK(t).Says(t, inWeb)
	})

	s.Step("D-601/one-push-line-with-git-counts", func() {
		m.Lore(wc.Dir, "memory", "add", "--body", "the org keeps one on-call rota", "--to", wOrg+"/org").OK(t)
		before := wpRevCount(t, store.Dir)
		r := m.Lore(wc.Dir, "sync").OK(t).Says(t, "lore: pushed store "+wOrg+" (the store clone: folders org, "+wpFolder+")").
			Never(t, "pushed store "+wOrg+"/org, ", "pushed store "+inWeb+", ")
		got := wpPushCounts.FindStringSubmatch(r.Out())
		if got == nil {
			t.Fatalf("the push line has no commit and row counts: %s", r.Out())
		}
		if sent := wpRevCount(t, store.Dir) - before; strconv.Itoa(sent) != got[1] {
			t.Errorf("the push line says %s commits, git rev-list says %d went out: %s", got[1], sent, r.Out())
		}
	})

	s.Step("D-603/policy-set-question-names-place-values-reach-command", func() {
		key := "context.budget.internal"
		// a linked repo holds no rows: the default --to repo is refused
		// before anything is asked, never a question whose --yes fails
		m.Lore(wc.Dir, "policy", "set", "--key", key, "--value", "22000").
			Refused(t, "E_STORE_READ_ONLY", "--to "+wOrg).Never(t, "E_NEEDS_CONFIRM")
		cmd := "lore policy set --key " + key + " --value 22000 --to " + wOrg + " --yes"
		m.Lore(wc.Dir, "policy", "set", "--key", key, "--value", "22000", "--to", wOrg).
			Refused(t, "E_NEEDS_CONFIRM", "in store "+inWeb+":", "(default) -> 22000", "every repo linked to "+inWeb, cmd)
		m.Lore(wc.Dir, strings.Fields(strings.TrimPrefix(cmd, "lore "))...).OK(t).Says(t, key+" = 22000")
	})

	s.Step("D-604/trash-list-store-names-the-deleter", func() {
		id := wuMemID.FindString(m.Lore(wc.Dir, "memory", "add", "--body", "a note to throw away").OK(t).Out())
		m.Lore(wc.Dir, "memory", "delete", "--id", id, "--yes").OK(t)
		m.Lore(m.Home, "trash", "list", "--store", inWeb).OK(t).Says(t, id, "by @").Never(t, "by act_")
	})
}

// wpRevCount is how many commits the bare store remote's main holds.
func wpRevCount(t *testing.T, bare string) int {
	t.Helper()
	out, err := exec.Command("git", "-C", bare, "rev-list", "--count", "main").Output()
	if err != nil {
		t.Fatalf("git rev-list in %s: %v", bare, err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("git rev-list printed %q", out)
	}
	return n
}
