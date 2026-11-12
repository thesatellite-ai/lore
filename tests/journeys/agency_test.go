//go:build journeys && !windows

package journeys

// agency_test.go — persona 2, the agency developer with clients. Dana works
// for an agency: its private notes live in the agency's store, the code she
// writes lives in the clients' repos, which the clients read in full. Over
// half a year: she joins and links her first client repo, takes a second
// client, edits and deletes and purges notes (and regrets it), the first
// client moves its repo to a new address, that project ends, she leaves the
// agency and later comes back as a contractor.
//
// What she checks after every step, besides the shared invariants: no file a
// client repo would commit, and nothing the client's remote serves, holds an
// agency note, her personal notes, the agency's name, its store address or
// her agency email; `lore check leaks` agrees; a commit carrying internal
// text is refused; each client sees only its own notes.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// The agency, its people and its clients (placeholders only).
const (
	agOrg       = "agencyco"
	agStoreSlug = "github.com/agencyco/notes"
	agToolsSlug = "github.com/agencyco/tools"
	agAcmeSlug  = "github.com/acme/web"
	agAcmeMoved = "github.com/acme-holdings/website"
	agGlobex    = "github.com/globex/api"

	agDanaName  = "Dana Dev"
	agDanaEmail = "dana@agencyco.example.test"
	agLeadName  = "Lee Lead"
	agLeadEmail = "lee@agencyco.example.test"
	agCarolName = "Carol Client"
	agCarolMail = "carol@acme.example.test"

	agFolderAcme   = "acme"
	agFolderGlobex = "globex"
)

// Notes Dana writes. The internal ones are long enough (8+ words) that the
// commit check matches a pasted copy.
const (
	agAcmeNote     = "acme pays late so always send the estimate before every single call"
	agAcmeNoteEdit = "acme pays late so always send the estimate two days before every call"
	agGlobexNote   = "globex legal reviews every release note so keep them short and plain"
	agGlobexTask   = "send globex the monthly hours report before the fifth"
	agPersonalNote = "my own habit is to review every diff at nine in the morning"
	agRepoRule     = "run the tests before you push"
	agPurgedNote   = "acme sandbox password rotates on the first monday of the month"
)

// agConfig is an agency person's ~/.lore/config.yaml: the agency entry with
// its store, its own repos and its clients' repos, notes in a client repo
// going to the agency by default (rules and decisions to the repo).
func agConfig(name, email, handle string) th.Config {
	return th.Config{
		Personal: &th.Personal{Store: th.PersonalLocal},
		Orgs: []th.Org{{
			Name:     agOrg,
			Identity: th.Identity{Key: "human:" + email, Name: name, Handle: handle},
			Stores:   []th.Store{{URL: "https://" + agStoreSlug, OrgFolder: "org"}},
			Own:      []string{"github.com/agencyco/*"},
			Repos:    []string{"github.com/acme/*", "github.com/globex/*"},
			Defaults: &th.OrgDefaults{ClientRepos: map[string]string{"task": agOrg, "memory": agOrg, "rule": "repo", "decision": "repo"}},
		}},
		Live: th.FreshFetch(),
	}
}

// agSecrets is what a client must never read: the agency's notes, Dana's
// personal note, the agency's name and store address, her agency email.
func agSecrets() []string {
	return []string{agAcmeNote, agAcmeNoteEdit, agGlobexNote, agGlobexTask, agPersonalNote, agPurgedNote, agOrg, agStoreSlug, agDanaEmail}
}

// agCommittable is every file of the clone git would commit (tracked, or
// untracked and not ignored), with its content.
func agCommittable(tb testing.TB, c *th.Clone) map[string]string {
	tb.Helper()
	r := c.TryGit("ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if r.Code != 0 {
		tb.Errorf("ls-files in %s: %s", c.Dir, r.Stderr)
		return nil
	}
	out := map[string]string{}
	for _, p := range strings.Split(r.Stdout, "\x00") {
		if p == "" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(c.Dir, p))
		if err != nil {
			continue // deleted in the work tree
		}
		out[p] = string(b)
	}
	return out
}

