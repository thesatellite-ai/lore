//go:build stories && !windows

package stories

// G_months_of_use_test.go — story G (tests/stories/G-months-of-use.md): a
// team uses lore for months. Three parts that run in parallel: volume,
// search and lifecycle with a docs-only repo (TestStoryGMonthsOfUse);
// people, stores and repos renamed (TestStoryGRenames); many agent sessions
// writing at once (TestStoryGManyAgents). Steps whose fix has not landed
// are written as they should behave and skipped with their finding id.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// The repos of story G (placeholders).
const (
	gAPISlug  = "github.com/initrode/orders-api"
	gLibSlug  = "github.com/initrode/retrykit"
	gDocsSlug = "github.com/initrode/handbook"
	gSiteSlug = "github.com/initrode/site"
)

// gTeam starts the story's code repo with lore adopted by the CTO and
// pushed, and gives each person a clone.
func gTeam(t *testing.T, s *Story, api *teamharness.Remote, people ...[2]string) []gMember {
	t.Helper()
	var ms []gMember
	for i, p := range people {
		m := s.Person(p[0], p[1])
		c := m.Clone(api, "orders-api")
		if i == 0 {
			m.LoreIn(c, "init").OK(t)
			c.CommitAll("lore: adopt")
			c.Git("push", "-q", "origin", "HEAD:main")
		} else {
			c.Git("pull", "-q", "--rebase", "origin", "main")
		}
		ms = append(ms, gMember{Name: p[0], Email: p[1], M: m, C: c})
	}
	return ms
}

