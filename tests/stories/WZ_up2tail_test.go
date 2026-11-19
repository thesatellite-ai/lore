//go:build stories && !windows

package stories

// WZ_up2tail_test.go — story WZ, devco D-607..D-611 (owner use pass 2
// follow-ups): an org admin with a personal store works in a repo linked
// to a store folder, in a client repo of the org not linked to a folder,
// and in the home folder.
//
//	day1: StartTeam in platform, own: github.com/acme-co/*, repos:
//	      github.com/client-co/*; web (own) linked to acme/sync; shop
//	      (client) cloned unlinked; LORE_PUSH_CLAIMS=0 (only --push asks)
//	D-610: search in the unlinked client repo says the client folders were
//	       not read and how to read them (the personal store answered)
//	D-607: every printed claim (task next's "take it:", E_LOST_CLAIM's
//	       "takes other work") carries --push in the linked repo and runs
//	       there without the claim refusal
//	D-608: claim, release, revoke and reassign name the store's signals ref
//	       by one label ("of store acme/sync"), never "pushed <ref> to
//	       origin"; a hold and its release name where they went
//	D-611: E_IN_TRASH names who deleted the row from the home folder too,
//	       never a raw act_ id
//	D-609: trash purge --id --confirm finishes cleanly in the linked repo
//	       and in the home folder and says what it purged

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// wzRawActor is an actor id printed where a person reads a name.
var wzRawActor = regexp.MustCompile(`act_[0-9a-f]{8,}`)

// wzTaskID is a task id in a command's output.
var wzTaskID = regexp.MustCompile(`tsk_[0-9a-f]+`)

// wzClaimCmd is a printed claim command: `lore task next --claim` or
// `lore task claim --id <id>`, with the --push that follows it if any.
var wzClaimCmd = regexp.MustCompile(`lore task (?:next --claim|claim --id tsk_[0-9a-f]+)(?: --push)?`)

// wzStoreLabel is the one label of a signals write in the linked repo.
const wzStoreLabel = "the signals ref refs/lore/signals/main of store acme/sync"