// agNoLeak reports every secret in a file the clone would commit, and in
// anything the clone's remote serves.
func agNoLeak(c Ctx, clones ...*th.Clone) {
	c.T.Helper()
	for _, cl := range clones {
		if cl == nil {
			continue // not cloned yet
		}
		if _, err := os.Stat(cl.Dir); err != nil {
			continue // the folder is gone (project ended)
		}
		for p, body := range agCommittable(c.T, cl) {
			for _, s := range agSecrets() {
				if strings.Contains(strings.ToLower(body), strings.ToLower(s)) {
					c.Errorf("%s would commit %q in %s", filepath.Base(cl.Dir), s, p)
				}
			}
		}
	}
}

// agNoteTexts are the notes alone: what no object of a client remote may
// hold. (Her agency email and so the agency's name are in the author line of
// every commit she makes there, as with any developer; files are checked
// for those by agNoLeak.)
func agNoteTexts() []string {
	return []string{agAcmeNote, agAcmeNoteEdit, agGlobexNote, agGlobexTask, agPersonalNote, agPurgedNote, agStoreSlug}
}

// agRemoteClean reports every note anything the remote serves holds.
func agRemoteClean(c Ctx, r *th.Remote) {
	c.T.Helper()
	if found := r.AllObjectsContain(agNoteTexts()...); len(found) > 0 {
		c.Errorf("the client's remote %s serves %q", r.Slug, found)
	}
}

// agLine is the whoami line starting with key.
func agLine(out, key string) string {
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == key {
			return l
		}
	}
	return ""
}

// agLeakCommit pastes internal text into a file of the clone and commits it:
// the commit must be refused naming the file, and nothing is committed.
func agLeakCommit(t *testing.T, c *th.Clone, text string) {
	t.Helper()
	head := c.Head()
	c.WriteFile("src/notes.ts", "// "+text+"\n")
	c.Git("add", "src/notes.ts")
	r := c.TryGit("commit", "-q", "-m", "add notes")
	if r.Code == 0 || !strings.Contains(r.Stdout+r.Stderr, "E_LEAK_BLOCKED") || !strings.Contains(r.Stdout+r.Stderr, "src/notes.ts") {
		t.Errorf("a commit pasting internal text into %s was not refused: exit %d\n%s%s", filepath.Base(c.Dir), r.Code, r.Stdout, r.Stderr)
	}
	if c.Head() != head {
		t.Errorf("the refused commit moved HEAD in %s", filepath.Base(c.Dir))
		c.Git("reset", "-q", "--hard", head) // drops the file too
		return
	}
	c.Git("rm", "-q", "--cached", "src/notes.ts")
	if err := os.Remove(filepath.Join(c.Dir, "src/notes.ts")); err != nil {
		t.Fatal(err)
	}
}

// The journey runs in parts (each under ~3 min; the gate runs them all
// with -run '^TestJourneyAgency'). Every part starts from the same first
// day (agStart) and Dana's second client (agWeek1), then lives on.

// agJ is one agency World: the people, the remotes and the clones.
type agJ struct {
	w                              *World
	m, lm, cm                      *Machine
	storeR, toolsR, acmeR, globexR *th.Remote
	tools, acme, carolAcme, globex *th.Clone
}