func TestStoryGMonthsOfUse(t *testing.T) {
	s := New(t, "G")
	api := s.Seed(s.Remote(gAPISlug), func(dir string) { PlainRepo(t, dir) })
	lib := s.Seed(s.Remote(gLibSlug), func(dir string) { V017Repo(t, dir, "retrykit", SmallShape) })
	docs := s.Seed(s.Remote(gDocsSlug), func(dir string) { DocsOnly(t, dir) })
	site := s.Seed(s.Remote(gSiteSlug), func(dir string) { PlainRepo(t, dir) })
	var team []gMember
	var sc, lc *teamharness.Clone
	var siteTree, siteRefs, libRefs string
	vol := gVolume{Tasks: 12, Memories: 6, Decisions: 3, Plans: 2}
	const naive = "Chloé: the naïve cache warms on every deploy"
	const rocket = "release train leaves on fridays 🚀"

	s.Step("day1/the-team-and-its-repos", func() {
		team = gTeam(t, s, api, [2]string{"Ines Ortega", "ines@example.test"}, [2]string{"Chloé Durand", "chloe@example.test"}, [2]string{"Zoë Brandt", "zoe@example.test"})
		sc = team[0].M.Clone(site, "site")
		lc = team[0].M.Clone(lib, "retrykit")
		siteTree, siteRefs, libRefs = TreeOf(t, sc.Dir), s.RefsOf(site), s.RefsOf(lib)
	})

	s.Step("month1/three-people-write-for-weeks", func() {
		for i, p := range team {
			s.AdvanceDays(7)
			var extra []string
			if i == 1 {
				extra = []string{naive, rocket}
			}
			gWrite(t, p, vol, extra...)
			gShare(t, p, fmt.Sprintf("lore: week %d notes", i+1))
		}
		for _, p := range team {
			gPull(t, p)
		}
	})

	s.Step("month1/no-write-is-lost", func() {
		n := len(team)
		for _, p := range team {
			if got := gCount(t, p.M.LoreIn(p.C, "task", "list", "--all", "--json").OK(t)); got != n*vol.Tasks {
				t.Errorf("%s sees %d tasks, want %d", p.Name, got, n*vol.Tasks)
			}
			if got := gCount(t, p.M.LoreIn(p.C, "memory", "list", "--json").OK(t)); got != n*vol.Memories+2 {
				t.Errorf("%s sees %d memories, want %d", p.Name, got, n*vol.Memories+2)
			}
		}
	})

	s.Step("month2/search-finds-what-people-wrote", func() {
		s.AdvanceDays(14)
		ines := team[0]
		// accents fold both ways
		ines.M.LoreIn(ines.C, "search", "--query", "Chloe").OK(t).Says(t, "naïve cache")
		ines.M.LoreIn(ines.C, "search", "--query", "NAÏVE").OK(t).Says(t, "naïve cache")
		ines.M.LoreIn(ines.C, "search", "--query", "naive").OK(t).Says(t, "naïve cache")
		ines.M.LoreIn(ines.C, "search", "--query", "🚀").OK(t).Says(t, "release train")
		ines.M.LoreIn(ines.C, "search", "--query", "\"tidy the handler\"").OK(t).Says(t, "task")
		// D-135: a memory search with no hits says so
		ines.M.LoreIn(ines.C, "memory", "search", "--query", "zeppelin").OK(t).Says(t, "(no matches)")
		// paging past the end says so instead of printing nothing
		if r := ines.M.LoreIn(ines.C, "task", "list", "--limit", "10", "--page", "99").OK(t); strings.TrimSpace(r.Out()) == "" {
			t.Errorf("task list past the last page printed nothing")
		}
	})

	s.Step("month2/rows-read-through-from-the-fetched-default-branch-are-found", func() {
		// D-134: Zoë pushes a note; Ines has fetched but not pulled. The
		// list shows the note (read through from origin/main) and search
		// must find it as well.
		zoe, ines := team[2], team[0]
		zoe.M.LoreIn(zoe.C, "memory", "add", "the retry budget is per tenant, not per host").OK(t)
		gShare(t, zoe, "lore: retry budget")
		ines.C.Git("fetch", "-q", "origin")
		ines.M.LoreIn(ines.C, "memory", "list", "--limit", "300").OK(t).Says(t, "retry budget")
		ines.M.LoreIn(ines.C, "search", "--query", "tenant").OK(t).Says(t, "retry budget")
		gPull(t, ines)
	})

	s.Step("month3/lifecycle", func() {
		s.AdvanceDays(30)
		ines := team[0]
		ines.M.LoreIn(ines.C, "reminder", "add", "--message", "rotate the queue credentials", "--due", s.Now().AddDate(0, 0, -3).Format("2006-01-02")).OK(t)
		ines.M.LoreIn(ines.C, "reminder", "list").OK(t).Says(t, "rotate the queue credentials")
		t.Run("overdue-reminders-in-inbox", func(t *testing.T) {
			ines.M.LoreIn(ines.C, "inbox").OK(t).Says(t, "rotate the queue credentials", "overdue")
		})
		t.Run("overdue-reminders-in-render", func(t *testing.T) {
			ines.M.LoreIn(ines.C, "render").OK(t)
			if b, _ := ines.C.ReadFile(".lore/LORE.md"); !strings.Contains(b, "rotate the queue credentials") {
				t.Errorf("LORE.md does not name the overdue reminder")
			}
		})
		t.Run("bulk-archive", func(t *testing.T) {
			ines.M.LoreIn(ines.C, "memory", "archive", "--where", "body~=queue drains", "--yes").OK(t)
			if got := gCount(t, ines.M.LoreIn(ines.C, "memory", "list", "--json").OK(t)); got > 4 {
				t.Errorf("bulk archive left %d memories", got)
			}
		})
		t.Run("trash-purge-is-fast", func(t *testing.T) {
			start := time.Now()
			ines.M.LoreIn(ines.C, "trash", "purge", "--older-than", "0d", "--confirm").OK(t)
			if d := time.Since(start); d > 5*time.Second {
				t.Errorf("trash purge took %s", d)
			}
		})
		t.Run("task-stale-days-zero", func(t *testing.T) {
			// G-20: 0 is refused with the floor, never read as "not given"
			// (which ran the 60-day default)
			ines.M.LoreIn(ines.C, "task", "stale", "--days", "0").Fails(t, 20, "--days must be 1 or more").Never(t, "60 days")
		})
		t.Run("doctor-quiet-on-a-small-LORE.md", func(t *testing.T) {
			ines.M.LoreIn(ines.C, "doctor").Never(t, "of 32000 bytes")
		})
		t.Run("must-rules-over-budget-keep-their-text", func(t *testing.T) {
			// 80 must rules of ~450 bytes each: well over the 32000-byte budget
			pad := strings.Repeat(" because the on-call engineer greps the logs by it", 8)
			for i := 0; i < 80; i++ {
				ines.M.LoreIn(ines.C, "rule", "add", "--body", fmt.Sprintf("must rule %02d: every handler logs its request id%s", i, pad), "--severity", "must").OK(t)
			}
			ines.M.LoreIn(ines.C, "render").OK(t)
			if b, _ := ines.C.ReadFile(".lore/LORE.md"); !strings.Contains(b, "must rule 79") {
				t.Errorf("an over-budget LORE.md drops the must rules' text")
			}
		})
	})

	s.Step("month3/a-docs-only-repo", func() {
		ines := team[0]
		dc := ines.M.Clone(docs, "handbook")
		dc.WriteFile("CLAUDE.md", "# Handbook\n\nWrite in plain words.\n")
		dc.CommitAll("docs: agent notes")
		ines.M.LoreIn(dc, "init").OK(t)
		ines.M.LoreIn(dc, "rule", "add", "--body", "every page names its owner").OK(t)
		ines.M.LoreIn(dc, "render").OK(t)
		if b, _ := dc.ReadFile("CLAUDE.md"); !strings.Contains(b, "Write in plain words.") {
			t.Errorf("init or render dropped the handbook's own CLAUDE.md text:\n%s", b)
		}
		dc.CommitAll("lore: adopt")
		// markdown-only commits stay quick with lore's hooks in place
		dc.WriteFile("pages/onboarding.md", "# Onboarding\n\nAsk for a buddy on day one.\n")
		start := time.Now()
		dc.CommitAll("docs: onboarding")
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("a markdown-only commit took %s with lore's hooks", d)
		}
		if st := dc.Git("status", "--porcelain"); st != "" {
			t.Errorf("the docs repo is left modified after a commit:\n%s", st)
		}
	})

	s.Step("month3/standing-rules", func() {
		ines := team[0]
		ines.M.LoreIn(lc, "task", "list").OK(t)
		ines.M.LoreIn(sc, "task", "list")
		ines.M.LoreIn(sc, "doctor")
		if bad := NewFormatFiles(t, lc.Dir); len(bad) != 0 {
			t.Errorf("reads raised the 0.1.17 library's format: %v", bad)
		}
		s.Untouched(t, sc.Dir, siteTree, "months of use")
		s.NoPushSince(t, site, siteRefs, "months of use")
		s.NoPushSince(t, lib, libRefs, "months of use")
	})
}

