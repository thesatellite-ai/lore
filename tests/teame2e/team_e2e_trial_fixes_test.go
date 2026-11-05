//go:build teame2e

package teame2e

// Real-user trial findings N1 and N2 (lore-final trial): one person on
// several machines keeps ONE actor row in a client repo, and a note on a
// task the writer does not hold goes where that task's notes live.

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// aliceActorFiles lists the actor files in c's checkout that name alice.
func aliceActorFiles(t *testing.T, c *th.Clone) []string {
	t.Helper()
	var out []string
	for _, f := range strings.Fields(c.Git("ls-files", dataDir+"/"+tblActors+"/")) {
		if body, ok := c.ReadFile(f); ok && strings.Contains(body, aliceName) {
			out = append(out, f)
		}
	}
	return out
}

// F18 / N1: "registering on two machines gives one file". The client repo
// holds only alice's projection {id, display name, kind}; a second and a
// third machine (new HOME, fresh clone) must reuse it, and two machines
// writing at the same instant before either row exists must still end with
// one file, because the id is derived from stable_key.
func TestF18_SamePersonOnSeveralMachinesKeepsOneRepoActor(t *testing.T) {
	a := newAgency(t, th.Flag("to", "rule", "add"))
	want := actorIDFor(aliceKey)
	if want == "" {
		t.Fatal("no actor id for alice's stable key")
	}
	wantFile := dataDir + "/" + tblActors + "/" + want + ".json"

	// Two machines of alice, both cloned before any row of hers is in the
	// repo, write at the same instant.
	alice2 := a.w.Human("alice2", "alice@northwind.example")
	alice2.WriteConfig(aliceConfig())
	m2 := alice2.Clone(a.app, "app")
	for _, r := range a.w.Simultaneously(
		a.aliceApp.Prepare("rule", "add", "--body", "run the tests before you push", "--to", "repo"),
		m2.Prepare("rule", "add", "--body", "keep migrations reversible", "--to", "repo"),
	) {
		r.OK(t)
	}
	a.aliceApp.CommitAll("lore: rule")
	a.aliceApp.AdminPush("origin", "main")
	m2.CommitAll("lore: rule")
	m2.Git("pull", "--no-rebase", "--no-edit", "origin", "main")
	m2.AdminPush("origin", "main")
	if got := aliceActorFiles(t, m2); len(got) != 1 || got[0] != wantFile {
		t.Fatalf("two machines writing at once must leave one actor file %s, got %v", wantFile, got)
	}

	// A third machine: fresh HOME, fresh clone holding only the projection.
	alice3 := a.w.Human("alice3", "alice@northwind.example")
	alice3.WriteConfig(aliceConfig())
	m3 := alice3.Clone(a.app, "app")
	if body, ok := m3.ReadFile(wantFile); !ok || strings.Contains(body, aliceKey) {
		t.Fatalf("precondition: the client repo holds alice's projection without her key: %q", body)
	}
	rul := mustID(t, m3.MustLore("rule", "add", "--body", "never commit .env files", "--to", "repo"), "rul")
	if st := m3.Status(); strings.Contains(st, dataDir+"/"+tblActors+"/") {
		t.Fatalf("a second machine of the same person must not add an actor row:\n%s", st)
	}
	body, ok := m3.ReadFile(dataDir + "/" + tblRules + "/" + rul + ".json")
	if !ok {
		t.Fatalf("rule %s was not exported", rul)
	}
	// Row envelope v2 names the creator in created_by (v0.1.17's
	// created_by_actor_id column is the lore.db bridge's spelling, 9cb384c).
	if !strings.Contains(body, `"created_by": "`+want+`"`) {
		t.Fatalf("the rule must name alice's existing actor %s:\n%s", want, body)
	}
}