// agStart is day 1: the lead creates the agency store, the client starts
// its repo, Dana joins, links acme and writes her first notes.
func agStart(t *testing.T) *agJ {
	w := NewWorld(t)
	dana := w.Persona(agDanaName, agDanaEmail)
	lee := w.Persona(agLeadName, agLeadEmail)
	carol := w.Persona(agCarolName, agCarolMail)
	j := &agJ{w: w, m: dana.Laptop(), lm: lee.Laptop(), cm: carol.Laptop()}
	j.storeR, j.toolsR = w.Remote(agStoreSlug), w.Remote(agToolsSlug)
	j.acmeR, j.globexR = w.Remote(agAcmeSlug), w.Remote(agGlobex)
	m, lm, cm := j.m, j.lm, j.cm

	w.Step("day1/lead-creates-the-agency-store", func() {
		lm.WriteConfig(agConfig(agLeadName, agLeadEmail, "lee"))
		j.tools = lm.Clone(j.toolsR, "tools")
		lm.LoreIn(j.tools, "init", "--non-interactive", "--name=tools").OK(t)
		lm.LoreIn(j.tools, "store", "init", agOrg, "https://"+agStoreSlug, "--yes").OK(t).Says(t, "created store "+agOrg)
		lm.LoreIn(j.tools, "actor", "register").OK(t)
		j.tools.CommitAll("lore")
		j.tools.Git("push", "-q", "origin", "main")
		if !j.storeR.Has("refs/heads/main") {
			t.Errorf("store init pushed nothing to the store")
		}
	})

	w.Step("day1/client-starts-its-repo", func() {
		// Carol (the client's developer) uses lore with no config.
		j.carolAcme = cm.Clone(j.acmeR, "web")
		cm.LoreIn(j.carolAcme, "init", "--non-interactive", "--name=web").OK(t)
		j.carolAcme.CommitAll("lore")
		j.carolAcme.Git("push", "-q", "origin", "main")
	})

	w.Step("day1/dana-joins-and-links-acme", func() {
		m.WriteConfig(agConfig(agDanaName, agDanaEmail, "dana"))
		j.acme = m.Clone(j.acmeR, "web")
		// A clone of a client repo is guarded before it is linked.
		who := m.LoreIn(j.acme, "whoami").OK(t)
		if l := agLine(who.Stdout, "repo"); !strings.Contains(l, "client repo") {
			t.Errorf("acme before the link is not a client repo: %q", l)
		}
		before := j.acme.Status()
		m.LoreIn(j.acme, "org", "link", agOrg, "--project", agFolderAcme, "--yes").OK(t).Says(t, "nothing was written to this repo")
		if j.acme.Status() != before {
			t.Errorf("org link changed the client repo: %q → %q", before, j.acme.Status())
		}
		m.LoreIn(j.acme, "actor", "register").OK(t)
		who = m.LoreIn(j.acme, "whoami").OK(t)
		if l := agLine(who.Stdout, "org"); !strings.Contains(l, "project id + remote") {
			t.Errorf("whoami after the link does not say the link matched: %q", l)
		}
		// Off a terminal (an agent's shell) whoami still names the store's
		// address: it is no secret, and "<redacted>" told Dana nothing.
		who.Says(t, agStoreSlug).Never(t, "<redacted>")
	})

	w.Step("day1/first-notes", func() {
		m.LoreIn(j.acme, "memory", "add", "--body", agAcmeNote).OK(t).Says(t, "["+agOrg+"/"+agFolderAcme+"]")
		m.LoreIn(j.acme, "memory", "add", "--body", agPersonalNote, "--to", "me").OK(t)
		m.LoreIn(j.acme, "rule", "add", "--body", agRepoRule, "--to", "repo").OK(t)
		list := m.LoreIn(j.acme, "memory", "list").OK(t)
		list.Says(t, agAcmeNote, agPersonalNote)
		m.LoreIn(j.acme, "render").OK(t).Says(t, "outside the checkout")
		if b, ok := j.acme.ReadFile(".lore/LORE.md"); !ok || !strings.Contains(b, agRepoRule) {
			t.Errorf("the committed render lacks the repo rule")
		}
		m.LoreIn(j.acme, "check", "leaks").OK(t)
		m.LoreIn(j.acme, "check", "leaks", "--ci").OK(t) // the client CI's backstop
		j.acme.CommitAll("lore notes")
		j.acme.Git("push", "-q", "origin", "main")
	})

	w.Fact("no client repo commits or serves an agency note", func(c Ctx) {
		agNoLeak(c, j.acme, j.carolAcme, j.globex)
		agRemoteClean(c, j.acmeR)
		agRemoteClean(c, j.globexR)
	})
	return j
}