// TestStoryGRenames: people, teams, stores and repos change names and
// addresses; every note is still found and nobody loses access.
func TestStoryGRenames(t *testing.T) {
	s := New(t, "G")
	api := s.Seed(s.Remote(gAPISlug), func(dir string) { PlainRepo(t, dir) })
	var team []gMember
	var memID string

	s.Step("day1/notes-before-the-renames", func() {
		team = gTeam(t, s, api, [2]string{"Ines Ortega", "ines@example.test"}, [2]string{"Chloé Durand", "chloe@example.test"})
		chloe := team[1]
		r := chloe.M.LoreIn(chloe.C, "memory", "add", "the export job runs at 02:00 UTC").OK(t)
		memID = gMemID.FindString(r.Stdout)
		if memID == "" {
			t.Fatalf("memory add printed no id: %s", r)
		}
		gShare(t, chloe, "lore: export note")
		gPull(t, team[0])
	})

	s.Step("month1/a-person-changes-email-and-name", func() {
		s.AdvanceDays(30)
		chloe := team[1]
		s.Personas()[1].Rename(t, "Chloé Martin", "chloe.martin@example.test")
		chloe.M.LoreIn(chloe.C, "memory", "show", "--id", memID).OK(t).Says(t, "export job")
		chloe.M.LoreIn(chloe.C, "search", "--query", "export").OK(t).Says(t, memID)
		chloe.M.LoreIn(chloe.C, "memory", "add", "the export job retries twice").OK(t)
		t.Run("whoami-shows-one-actor", func(t *testing.T) {
			r := chloe.M.LoreIn(chloe.C, "whoami").OK(t)
			if n := len(fActorID.FindAllString(r.Stdout, -1)); n > 1 {
				t.Errorf("whoami names %d actors after an email change: %s", n, r)
			}
		})
	})

	s.Step("month2/the-repo-moves-to-a-new-address", func() {
		s.AdvanceDays(30)
		t.Run("writes-keep-working-after-a-remote-rename", func(t *testing.T) {
			ines := team[0]
			moved := s.Remote("github.com/initrode/orders-service")
			// the host renames the repo: the old address is gone and the new
			// one holds its history (Remote made an empty one there first)
			if err := os.RemoveAll(moved.Dir); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(api.Dir, moved.Dir); err != nil {
				t.Fatal(err)
			}
			ines.C.Git("remote", "set-url", "origin", moved.URL())
			ines.M.LoreIn(ines.C, "repo", "set-url", "--url", moved.URL()).OK(t)
			ines.M.LoreIn(ines.C, "task", "add", "--title", "after the move").OK(t).Never(t, "deleted or rewritten")
			ines.M.LoreIn(ines.C, "sync").OK(t).Never(t, "E_STALE")
		})
	})

	s.Step("month3/team-and-org-store-renames", func() {
		t.Run("team-tasks-reach-members-after-a-team-rename", func(t *testing.T) {
			t.Skip("waiting for r3 G-15")
		})
		t.Run("org-store-moved-sync-names-set-url", func(t *testing.T) {
			t.Skip("waiting for r3 G-16")
		})
		t.Run("old-store-clone-named-after-set-url", func(t *testing.T) {
			t.Skip("waiting for r3 G-19")
		})
	})

	s.Step("after/every-note-still-found", func() {
		for _, p := range team {
			p.M.LoreIn(p.C, "memory", "show", "--id", memID).OK(t).Says(t, "export job")
		}
	})
}