func TestStoryWZUp2Tail(t *testing.T) {
	s := &Story{World: journeys.NewWorld(t), ID: "WZ"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote(wWebSlug), func(dir string) { PlainRepo(t, dir) })
	shop := s.Seed(s.Remote(wyShopSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "own", "--add", "github.com/acme-co/*", "--yes").OK(t)
	m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "repos", "--add", "github.com/client-co/*", "--yes").OK(t)
	wc := m.Clone(web, "web")
	m.MoveClone(t, wc, filepath.Join(m.Home, "code", "web"))
	sc := m.Clone(shop, "shop")
	m.MoveClone(t, sc, filepath.Join(m.Home, "code", "shop"))
	inSync := wOrg + "/sync"
	m.Lore(wc.Dir, "org", "link", "--org", wOrg, "--project", "sync", "--own", "--yes").OK(t)
	m.SetEnv(teamharness.EnvPushClaims, "0") // no standing go-ahead: only --push asks
	add := m.Lore(m.Home, "memory", "add", "--body", "the deploy key rotates monthly", "--to", inSync, "--setup", wSetup, "--yes").OK(t)
	mem := wuMemID.FindString(add.Out())
	if mem == "" {
		t.Fatalf("memory add printed no id: %s", add.Out())
	}

	s.Step("D-610/client-search-says-the-client-folders-were-not-read", func() {
		// the personal store answers; the answer says acme's folders were
		// not read and names the link and --store, as the refusal does
		r := m.Lore(sc.Dir, "search", "--query", "deploy").OK(t).
			Says(t, "personal store only, not "+wOrg+"'s folders", "lore org link --org "+wOrg+" --project <folder>", "--store "+wOrg+"/<folder>")
		wyFollow(t, m, sc.Dir, r, "devco:D-610 search@client")
		m.Lore(sc.Dir, "search", "--query", "deploy", "--store", inSync).OK(t).Says(t, mem)
	})

	var id string
	s.Step("D-607/claim-hints-carry-push", func() {
		id = wzTaskID.FindString(m.Lore(wc.Dir, "task", "add", "--title", "rotate the deploy key").OK(t).Out())
		if id == "" {
			t.Fatal("task add printed no id")
		}
		r := m.Lore(wc.Dir, "task", "next").OK(t).Says(t, "take it: lore task next --claim --push (or lore task claim --id "+id+" --push)")
		wzFollowClaims(t, m, wc.Dir, r, "devco:D-607 task next@linked", true)
		// the claim the follow took is revoked; the holder's heartbeat is
		// fenced out (E_LOST_CLAIM) and its hint takes other work with --push
		r = m.Lore(wc.Dir, "task", "revoke", "--id", id, "--push").OK(t)
		r = m.Lore(wc.Dir, "task", "heartbeat", "--id", id).Refused(t, "E_LOST_CLAIM", "lore task next --claim --push")
		wzFollowClaims(t, m, wc.Dir, r, "devco:D-607 E_LOST_CLAIM@linked", false)
	})

	s.Step("D-608/one-label-for-every-signals-write", func() {
		m.Lore(wc.Dir, "task", "release", "--id", id) // what the follow above claimed, if it still holds
		m.Lore(wc.Dir, "task", "claim", "--id", id, "--push").OK(t).Says(t, "on "+wzStoreLabel)
		m.Lore(wc.Dir, "task", "release", "--id", id).OK(t).Says(t, "off "+wzStoreLabel)
		m.Lore(wc.Dir, "task", "claim", "--id", id, "--push").OK(t)
		m.Lore(wc.Dir, "task", "revoke", "--id", id, "--push").OK(t).
			Says(t, "on "+wzStoreLabel, "fenced out").Never(t, "pushed refs/lore/signals")
		m.Lore(wc.Dir, "task", "claim", "--id", id, "--push").OK(t)
		m.Lore(wc.Dir, "task", "reassign", "--id", id, "--assign", "@ada", "--push").OK(t).
			Says(t, "reassigned to @ada", "claim off "+wzStoreLabel)
		// a hold names where it went and where its release went
		m.Lore(wc.Dir, "hold", "claim", "--name", "full-suite", "--push").OK(t).Says(t, "holding full-suite until", " on refs/lore/signals", "(pushed now")
		m.Lore(wc.Dir, "hold", "release", "--name", "full-suite").OK(t).Says(t, "the hold is off refs/lore/signals", "(pushed now")
	})

	var rul string
	s.Step("D-611/in-trash-names-the-deleter", func() {
		out := m.Lore(m.Home, "rule", "add", "--body", "never deploy on fridays", "--to", inSync, "--setup", wSetup, "--yes").OK(t).Out()
		rul = wyRulID.FindString(out)
		if rul == "" {
			t.Fatalf("rule add printed no id: %s", out)
		}
		m.Lore(m.Home, "rule", "delete", "--id", rul, "--yes").OK(t)
		for _, dir := range []string{m.Home, wc.Dir, pc.Dir} {
			r := m.Lore(dir, "rule", "show", "--id", rul).Refused(t, "is in the trash", "deleted by ada")
			if wzRawActor.MatchString(r.Out()) {
				t.Errorf("devco:D-611: E_IN_TRASH in %s prints a raw actor id:\n%s", filepath.Base(dir), r)
			}
		}
	})

	s.Step("D-609/purge-finishes-cleanly", func() {
		m.Lore(wc.Dir, "trash", "purge", "--id", rul, "--confirm").OK(t).Says(t, "purged 1 row", rul).Never(t, "E_INTERNAL", "safety copies")
		out := m.Lore(m.Home, "rule", "add", "--body", "never deploy at night", "--to", inSync, "--setup", wSetup, "--yes").OK(t).Out()
		home := wyRulID.FindString(out)
		m.Lore(m.Home, "rule", "delete", "--id", home, "--yes").OK(t)
		m.Lore(m.Home, "trash", "purge", "--id", home, "--confirm").OK(t).Says(t, "purged 1 row", home).Never(t, "E_INTERNAL", "safety copies")
		m.Lore(m.Home, "rule", "show", "--id", home).Refused(t, home+" was purged from "+inSync)
	})
}

// wzFollowClaims checks every claim command r printed: it names --push
// (this repo's claims need it), and, with run, it runs in dir without the
// claim refusal ("unless you ask") or a wrong-hint refusal.
func wzFollowClaims(t *testing.T, m *journeys.Machine, dir string, r teamharness.Result, id string, run bool) {
	t.Helper()
	cmds := wzClaimCmd.FindAllString(r.Out(), -1)
	if len(cmds) == 0 {
		t.Errorf("%s: no claim command printed:\n%s", id, r)
	}
	for _, c := range cmds {
		c = strings.TrimSpace(c)
		if !strings.Contains(c, "--push") {
			t.Errorf("%s: printed `%s` without --push, which this repo refuses:\n%s", id, c, r)
			continue
		}
		if !run {
			continue
		}
		pr := m.Lore(dir, strings.Fields(c)[1:]...)
		if strings.Contains(pr.Out(), "unless you ask") || wuBadRefusal(pr.Code, pr.Out()) != "" {
			t.Errorf("%s: printed `%s`, which is refused where printed:\n%s", id, c, pr)
		}
		if pr.Code == 0 {
			m.Lore(dir, "task", "release", "--id", strings.TrimSpace(wzTaskID.FindString(pr.Out()))) // free it for the next printed claim
		}
	}
}