// agWeek1 is week 1: Dana takes a second client, globex.
func agWeek1(t *testing.T, j *agJ) {
	w, m := j.w, j.m

	w.AdvanceDays(7)
	w.Step("week1/second-client", func() {
		j.globex = m.Clone(j.globexR, "api")
		m.LoreIn(j.globex, "init", "--non-interactive", "--name=api").OK(t)
		m.LoreIn(j.globex, "org", "link", agOrg, "--project", agFolderGlobex, "--yes").OK(t)
		m.LoreIn(j.globex, "memory", "add", "--body", agGlobexNote).OK(t).Says(t, "["+agOrg+"/"+agFolderGlobex+"]")
		m.LoreIn(j.globex, "task", "add", agGlobexTask, "--to", agOrg).OK(t)
		// One client's notes never show in another client's repo.
		m.LoreIn(j.globex, "memory", "list").OK(t).Says(t, agGlobexNote).Never(t, agAcmeNote)
		m.LoreIn(j.acme, "memory", "list").OK(t).Says(t, agAcmeNote).Never(t, agGlobexNote)
		m.LoreIn(j.globex, "render").OK(t)
		m.LoreIn(j.globex, "context", "--internal").OK(t).Never(t, agAcmeNote)
		j.globex.CommitAll("lore")
		j.globex.Git("push", "-q", "origin", "main")
	})
}