// TestStoryGManyAgents: agent sessions write in one clone at the same
// instant; no write is lost. The claim races of round 3 wait for their fix.
func TestStoryGManyAgents(t *testing.T) {
	s := New(t, "G")
	api := s.Seed(s.Remote(gAPISlug), func(dir string) { PlainRepo(t, dir) })
	const agents = 4
	var team []gMember
	var task string
	var refs string

	s.Step("day1/a-repo-and-its-tasks", func() {
		team = gTeam(t, s, api, [2]string{"Ines Ortega", "ines@example.test"})
		ines := team[0]
		for i := 0; i < 6; i++ {
			r := ines.M.LoreIn(ines.C, "task", "add", "--title", fmt.Sprintf("queue task %02d", i)).OK(t)
			if i == 0 {
				task = fTaskOf(t, r)
			}
		}
		refs = s.RefsOf(api)
	})

	s.Step("day1/agents-add-and-comment-at-once", func() {
		ines := team[0]
		var adds, comments []*teamharness.Pending
		for i := 0; i < agents; i++ {
			c := ines.C.As(ines.M.Agent(fmt.Sprintf("agent-%d", i)))
			adds = append(adds, c.Prepare("task", "add", "--title", fmt.Sprintf("agent %d follow-up", i), "--commitment", "accepted"))
			comments = append(comments, c.Prepare("comment", "add", "--on-table", "tasks", "--on-id", task, "--body", fmt.Sprintf("agent %d looked at it", i)))
		}
		for _, r := range s.Simultaneously(adds...) {
			r.OK(t)
		}
		for _, r := range s.Simultaneously(comments...) {
			r.OK(t)
		}
		list := ines.M.LoreIn(ines.C, "task", "list", "--all").OK(t)
		for i := 0; i < agents; i++ {
			list.Says(t, fmt.Sprintf("agent %d follow-up", i))
		}
		show := ines.M.LoreIn(ines.C, "task", "show", "--id", task).OK(t)
		for i := 0; i < agents; i++ {
			show.Says(t, fmt.Sprintf("agent %d looked at it", i))
		}
		s.NoPushSince(t, api, refs, "agent writes without --push")
	})

	s.Step("day1/claim-races", func() {
		ines := team[0]
		t.Run("task-next-never-hands-out-a-done-task", func(t *testing.T) {
			var next []*teamharness.Pending
			for i := 0; i < agents; i++ {
				c := ines.C.As(ines.M.Agent(fmt.Sprintf("agent-%d", i)))
				next = append(next, c.Prepare("task", "next", "--push", "--yes", "--json"))
			}
			seen := map[string]bool{}
			for _, r := range s.Simultaneously(next...) {
				id := fTaskID.FindString(r.Stdout)
				if id != "" && seen[id] {
					t.Errorf("task next handed %s to two agents", id)
				}
				seen[id] = true
			}
		})
		t.Run("concurrent-done-writes-no-conflict-markers", func(t *testing.T) {
			var done []*teamharness.Pending
			for i := 0; i < agents; i++ {
				c := ines.C.As(ines.M.Agent(fmt.Sprintf("agent-%d", i)))
				done = append(done, c.Prepare("task", "done", "--id", task, "--no-lesson", "--yes"))
			}
			s.Simultaneously(done...)
			files, _ := filepath.Glob(filepath.Join(ines.C.Dir, ".lore", "data", "tasks", "*.json"))
			for _, f := range files {
				if b, _ := os.ReadFile(f); strings.Contains(string(b), "<<<<<<<") {
					t.Errorf("%s holds conflict markers", f)
				}
			}
			ines.M.LoreIn(ines.C, "sync").OK(t)
		})
		t.Run("every-command-under-five-seconds", func(t *testing.T) {
			t.Skip("waiting for D-159")
		})
		t.Run("task-add-help-matches-what-agents-must-pass", func(t *testing.T) {
			ines.M.LoreIn(ines.C, "task", "add", "--help").OK(t).Never(t, "defaults to accepted")
		})
	})
}
