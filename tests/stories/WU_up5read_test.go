//go:build stories && !windows

package stories

// WU_up5read_test.go — owner use pass 5 weak 6, 7, 8 (devco D-700, D-701,
// D-702): reads and errors that answered the wrong question.
//
//	D-700: context --topics counts exactly the rules lore context and rule
//	       list read, the org folder included: in a linked repo, from the
//	       home folder under --setup, and --topic finds an org folder's tag
//	D-701: policy show --setup from the home folder names each folder that
//	       sets a key with its own value, never one folder's value as the
//	       setup's
//	D-702: lore delete --id <memory id> is refused as an unknown command
//	       (not an unknown flag) and the guesses lead with lore memory delete

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

// Names of the up5read story (made up).
const (
	wu5Folder  = "web"
	wu5Folder2 = "api"
	wu5Key     = "context.budget.internal"
)

// wu5RuleCount finds the "N rules" counts of a --topics listing.
var wu5RuleCount = regexp.MustCompile(`(\d+) rules?\b`)

// wu5Rules sums the rule counts of a context --topics listing.
func wu5Rules(t *testing.T, out string) int {
	t.Helper()
	n := 0
	for _, m := range wu5RuleCount.FindAllStringSubmatch(out, -1) {
		v, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatal(err)
		}
		n += v
	}
	return n
}

// wu5RuleIDs counts the distinct rule ids a listing names.
func wu5RuleIDs(out string) int {
	seen := map[string]bool{}
	for _, id := range wubRuleID.FindAllString(out, -1) {
		seen[id] = true
	}
	return len(seen)
}

func TestStoryWUUp5Read(t *testing.T) {
	t.Parallel()
	s := &Story{World: journeys.NewWorld(t), ID: "WU"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote(wWebSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", "github.com/acme-co/*", "--yes").OK(t)
	m.Lore(pc.Dir, "store", "folder", "add", "team", wu5Folder).OK(t)
	m.Lore(pc.Dir, "store", "folder", "add", "team", wu5Folder2).OK(t)
	wc := m.Clone(web, "web")
	m.MoveClone(t, wc, filepath.Join(m.Home, "code", "web"))
	m.Lore(wc.Dir, "org", "link", "--org", wOrg, "--project", wu5Folder, "--own", "--yes").OK(t)
	inWeb, inAPI := wOrg+"/"+wu5Folder, wOrg+"/"+wu5Folder2
	m.Lore(wc.Dir, "tag", "add", "--name", "ops", "--to", wOrg+"/org", "--yes").OK(t)
	m.Lore(wc.Dir, "rule", "add", "--to", wOrg+"/org", "--severity", "must", "--body", "every company service logs in UTC", "--tag", "ops", "--yes").OK(t)
	m.Lore(wc.Dir, "rule", "add", "--to", inWeb, "--body", "the web app ships behind a flag", "--yes").OK(t)
	m.Lore(wc.Dir, "rule", "add", "--to", inWeb, "--body", "web forms validate on the server", "--yes").OK(t)

	s.Step("D-700/topics-count-what-context-reads", func() {
		// the matrix cell: in every placement --topics counts the rules
		// rule list there names, and the org folder's tag is a topic
		storeFolder := filepath.Join(m.Home, ".lore", "stores", "acme-co", "lore-team", wu5Folder)
		for _, cell := range []struct {
			name, dir string
			extra     []string
		}{
			{"linked own repo", wc.Dir, nil},
			{"store clone folder", storeFolder, nil},
			{"home folder --setup", m.Home, []string{"--setup", wSetup}},
		} {
			listed := wu5RuleIDs(m.Lore(cell.dir, append([]string{"rule", "list"}, cell.extra...)...).OK(t).Out())
			if listed < 3 {
				t.Errorf("devco:D-700 %s: rule list names %d rules, want the org rule and the two web rules", cell.name, listed)
			}
			topics := m.Lore(cell.dir, append([]string{"context", "--topics"}, cell.extra...)...).OK(t)
			topics.Says(t, "ops ")
			if got := wu5Rules(t, topics.Out()); got != listed {
				t.Errorf("devco:D-700 %s: context --topics counts %d rules, rule list names %d (the org folder left out?)\n%s", cell.name, got, listed, topics.Out())
			}
			// --topic reads the org folder's tag where --topics lists it
			m.Lore(cell.dir, append([]string{"context", "--topic", "ops"}, cell.extra...)...).OK(t).Says(t, "every company service logs in UTC")
		}
	})

	s.Step("D-701/policy-show-setup-names-each-folder", func() {
		m.Lore(m.Home, "policy", "set", "--key", wu5Key, "--value", "22000", "--to", inWeb, "--setup", wSetup, "--yes").OK(t)
		m.Lore(m.Home, "policy", "set", "--key", wu5Key, "--value", "23000", "--to", inAPI, "--setup", wSetup, "--yes").OK(t)
		out := m.Lore(m.Home, "policy", "show", "--id", wu5Key, "--setup", wSetup).OK(t).Out()
		for _, want := range []string{"22000\tfolder " + inWeb + " (pol_", "23000\tfolder " + inAPI + " (pol_"} {
			if !strings.Contains(out, want) {
				t.Errorf("policy show --setup: no %q line, each folder's own value:\n%s", want, out)
			}
		}
		// in the linked repo one folder is read: one line, that folder's
		m.Lore(wc.Dir, "policy", "show", "--id", wu5Key).OK(t).Says(t, "22000\tfolder "+inWeb).Never(t, inAPI, "every other folder")
	})

	s.Step("D-702/unknown-command-before-its-flag", func() {
		id := wuMemID.FindString(m.Lore(m.Home, "memory", "add", "--body", "the cache warms at boot", "--to", inWeb, "--setup", wSetup, "--yes").OK(t).Out())
		if id == "" {
			t.Fatal("memory add printed no id")
		}
		m.Lore(m.Home, "delete", "--id", id).Refused(t, `unknown command "delete" for "lore"`, "did you mean lore memory delete").Never(t, "unknown flag")
		m.Lore(m.Home, "delete").Refused(t, `unknown command "delete"`, "lore memory delete or lore rule delete")
	})
}