// TestJourneyAgencyFirstWeeks: the client's view, a pasted note, the
// second client, the undo paths of week 2 and lore taken off the laptop.
func TestJourneyAgencyFirstWeeks(t *testing.T) {
	t.Parallel()
	j := agStart(t)
	w, m, lm, cm := j.w, j.m, j.lm, j.cm
	_, _, _ = m, lm, cm

	w.Step("day1/client-sees-only-the-repo-rule", func() {
		j.carolAcme.Git("pull", "-q", "--rebase", "origin", "main")
		r := cm.LoreIn(j.carolAcme, "rule", "list").OK(t)
		r.Says(t, agRepoRule)
		mem := cm.LoreIn(j.carolAcme, "memory", "list").OK(t)
		mem.Never(t, agAcmeNote, agPersonalNote)
		cm.LoreIn(j.carolAcme, "whoami").OK(t).Never(t, agOrg, agStoreSlug)
	})

	w.Step("day1/pasted-note-is-refused", func() {
		agLeakCommit(t, j.acme, agAcmeNote)
		pr := filepath.Join(m.Home, "pr.md")
		if err := os.WriteFile(pr, []byte("Fixes checkout.\n\n"+agAcmeNote+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		m.LoreIn(j.acme, "check", "pr-body", pr).Refused(t, "E_LEAK_BLOCKED")
	})
	agWeek1(t, j)

	var acmeMemID string
	w.AdvanceDays(7)
	w.Step("week2/edit-then-edit-back", func() {
		acmeMemID = agFindID(t, m.LoreIn(j.acme, "memory", "list", "--json").OK(t), agAcmeNote)
		m.LoreIn(j.acme, "memory", "edit", acmeMemID, "--body", agAcmeNoteEdit).OK(t)
		m.LoreIn(j.acme, "memory", "show", acmeMemID).OK(t).Says(t, agAcmeNoteEdit)
		agLeakCommit(t, j.acme, agAcmeNoteEdit) // the new wording is guarded too
		m.LoreIn(j.acme, "memory", "edit", acmeMemID, "--body", agAcmeNote).OK(t)
		m.LoreIn(j.acme, "memory", "show", acmeMemID).OK(t).Says(t, agAcmeNote).Never(t, agAcmeNoteEdit)
	})

	w.Step("week2/delete-then-restore", func() {
		// Without a terminal the delete asks first; she answers yes.
		m.LoreIn(j.acme, "memory", "delete", acmeMemID).Fails(t, 26, "E_NEEDS_CONFIRM")
		m.LoreIn(j.acme, "memory", "list").OK(t).Says(t, agAcmeNote)
		m.LoreIn(j.acme, "memory", "delete", acmeMemID, "--yes").OK(t)
		m.LoreIn(j.acme, "memory", "list").OK(t).Never(t, agAcmeNote)
		m.LoreIn(j.acme, "trash", "restore", acmeMemID).OK(t)
		m.LoreIn(j.acme, "memory", "list").OK(t).Says(t, agAcmeNote)
		agLeakCommit(t, j.acme, agAcmeNote) // restored, and guarded again
	})

	w.Step("week2/purge-then-regret", func() {
		r := m.LoreIn(j.acme, "memory", "add", "--body", agPurgedNote).OK(t)
		id := agFindID(t, m.LoreIn(j.acme, "memory", "list", "--json").OK(t), agPurgedNote)
		_ = r
		m.LoreIn(j.acme, "memory", "delete", id, "--purge", "--confirm", "--yes").OK(t)
		m.LoreIn(j.acme, "memory", "list").OK(t).Never(t, agPurgedNote)
		// The regret: restore says it is gone for good, and writes nothing.
		m.LoreIn(j.acme, "trash", "restore", id).Refused(t, id)
		m.LoreIn(j.acme, "memory", "list").OK(t).Never(t, agPurgedNote)
	})
	agUninstalled(t, j)
}

// TestJourneyAgencyClientMoves: the client moves its repo, the project
// ends (unlink) and a warranty fix brings it back (re-link).
func TestJourneyAgencyClientMoves(t *testing.T) {
	t.Parallel()
	j := agStart(t)
	agWeek1(t, j)
	w, m, lm, cm := j.w, j.m, j.lm, j.cm
	_, _, _ = m, lm, cm

	w.AdvanceDays(7)
	w.Step("week3/client-repo-moves", func() {
		// The client transfers its repo to a new owner, outside the
		// agency's repos: patterns; Dana's clone follows the new address.
		moved := w.Remote(agAcmeMoved)
		w.Git(j.acmeR.Dir, "push", "-q", "--mirror", moved.Dir)
		j.acme.Git("remote", "set-url", "origin", moved.URL())
		j.acme.Remote = moved
		agLeakCommit(t, j.acme, agAcmeNote)
		who := m.LoreIn(j.acme, "whoami").OK(t)
		if l := agLine(who.Stdout, "repo"); !strings.Contains(l, "client repo") {
			t.Errorf("the moved client repo lost its leak guards: %q\n%s", l, who)
		}
		who.Says(t, "lore org link "+agOrg+" --project "+agFolderAcme)
		m.LoreIn(j.acme, "memory", "list").OK(t).Says(t, agAcmeNote)
		// Writes do not guess while the link names the old address.
		m.LoreIn(j.acme, "memory", "add", "--body", "a note while the link is stale").Refused(t, "lore org link")
		// She re-links at the new address, as whoami told her.
		m.LoreIn(j.acme, "org", "link", agOrg, "--project", agFolderAcme, "--yes").OK(t)
		who = m.LoreIn(j.acme, "whoami").OK(t)
		if l := agLine(who.Stdout, "org"); !strings.Contains(l, "project id + remote") {
			t.Errorf("after re-linking, whoami does not match by the link: %q", l)
		}
		m.LoreIn(j.acme, "memory", "list").OK(t).Says(t, agAcmeNote)
		j.acme.Git("push", "-q", "origin", "main")
	})
	w.Fact("the moved repo serves no agency note", func(c Ctx) { agRemoteClean(c, w.Remote(agAcmeMoved)) })

	var acmeRepoID string
	w.AdvanceDays(30)
	w.Step("month2/acme-project-ends", func() {
		acmeRepoID = agRepoID(t, j.acme)
		acmePrj := agProjectID(t, j.acme)
		m.LoreIn(j.acme, "task", "list").OK(t).Never(t, agGlobexTask)
		// The contract is over: the repo is unlinked from the agency. It
		// asks first, and names every link (old and new address).
		ask := m.LoreIn(j.acme, "org", "unlink", agOrg)
		ask.Fails(t, 26, "E_NEEDS_CONFIRM", agAcmeSlug, agAcmeMoved)
		if !agStoreLinks(t, j.storeR, agFolderAcme, acmePrj) {
			t.Errorf("an unconfirmed unlink changed the store")
		}
		m.LoreIn(j.acme, "org", "unlink", agOrg, "--yes").OK(t).Says(t, "2 link(s) removed", "nothing was written to this repo")
		if agStoreLinks(t, j.storeR, agFolderAcme, acmePrj) {
			t.Errorf("the store still links acme after the unlink")
		}
		// The notes stay the agency's, in the store.
		if !agStoreHolds(t, j.storeR, agAcmeNote) {
			t.Errorf("unlinking dropped the agency's acme notes from the store")
		}
		// The repo now follows its own rules: the new owner matches no
		// pattern, so it is no org's repo and the agency notes are gone
		// from it.
		who := m.LoreIn(j.acme, "whoami").OK(t)
		if l := agLine(who.Stdout, "repo"); !strings.Contains(l, "no org") {
			t.Errorf("after the unlink acme should follow its own rules (no org): %q", l)
		}
		m.LoreIn(j.acme, "memory", "list").OK(t).Never(t, agAcmeNote)
		// Unlinking twice changes nothing.
		m.LoreIn(j.acme, "org", "unlink", agOrg, "--yes").OK(t).Says(t, "nothing to unlink")
		// j.globex is untouched.
		m.LoreIn(j.globex, "memory", "list").OK(t).Says(t, agGlobexNote)
		// The folder goes.
		if err := os.RemoveAll(j.acme.Dir); err != nil {
			t.Fatal(err)
		}
		m.Lore("", "doctor").OK(t)
	})

	w.AdvanceDays(30)
	w.Step("month3/warranty-fix-in-acme", func() {
		// A bug report: she clones the client's repo again, at its new
		// address, and links it again (the undo of the unlink).
		j.acme = m.Clone(w.Remote(agAcmeMoved), "website")
		if got := agRepoID(t, j.acme); got != acmeRepoID {
			t.Errorf("the fresh clone has repo id %s, the old one had %s", got, acmeRepoID)
		}
		m.LoreIn(j.acme, "org", "link", agOrg, "--project", agFolderAcme, "--yes").OK(t)
		if l := agLine(m.LoreIn(j.acme, "whoami").OK(t).Stdout, "repo"); !strings.Contains(l, "client repo") {
			t.Errorf("re-linked acme is not a client repo: %q", l)
		}
		m.LoreIn(j.acme, "memory", "list").OK(t).Says(t, agAcmeNote)
		agLeakCommit(t, j.acme, agAcmeNote)
	})
}

// TestJourneyAgencyLeaves: Dana leaves the agency and comes back as a
// contractor.
func TestJourneyAgencyLeaves(t *testing.T) {
	t.Parallel()
	j := agStart(t)
	agWeek1(t, j)
	w, m, lm, cm := j.w, j.m, j.lm, j.cm
	_, _, _ = m, lm, cm

	var danaActor string
	w.AdvanceDays(30)
	w.Step("month4/dana-leaves-the-agency", func() {
		danaActor = agActorID(t, m.LoreIn(j.globex, "whoami").OK(t))
		r := lm.LoreIn(j.tools, "actor", "offboard", "@dana", "--reason", "moved on", "--yes").OK(t)
		r.Says(t, "offboarded")
		lm.LoreIn(j.tools, "actor", "list").OK(t).Says(t, agDanaName)
		// Her own clone cannot put her back on the roster.
		m.LoreIn(j.globex, "actor", "register").Refused(t, "E_STATUS_CHANGE")
		// She takes the agency off her laptop: its entry, then its store.
		m.WriteConfig(th.Config{Personal: &th.Personal{Store: th.PersonalLocal}, Live: th.FreshFetch()})
		m.Lore("", "store", "forget", "https://"+agStoreSlug).OK(t)
		// Nothing of the agency stays readable on her laptop.
		agNoAgencyNotesIn(t, m.LorePath())
		m.LoreIn(j.globex, "memory", "list").OK(t).Never(t, agGlobexNote, agAcmeNote)
		// Her own notes stay on her laptop. The one she wrote at work is
		// filed in her personal store's agency folder (me/agencyco), shown
		// while she works for the agency; it is kept, not deleted.
		if !agFileHolds(t, filepath.Join(m.LorePath(), "personal"), agPersonalNote) {
			t.Errorf("leaving the agency lost Dana's personal note")
		}
	})

	w.AdvanceDays(60)
	w.Step("month6/dana-rejoins-as-contractor", func() {
		lm.LoreIn(j.tools, "actor", "rejoin", "@dana", "--type", "contractor", "--yes").OK(t)
		m.WriteConfig(agConfig(agDanaName, agDanaEmail, "dana"))
		m.LoreIn(j.globex, "actor", "register").OK(t)
		if got := agActorID(t, m.LoreIn(j.globex, "whoami").OK(t)); got != danaActor {
			t.Errorf("rejoining gave Dana actor %s, she was %s", got, danaActor)
		}
		m.LoreIn(j.globex, "memory", "list").OK(t).Says(t, agGlobexNote, agPersonalNote).Never(t, agAcmeNote)
		agLeakCommit(t, j.globex, agGlobexNote)
	})
}

// agRepoID is the repo id of the clone (.lore/data/_meta.json).
func agRepoID(t *testing.T, c *th.Clone) string {
	t.Helper()
	b, ok := c.ReadFile(".lore/data/_meta.json")
	if !ok {
		t.Fatalf("%s has no .lore/data/_meta.json", c.Dir)
	}
	i := strings.Index(b, `"rep_`)
	if i < 0 {
		t.Fatalf("no repo id in %s", b)
	}
	return b[i+1 : i+1+strings.Index(b[i+1:], `"`)]
}

// agProjectID is the prj_ id of the clone (.lore/data/_meta.json).
func agProjectID(t *testing.T, c *th.Clone) string {
	t.Helper()
	b, _ := c.ReadFile(".lore/data/_meta.json")
	i := strings.Index(b, `"prj_`)
	if i < 0 {
		t.Fatalf("no project id in %s", b)
	}
	return b[i+1 : i+1+strings.Index(b[i+1:], `"`)]
}

// agStoreLinks reports whether the store's folder links project id pid.
func agStoreLinks(t *testing.T, store *th.Remote, folder, pid string) bool {
	t.Helper()
	b, ok := store.Read("refs/heads/main", folder+"/.lore/data/_meta.json")
	return ok && strings.Contains(b, pid)
}

// agStoreHolds reports whether any object of the store holds text.
func agStoreHolds(t *testing.T, store *th.Remote, text string) bool {
	t.Helper()
	return len(store.AllObjectsContain(text)) > 0
}

// agActorID is the act_ id whoami names.
func agActorID(t *testing.T, r th.Result) string {
	t.Helper()
	l := agLine(r.Stdout, "identity")
	i := strings.Index(l, "act_")
	if i < 0 {
		t.Fatalf("whoami names no actor: %s", r)
	}
	return strings.Fields(l[i:])[0]
}

// agFileHolds reports whether a file under dir holds text.
func agFileHolds(t *testing.T, dir, text string) bool {
	t.Helper()
	found := false
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if b, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(b), text) {
				found = true
			}
		}
		return nil
	})
	return found
}

