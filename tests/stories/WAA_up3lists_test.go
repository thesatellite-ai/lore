//go:build stories && !windows

package stories

// WAA_up3lists_test.go — story WAA, devco D-631..D-635 (owner use pass 3):
// an org admin with one setup works from the home folder, where no repo
// and no store clone answers, so the setup names the stores.
//
//	day1: StartTeam in platform; a playbook, a task and a rule in acme/sync
//	D-631: every row kind's list (and show, search) under --setup, and plain
//	       in the home folder (the default setup), lists the setup's rows
//	D-632: context --topics / --topic / --for-prompt honour --setup
//	D-633: store doctor takes --store <org>
//	D-634: a malformed id says it is no lore id, before any store lookup

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/wire"
)

// waaID is any lore row id in a command's output.
var waaID = regexp.MustCompile(`\b[a-z]{3}_[0-9a-f]{32}\b`)

func TestStoryWAAUp3Lists(t *testing.T) {
	s := &Story{World: journeys.NewWorld(t), ID: "WAA"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	inSync := wOrg + "/sync"
	setup := []string{"--setup", wSetup}
	m.Lore(m.Home, "store", "folder", "add", "--store", wOrg, "--folder", "sync", "--setup", wSetup).OK(t)
	pb := m.Lore(m.Home, "playbook", "add", "--title", "release checklist", "--body", "tag, build, ship", "--to", inSync, "--setup", wSetup, "--yes").OK(t)
	pbID := waaID.FindString(pb.Out())
	m.Lore(m.Home, "tag", "add", "--name", "deploy", "--to", inSync, "--setup", wSetup, "--yes").OK(t)
	m.Lore(m.Home, "rule", "add", "--body", "deploy only on weekdays", "--tag", "deploy", "--to", inSync, "--setup", wSetup, "--yes").OK(t)
	if pbID == "" {
		t.Fatalf("playbook add printed no id: %s", pb.Out())
	}

	s.Step("D-631/every-kind-list-under-a-setup-from-home", func() {
		lists := waaKindLists(t)
		for _, path := range lists {
			for _, args := range [][]string{append(append([]string{}, path...), setup...)} {
				r := m.Lore(m.Home, args...)
				if r.Code != 0 || strings.Contains(r.Out(), "no store named repo") {
					t.Errorf("devco:D-631 `lore %s` in the home folder:\n%s", strings.Join(args, " "), r)
				}
			}
		}
		m.Lore(m.Home, "playbook", "list", "--setup", wSetup).OK(t).Says(t, "release checklist")
		// a folder the setup covers (~/code/**) that is no repo: the plain
		// list reads the default setup's stores
		m.Lore(filepath.Join(m.Home, "code"), "playbook", "list").OK(t).Says(t, "release checklist")
		m.Lore(m.Home, "playbook", "show", "--id", pbID, "--setup", wSetup).OK(t).Says(t, "release checklist")
		m.Lore(m.Home, "playbook", "search", "--query", "checklist", "--setup", wSetup).OK(t).Says(t, "release checklist")
	})

	s.Step("D-632/context-honours-setup", func() {
		m.Lore(m.Home, "context", "--topics", "--setup", wSetup).OK(t).Says(t, "deploy")
		m.Lore(m.Home, "context", "--topic", "deploy", "--setup", wSetup).OK(t).Says(t, "deploy only on weekdays")
		m.Lore(m.Home, "context", "--for-prompt", "deploy the app", "--setup", wSetup).OK(t).Says(t, "deploy only on weekdays")
		// the refusal without --setup names it, and the named command works
		r := m.Lore(m.Home, "context", "--topics")
		wyFollow(t, m, m.Home, r, "devco:D-632 context@home")
	})

	s.Step("D-633/store-doctor-takes-store", func() {
		r := m.Lore(m.Home, "store", "doctor", "--store", wOrg)
		if strings.Contains(r.Out(), "unknown flag") {
			t.Errorf("devco:D-633 store doctor --store: %s", r)
		}
		m.Lore(m.Home, "store", "doctor", "--store", wOrg).Never(t, "unknown flag", "not inside a git work tree")
	})

	s.Step("D-634/malformed-id-is-no-lore-id", func() {
		for _, args := range [][]string{{"show", "--id", "mem_bogus"}, {"memory", "show", "--id", "mem_bogus"}, {"show", "--id", "mem_bogus", "--setup", wSetup}} {
			m.Lore(m.Home, args...).Fails(t, 20).Says(t, "not a valid lore id", "mem_<32 hex>").Never(t, "git work tree")
		}
	})
}

// waaKindLists is the CLI path of every row kind's list in the registry
// (`<kind> list`), so a new kind is in the walk the day it registers.
func waaKindLists(t testing.TB) [][]string {
	t.Helper()
	reg, err := wire.Registry(&wire.App{})
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for _, e := range reg.Entries() {
		sp := e.Spec()
		if sp.Hidden || !sp.OnlyReads() || sp.Local || !strings.HasSuffix(sp.Name, ".list") || strings.Count(sp.Name, ".") != 1 {
			continue
		}
		if _, why := readsWithoutStoreStory[sp.Name]; why {
			continue
		}
		out = append(out, op.CLIPath(sp.Name))
	}
	if len(out) < 20 {
		t.Fatalf("only %d kind lists in the registry; the walk is broken", len(out))
	}
	return out
}

// readsWithoutStoreStory are lists that read no store's rows (the
// machine's own, this checkout's), as wire's readsWithoutStore says.
var readsWithoutStoreStory = map[string]string{
	"store.list":  "lists the stores themselves",
	"batch.list":  "this machine's batch log",
	"scope.list":  "this repo's folder scopes",
	"branch.list": "this checkout's branch layers",
}
