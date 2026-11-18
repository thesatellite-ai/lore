//go:build stories && !windows

package stories

// WQ_up3polish2_test.go — story WQ (tests/stories/WQ-up3polish2.md): the
// proofs owner use pass 3's polish fixes (D-646..D-652) left open, each
// checked the way the owner met it (devco D-660..D-663).
//
//	D-660: a store push, a set-url away and back, then `lore sync --setup`
//	       says "sent N" for the N commits that really went out, and a
//	       pull's "received N" is the commits that arrived (D-648's proof)
//	D-661: doctor (every section, Team versions too) names an agent of a
//	       known person whose own actors row is not on this machine, never
//	       its raw act_ id; rule (f) of the W/WW matrices now knows such ids
//	D-662: every command printed by the lines D-646..D-650 changed runs
//	       where it was printed
//	D-663: every roster sentence says "the roster of acme (acme/org)",
//	       never "the roster in acme org/"

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// wqMember is the teammate whose work the admin pulls (made-up names).
var wqMember = journeys.Person{Name: "Ben Builder", Email: "ben@example.test", Login: "ben-gh"}

// wqRosterLabel is how every sentence names the org's roster (D-647, D-663).
const wqRosterLabel = "the roster of " + wOrg + " (" + wOrg + "/org)"

// wqStoreLine is the counts of the store line of a sync for the org store.
var wqStoreLine = regexp.MustCompile(`store ` + wOrg + `[^\n]*\(sent (\d+), received (\d+), merged \d+, refused \d+\)`)