// agNoAgencyNotesIn reports every file under dir that still holds an agency
// note (the agency's store clone, a private render, a cache).
func agNoAgencyNotesIn(t *testing.T, dir string) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		for _, n := range []string{agAcmeNote, agGlobexNote, agGlobexTask} {
			if strings.Contains(string(b), n) {
				t.Errorf("after leaving, %s still holds the agency note %q", p, n)
			}
		}
		return nil
	})
}

// agFindID is the id of the row whose JSON line holds body.
func agFindID(t *testing.T, r th.Result, body string) string {
	t.Helper()
	for _, l := range strings.Split(r.Stdout, "\n") {
		if !strings.Contains(l, body) {
			continue
		}
		if i := strings.Index(l, `"id":"`); i >= 0 {
			rest := l[i+6:]
			return rest[:strings.Index(rest, `"`)]
		}
	}
	// --json may be one array: find the id nearest before the body.
	out := r.Stdout
	bi := strings.Index(out, body)
	if bi < 0 {
		t.Fatalf("no row with %q in %s", body, r)
	}
	ii := strings.LastIndex(out[:bi], `"id"`)
	if ii < 0 {
		t.Fatalf("no id before %q in %s", body, r)
	}
	rest := out[ii+4:]
	rest = rest[strings.Index(rest, `"`)+1:]
	return rest[:strings.Index(rest, `"`)]
}