// §7.6 / D8 / N2: a note by an agent that does not hold the task goes
// where the task's notes live (the store), the same as a holder's note;
// it never reaches the client repo, and ✓ means it was written.
func TestRecipe15_NoteOnATaskNotHeldGoesToTheStore(t *testing.T) {
	a := newAgency(t, append(append([]th.Cap{}, crewCaps...), th.Cmd("task", "note"), th.Flag("to", "task", "add"))...)
	storeTask := mustID(t, a.aliceApp.MustLore("task", "add", "invoice reminder", "--to", orgNorthwind), "tsk")
	clientTask := mustID(t, a.acmeApp.MustLore("task", "add", "fix rounding", "--tasklist", a.tl), "tsk")
	a.acmeApp.MustLore("sync")
	ag := startAgent(t, a.alice, a.aliceApp, agentLabelBackend, "alice")
	ac := ag.Clone(a.app, "app")

	cases := []struct{ name, id, note string }{
		{"store task", storeTask, "unheld note on the store task 4f1"},
		{"client repo task", clientTask, "unheld note on the client task 9b2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ac.Lore("task", "note", tc.id, tc.note).OK(t).Says(t, "note", tc.id)
			// The note is a comment row of the store project acme/. A v0.1.17
			// store kept it on the live ref under acme/comments/; a 0.1.18
			// store keeps its rows on the store branch under
			// acme/.lore/data/comments/ (STORE_SERVER_SPEC 6.1, DECISIONS_LOG
			// 18 STORE TARGETS), so both refs and both layouts count.
			found := false
			for _, ref := range []string{storeLiveRef, refMain} {
				for _, f := range a.store.Files(ref) {
					if !strings.HasPrefix(f, folderAcme+"/") || !strings.Contains(f, "/"+tblComments+"/") {
						continue
					}
					if b, _ := a.store.Read(ref, f); strings.Contains(b, tc.note) {
						found = true
					}
				}
			}
			if !found {
				t.Fatalf("the note must be a comment row of the store project %s/: live %v main %v", folderAcme, a.store.Files(storeLiveRef), a.store.Files(refMain))
			}
			if f := a.clientSees(t, tc.note); len(f) != 0 {
				t.Fatalf("a work note must never reach the client repo: %v", f)
			}
		})
	}
}

// N2 follow-up, fixed rule 6: a note in a LINKED repo whose coordination
// target cannot be opened (store unreadable) fails closed: no ✓, nothing
// written, and after the network is back nothing reaches the client repo.
func TestF16_NoteFailsClosedWithoutTheStore(t *testing.T) {
	a := newAgency(t, th.Cmd("task", "note"), th.Flag("to", "task", "add"))
	cases := []struct{ name, id, note string }{
		{"store task", mustID(t, a.aliceApp.MustLore("task", "add", "invoice reminder", "--to", orgNorthwind), "tsk"), "note while the store is unreadable 3d7"},
		{"client repo task", mustID(t, a.acmeApp.MustLore("task", "add", "fix rounding", "--tasklist", a.tl), "tsk"), "note on the client task while the store is unreadable 5e8"},
	}
	a.acmeApp.MustLore("sync")
	a.aliceApp.MustLore("sync")
	a.breakStoreAccess(t)
	for _, tc := range cases {
		a.aliceApp.Lore("task", "note", tc.id, tc.note).Refused(t).Never(t, "✓", "note cmt_")
	}
	a.alice.Online()
	a.aliceApp.Lore("sync")
	for _, tc := range cases {
		if f := a.clientSees(t, tc.note); len(f) != 0 {
			t.Fatalf("%s: a refused note reached the client repo: %v", tc.name, f)
		}
		if out := a.aliceApp.TryGit("grep", "--untracked", "-l", tc.note, "--", dataDir).Stdout; out != "" {
			t.Fatalf("%s: a refused note was written to the checkout: %s", tc.name, out)
		}
	}
}

// N2 follow-up: a plain repo (no org, no store) keeps writing notes as
// before, including when it has no team coordination (LORE_LIVE=0), where
// the note is a plain comment in this checkout.
func TestF07_NoteInAPlainRepoStillWrites(t *testing.T) {
	cases := []struct {
		name string
		env  []string
	}{
		{"live rows", nil},
		{"no live rows (LORE_LIVE=0)", []string{"LORE_LIVE=0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := newRepoTeam(t, th.Cmd("task", "note"))
			c := rt.c1.With(tc.env...)
			id := mustID(t, c.MustLore("task", "add", "fix rounding", "--tasklist", rt.tasklist), "tsk")
			note := "plain repo note 61c"
			c.Lore("task", "note", id, note).OK(t).Says(t, "✓ note", id)
			c.MustLore("sync")
			// With live rows the comment is shared on the remote (the live
			// ref, or main once it lands, as a finished row does); without,
			// it is a file in this checkout's .lore/data.
			shared := false
			for _, ref := range []string{refLive, refMain} {
				for _, f := range rt.app.FilesMatching(ref, tblComments+"/") {
					if b, _ := rt.app.Read(ref, f); strings.Contains(b, note) {
						shared = true
					}
				}
			}
			inData := c.TryGit("grep", "--untracked", "-l", note, "--", dataDir+"/"+tblComments).Stdout != ""
			if !shared && !inData {
				t.Fatalf("the note must be a comment row of this repo (remote or .lore/data):\n%s", c.Status())
			}
		})
	}
}