func TestStoryWQUp3Polish2(t *testing.T) {
	s := &Story{World: journeys.NewWorld(t), ID: "WQ"}
	store := s.Remote(wStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	pc := m.Clone(app, "platform")
	m.MoveClone(t, pc, filepath.Join(m.Home, "code", "platform"))
	journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
	inOrg := wOrg + "/org"
	var bm *journeys.Machine // the teammate's laptop and clone (D-660 on)
	var bc *teamharness.Clone

	s.Step("D-663/every-roster-sentence-names-org-entry-and-folder", func() {
		for _, dir := range []string{m.Home, pc.Dir} {
			m.Lore(dir, "member", "show", "--who", "nobody@example.test", "--setup", wSetup).
				Refused(t, "nobody@example.test", wqRosterLabel).Never(t, "roster in", "org/ ", "/'s roster")
		}
		m.LoreIn(pc, "team", "show", "--handle", "@ghost").Refused(t, wqRosterLabel).Never(t, "roster in")
		m.LoreIn(pc, "team", "add", "--handle", "@web", "--members", wAdminHandle(t, m, pc)).OK(t).
			Says(t, wqRosterLabel).Never(t, "in "+wOrg+" org/")
		m.LoreIn(pc, "team", "remove", "--handle", "@web", "--yes").OK(t).
			Says(t, "removed from "+wqRosterLabel).Never(t, "roster in")
	})

	s.Step("D-662/printed-next-steps-run-where-printed", func() {
		// org config set (D-649): the confirmation's --yes command
		r := m.Lore(m.Home, "org", "config", "set", "--org", wOrg, "--key", "repos", "--add", "github.com/acme-co/partner-a")
		wyFollow(t, m, m.Home, r, "devco:D-662 org config set@home")
		// resource list --kind (D-650): the hint's --wide and --json run
		// as the same command with the flag added
		setup := []string{"--setup", wSetup, "--yes"}
		lore := func(args ...string) { m.Lore(m.Home, append(args, setup...)...).OK(t) }
		fields := []string{"project", "url", "ip", "access", "os", "region"}
		lore("resource", "kind", "add", "--kind", "server", "--to", inOrg)
		for _, f := range fields {
			lore("resource", "field", "add", "--kind", "server", "--field", f, "--type", "text", "--to", inOrg)
		}
		add := []string{"resource", "add", "--kind", "server", "--name", "web1", "--to", inOrg}
		for _, f := range fields {
			add = append(add, "--set", f+"="+f+"-value")
		}
		lore(add...)
		list := []string{"resource", "list", "--kind", "server", "--store", wOrg}
		r = m.Lore(m.Home, list...).OK(t).Says(t, "add --wide for every field, or --json")
		wyFollow(t, m, m.Home, r, "devco:D-662 resource list --kind@home")
		m.Lore(m.Home, append(list, "--wide")...).OK(t).Says(t, "REGION").Never(t, "not shown")
		m.Lore(m.Home, append(list, "--json")...).OK(t).Says(t, "region-value")
		// rosters and restores (D-647): what team list, member show and a
		// delete, trash list and restore print runs where printed
		for _, dir := range []string{m.Home, pc.Dir} {
			wyFollow(t, m, dir, m.Lore(dir, "team", "list", "--setup", wSetup), "devco:D-662 team list@"+filepath.Base(dir))
			wyFollow(t, m, dir, m.Lore(dir, "member", "show", "--who", "nobody@example.test", "--setup", wSetup), "devco:D-662 member show@"+filepath.Base(dir))
		}
		// the roster miss hint's teammate commands, with their setup, run
		// from the home folder (here the admin is that teammate)
		m.Lore(m.Home, "actor", "register", "--setup", wSetup).OK(t)
		id := wuMemID.FindString(m.LoreIn(pc, "memory", "add", "--body", "the staging database is reset every night", "--to", inOrg).OK(t).Out())
		r = m.LoreIn(pc, "memory", "delete", "--id", id, "--yes").OK(t)
		wyFollow(t, m, pc.Dir, r, "devco:D-662 memory delete@platform")
		for _, dir := range []string{pc.Dir, m.Home} {
			wyFollow(t, m, dir, m.Lore(dir, "trash", "list", "--store", inOrg).OK(t), "devco:D-662 trash list@"+filepath.Base(dir))
		}
		// setup list's line for a check from an earlier day (D-646): its
		// `lore setup check …` runs from the home folder
		m.Lore(m.Home, "setup", "check", "--setup", wSetup)
		s.AdvanceDays(2)
		wyFollow(t, m, m.Home, m.Lore(m.Home, "setup", "list").OK(t), "devco:D-662 setup list@home")
		wyFollow(t, m, m.Home, m.Lore(m.Home, "setup", "show", "--name", wSetup).OK(t), "devco:D-662 setup show@home")
	})

	s.Step("D-660/sent-and-received-are-the-commits-git-moved", func() {
		// a store push first, then the store moves away and back
		m.LoreIn(pc, "memory", "add", "--body", "the org keeps one on-call rota", "--to", inOrg).OK(t)
		m.Lore(m.Home, "store", "push", "--store", wOrg).OK(t)
		moved := s.MoveRemote(t, store, "acme-moved", true)
		m.Lore(m.Home, "store", "set-url", "--name", wOrg, "--url", moved.SSHURL(), "--yes").OK(t)
		m.Lore(m.Home, "store", "set-url", "--name", wOrg, "--url", store.SSHURL(), "--yes").OK(t)
		for _, body := range []string{"deploys freeze on fridays", "the api keeps a v1 route for a year"} {
			id := wuMemID.FindString(m.LoreIn(pc, "memory", "add", "--body", body, "--to", inOrg).OK(t).Out())
			// an edit is a second commit of the same row: rows and commits differ
			m.LoreIn(pc, "memory", "edit", "--id", id, "--body", body+" (checked)").OK(t)
		}
		before := wpRevCount(t, store.Dir)
		r := m.Lore(m.Home, "sync", "--setup", wSetup).OK(t)
		sent, _ := wqCounts(t, r)
		if went := wpRevCount(t, store.Dir) - before; sent != went {
			t.Errorf("[WQ D-660] sync --setup after a set-url round trip says sent %d, git rev-list says %d commits went out:\n%s", sent, went, r)
		}

		// a teammate's work: the admin's pull says the commits that arrived
		_, line := journeys.Invite(t, m, pc.Dir, wqMember.Email)
		bm = s.Persona(wqMember.Name, wqMember.Email).Laptop()
		journeys.GitHubLogin(t, s.World, bm, wqMember.Login)
		bc = bm.Clone(app, "platform")
		bm.MoveClone(t, bc, filepath.Join(bm.Home, "code", "platform"))
		journeys.Join(t, bm, bc.Dir, wSetup, line, wqMember, []string{wFolders})
		for _, body := range []string{"the build cache lives on the ci host", "flaky tests are quarantined, not skipped"} {
			id := wuMemID.FindString(bm.LoreIn(bc, "memory", "add", "--body", body, "--to", inOrg).OK(t).Out())
			bm.LoreIn(bc, "memory", "edit", "--id", id, "--body", body+" (checked)").OK(t)
			bm.LoreIn(bc, "sync").OK(t)
		}
		clone := storeCloneDir(t, m)
		head := wqGit(t, clone, "rev-parse", "HEAD")
		r = m.Lore(m.Home, "sync", "--setup", wSetup).OK(t)
		_, received := wqCounts(t, r)
		arrived, err := strconv.Atoi(wqGit(t, clone, "rev-list", "--count", head+"..HEAD"))
		if err != nil {
			t.Fatal(err)
		}
		if received != arrived {
			t.Errorf("[WQ D-660] the pull says received %d, git rev-list %s..HEAD counts %d commits that arrived:\n%s", received, head[:8], arrived, r)
		}
	})

	s.Step("D-661/doctor-names-a-known-persons-agent", func() {
		// the admin's and the teammate's coding agents write a row each;
		// their own actors rows are then taken out of the admin's store
		// clone, the shape of an agent whose saves arrive before (or
		// without) its actors row (the teammate's is D-652's: no identity
		// of this machine names it, only the roster's person behind it)
		// a repo linked to the org folder: doctor checks its store there
		lc := m.Clone(app, "linked")
		m.MoveClone(t, lc, filepath.Join(m.Home, "code", "linked"))
		m.LoreIn(lc, "org", "link", "--org", wOrg, "--project", "linked", "--yes").OK(t)
		ag := pc.With(wqAgentEnv("wq-session-1")...)
		ag.Lore("memory", "add", "--body", "the agent notes the release checklist", "--to", inOrg).OK(t)
		bc.With(wqAgentEnv("wq-session-2")...).Lore("memory", "add", "--body", "the agent keeps the changelog short", "--to", inOrg).OK(t)
		bm.LoreIn(bc, "sync").OK(t)
		m.Lore(m.Home, "sync", "--setup", wSetup).OK(t)
		clone := storeCloneDir(t, m)
		removed := 0
		for _, person := range []string{wAdminID(t, m, pc), wRawActor.FindString(bm.LoreIn(bc, "whoami", "--json").OK(t).Out())} {
			for _, id := range wAgentsOf(person) {
				if p := filepath.Join(clone, "org", "actors", id+".json"); wqExists(p) {
					wqGit(t, clone, "rm", "-q", p)
					removed++
				}
			}
		}
		if removed != 2 {
			t.Fatalf("want both agents' actors rows in the store clone, found %d", removed)
		}
		wqGit(t, clone, "commit", "-q", "-m", "test: the agents' actors rows have not arrived")
		known := wKnownActors(m.LoreIn(pc, "actor", "list", "--json").Out())
		for _, c := range []struct {
			dir  string
			args []string
		}{
			{pc.Dir, []string{"doctor"}},
			{clone, []string{"doctor"}},
			{filepath.Join(clone, "org"), []string{"doctor"}},
			{lc.Dir, []string{"doctor"}},
			{m.Home, []string{"doctor"}},
			{m.Home, []string{"doctor", "--all"}},
		} {
			r := m.Lore(c.dir, c.args...)
			for _, l := range wRawActorIDs(c.args, r.Out(), known) {
				t.Errorf("[WQ D-661] lore %s in %s prints a raw actor id where a name belongs: %s\n%s", strings.Join(c.args, " "), filepath.Base(c.dir), l, r)
			}
		}
	})
}

// wqCounts is the sent and received counts of r's store line for the org.
func wqCounts(t *testing.T, r teamharness.Result) (sent, received int) {
	t.Helper()
	got := wqStoreLine.FindStringSubmatch(r.Out())
	if got == nil {
		t.Fatalf("no store line with counts for %s:\n%s", wOrg, r)
	}
	sent, _ = strconv.Atoi(got[1])
	received, _ = strconv.Atoi(got[2])
	return sent, received
}

// wqGit runs git in dir and returns its trimmed output.
func wqGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// wqExists reports whether p is a file.
func wqExists(p string) bool { return exec.Command("test", "-f", p).Run() == nil }

// wqAgentEnv is a Claude Code session as lore detects it: every variable
// detection reads blanked, detection on, then the tool's markers.
func wqAgentEnv(session string) []string {
	out := []string{"LORE_HUMAN=", "LORE_NO_DETECT=0"}
	for _, n := range agentdetect.AllowedNames(nil) {
		out = append(out, n+"=")
	}
	return append(out, "CLAUDE_CODE_CHILD_SESSION=1", "CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID="+session)
}

// wqWhoami is the admin's whoami JSON in the platform repo.
func wqWhoami(t *testing.T, m *journeys.Machine, c *teamharness.Clone) string {
	t.Helper()
	return m.LoreIn(c, "whoami", "--json").OK(t).Out()
}

// wAdminID is the admin's actor id (whoami).
func wAdminID(t *testing.T, m *journeys.Machine, c *teamharness.Clone) string {
	t.Helper()
	id := wRawActor.FindString(wqWhoami(t, m, c))
	if id == "" {
		t.Fatal("whoami --json names no actor id")
	}
	return id
}

// wAdminHandle is the admin as team add --members takes her: @handle, or
// her email when she has no handle.
func wAdminHandle(t *testing.T, m *journeys.Machine, c *teamharness.Clone) string {
	t.Helper()
	h := regexp.MustCompile(`"handle":\s*"([^"]+)"`).FindStringSubmatch(wqWhoami(t, m, c))
	if h == nil {
		return wAdmin.Email
	}
	return "@" + h[1]
}