// agUninstalled is week 2's last day: Dana's laptop loses lore (an
// uninstall, a new shell profile without it) while her client clones keep
// their hooks. A client repo must not take an internal note just because
// nothing is left to check it; her own repo must still commit.
func agUninstalled(t *testing.T, j *agJ) {
	w, m := j.w, j.m
	w.Step("week2/lore-gone-from-path-then-back", func() {
		own := m.Clone(w.Remote("github.com/dana-dev/dotfiles"), "dotfiles")
		m.LoreIn(own, "init", "--non-interactive", "--name=dotfiles").OK(t)
		own.CommitAll("lore") // checked by lore: not a client repo
		m.SetEnv("PATH", "/usr/bin:/bin")
		defer m.SetEnv("PATH", "")
		// The client repo refuses the pasted note, says why and what to do.
		head := j.acme.Head()
		j.acme.WriteFile("src/notes.ts", "// "+agAcmeNote+"\n")
		j.acme.Git("add", "src/notes.ts")
		r := j.acme.TryGit("commit", "-q", "-m", "add notes")
		out := r.Stdout + r.Stderr
		if r.Code == 0 || !strings.Contains(out, "client repo") || !strings.Contains(out, "install lore") {
			t.Errorf("without lore a client repo took a commit unchecked: exit %d\n%s", r.Code, out)
		}
		if j.acme.Head() != head {
			t.Errorf("the refused commit moved HEAD in acme")
			j.acme.Git("reset", "-q", "--hard", head)
		} else {
			j.acme.Git("rm", "-q", "--cached", "src/notes.ts")
			if err := os.Remove(filepath.Join(j.acme.Dir, "src/notes.ts")); err != nil {
				t.Fatal(err)
			}
		}
		// Her own repo is nobody's client: it still commits.
		own.WriteFile("notes.txt", "just mine\n")
		own.Git("add", "notes.txt")
		if r := own.TryGit("commit", "-q", "-m", "mine"); r.Code != 0 {
			t.Errorf("without lore her own repo cannot commit: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
		}
		// lore back on PATH: the real check runs again.
		m.SetEnv("PATH", "")
		agLeakCommit(t, j.acme, agAcmeNote)
	})
}
