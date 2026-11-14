//go:build stories && !windows

package stories

// j2_teammate_test.go — story J2, a teammate on a second machine (page:
// J2-teammate-second-machine.md). The lead starts the team and invites Mo;
// Mo joins on their own laptop with the invite line, reads and edits the
// team's notes, both change the same note, Mo hand-edits a note in the store
// clone with plain git, then sets up a second laptop that gets everything
// back.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// The team's names (placeholders).
const (
	j2Org     = "brightco"
	j2Project = "platform"
	j2Setup   = "bright"
	j2Folders = "~/code/**"
)

var (
	j2Lead = journeys.Person{Name: "Lena Lead", Email: "lena.lead@example.test", Login: "lena-gh"}
	j2Mate = journeys.Person{Name: "Mo Mate", Email: "mo.mate@example.test", Login: "mo-gh"}
)

// j2Memories is the id → body of every memory a list --json answer shows.
func j2Memories(t testing.TB, r teamharness.Result) map[string]string {
	t.Helper()
	var o struct {
		Data []struct {
			ID   string `json:"id"`
			Body string `json:"body"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &o); err != nil {
		t.Fatalf("not a list --json answer (%v): %s", err, r)
	}
	out := map[string]string{}
	for _, m := range o.Data {
		out[m.ID] = m.Body
	}
	return out
}

// j2Task is the title and priority of a task show --json answer.
func j2Task(t testing.TB, r teamharness.Result) (title, priority string) {
	t.Helper()
	var o struct {
		Data struct {
			Title    string `json:"title"`
			Priority string `json:"priority"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &o); err != nil {
		t.Fatalf("not a task show --json answer (%v): %s", err, r)
	}
	return o.Data.Title, o.Data.Priority
}

// j2Laptop gives p a machine signed in to GitHub with the app cloned at
// ~/code/app.
func j2Laptop(t *testing.T, s *Story, m *journeys.Machine, p journeys.Person, app *teamharness.Remote) *teamharness.Clone {
	t.Helper()
	journeys.GitHubLogin(t, s.World, m, p.Login)
	c := m.Clone(app, "app")
	m.MoveClone(t, c, filepath.Join(m.Home, "code", "app"))
	return c
}

// TestStoryJ2TeammateSecondMachine replays J2 end to end.
func TestStoryJ2TeammateSecondMachine(t *testing.T) {
	s := New(t, "J2")
	store := s.Remote("github.com/bright-co/lore-team")
	app := s.Seed(s.Remote("github.com/bright-co/app"), func(dir string) { PlainRepo(t, dir) })
	lead := s.Person(j2Lead.Name, j2Lead.Email)
	mo := s.Person(j2Mate.Name, j2Mate.Email)
	la, ma := j2Laptop(t, s, lead, j2Lead, app), j2Laptop(t, s, mo, j2Mate, app)
	target := j2Org + "/" + j2Project
	var memID, taskID, invite string

	s.Step("day1/lead-starts-the-team", func() {
		journeys.StartTeam(t, lead, la.Dir, j2Setup, j2Org, store.SSHURL(), j2Lead, []string{j2Folders})
		lead.LoreIn(la, "init").OK(t)
		la.CommitAll("lore init")
		la.Git("push", "-q", "origin", "HEAD")
		memID = j1ID(t, lead.LoreIn(la, "memory", "add", "--body", "deploys go out on tuesdays", "--to", target, "--create", "--json").OK(t))
		taskID = j1ID(t, lead.LoreIn(la, "task", "add", "--title", "upgrade the queue client", "--to", target, "--json").OK(t))
		lead.LoreIn(la, "org", "link", j2Org, "--project", j2Project, "--yes").OK(t)
		lead.LoreIn(la, "sync").OK(t)
		_, invite = journeys.Invite(t, lead, la.Dir, j2Mate.Email)
	})

	s.Step("day1/mate-joins-on-their-laptop", func() {
		ma.Git("pull", "-q", "--ff-only") // the lead's lore init commit
		journeys.Join(t, mo, ma.Dir, j2Setup, invite, j2Mate, []string{j2Folders})
		mo.LoreIn(ma, "whoami").OK(t).Says(t, j2Org, j2Mate.Email)
		if got := j2Memories(t, mo.LoreIn(ma, "memory", "list", "--json").OK(t))[memID]; got != "deploys go out on tuesdays" {
			t.Errorf("[J2] the teammate reads the team note as %q after joining", got)
		}
		mo.LoreIn(ma, "task", "list").OK(t).Says(t, "upgrade the queue client")
		// B2-4: a store commit is titled by what changed, never by an actor id
		if log := s.Git(store.Dir, "log", "--format=%s", "main"); strings.Contains(log, "write act_") {
			t.Errorf("[J2] a team store commit is titled by an actor id:\n%s", log)
		}
		// B2-6: one cache folder per kind, never both repo/ and repos/
		_, e1 := os.Stat(filepath.Join(mo.Home, ".lore", "cache", "repo"))
		_, e2 := os.Stat(filepath.Join(mo.Home, ".lore", "cache", "repos"))
		if e1 == nil && e2 == nil {
			t.Errorf("[J2] ~/.lore/cache has both repo/ and repos/")
		}
	})

	s.Step("day2/both-change-the-same-task", func() {
		s.AdvanceDays(1)
		storeRefs, appRefs := s.RefsOf(store), s.RefsOf(app)
		lead.LoreIn(la, "task", "edit", "--id", taskID, "--priority", "high").OK(t)
		mo.LoreIn(ma, "task", "edit", "--id", taskID, "--title", "upgrade the queue client to v3").OK(t)
		s.NoPushSince(t, store, storeRefs, "two task edits")
		s.NoPushSince(t, app, appRefs, "two task edits")
		lead.LoreIn(la, "sync").OK(t)
		r := mo.LoreIn(ma, "sync")
		s.NamedFlags(t, r)
		lead.LoreIn(la, "sync")
		for name, m := range map[string]*journeys.Machine{"lead": lead, "mate": mo} {
			c := la
			if m == mo {
				c = ma
			}
			title, prio := j2Task(t, m.LoreIn(c, "task", "show", "--id", taskID, "--json").OK(t))
			if title != "upgrade the queue client to v3" || prio != "high" {
				t.Errorf("[J2] the %s reads the task as %q/%s after both synced: both changes (title by one, priority by the other) must be kept (B2-22)", name, title, prio)
			}
		}
	})

	s.Step("day3/mate-hand-edits-a-note-in-the-store-clone", func() {
		s.AdvanceDays(1)
		var file string
		for _, dir := range mo.StoreClones(t) {
			_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() && filepath.Base(p) == memID+".json" {
					file = p
				}
				return nil
			})
		}
		if file == "" {
			t.Fatalf("[J2] the team note %s is in none of the teammate's store clones %v", memID, mo.StoreClones(t))
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		edited := strings.Replace(string(b), "deploys go out on tuesdays", "deploys go out on tuesdays and thursdays", 1)
		if edited == string(b) {
			t.Fatalf("[J2] the note's file does not hold its body as plain text: %s", file)
		}
		if err := os.WriteFile(file, []byte(edited), 0o644); err != nil {
			t.Fatal(err)
		}
		clone := file
		for clone != "/" && !j2GitTop(clone) {
			clone = filepath.Dir(clone)
		}
		sc := mo.OpenClone(store, clone) // git runs as Mo: their HOME, identity and lore hooks
		sc.Git("commit", "-q", "-am", "hand edit: thursdays too")
		sc.Git("push", "-q")
		lead.LoreIn(la, "sync").OK(t)
		if got := j2Memories(t, lead.LoreIn(la, "memory", "list", "--json").OK(t))[memID]; got != "deploys go out on tuesdays and thursdays" {
			t.Errorf("[J2] the lead reads the hand-edited note as %q", got)
		}
		mo.LoreIn(ma, "doctor")
	})

	s.Step("week2/mate-sets-up-a-second-laptop", func() {
		s.AdvanceDays(5)
		desk := s.Personas()[1].NewLaptop("desk")
		dc := j2Laptop(t, s, desk, j2Mate, app)
		// a clone of a team repo on a machine that has not joined says how to join
		desk.LoreIn(dc, "whoami").OK(t).Says(t, "lore setup join --invite")
		journeys.Join(t, desk, dc.Dir, j2Setup, store.SSHURL(), j2Mate, []string{j2Folders})
		desk.LoreIn(dc, "whoami").OK(t).Says(t, j2Org, j2Mate.Email)
		if got := j2Memories(t, desk.LoreIn(dc, "memory", "list", "--json").OK(t))[memID]; got != "deploys go out on tuesdays and thursdays" {
			t.Errorf("[J2] the second laptop reads the team note as %q", got)
		}
		title, prio := j2Task(t, desk.LoreIn(dc, "task", "show", "--id", taskID, "--json").OK(t))
		if title != "upgrade the queue client to v3" || prio != "high" {
			t.Errorf("[J2] the second laptop reads the task as %q/%s", title, prio)
		}
	})
}

// j2GitTop reports whether dir is the top of a git work tree (has .git).
func j2GitTop(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}
