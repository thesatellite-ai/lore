//go:build journeys && !windows

package journeys

// fix_big_test.go — a big lore 0.1.17 repo (bugs B1-10, B1-12, B1-16).
// Dana's main repo holds thousands of rows. Her first migration is stopped
// half way (killed between two tables); the next `lore fix` takes over the
// dead run's lock, resumes and finishes it without redoing the rows
// already rewritten. Until she commits the migration, every row file is
// modified in her work tree: a read there picks none of them up as a hand
// edit (the migration's own changes, an empty ref written as null, are not
// her edits) and answers promptly.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// bigTasks is how many v0.1.17 task rows the big repo holds.
const bigTasks = 1500

// bigTaskID is the id of the i-th generated task (made-up, valid ids).
func bigTaskID(i int) string { return fmt.Sprintf("tsk_01a1cb5743e371b7bab946c6%08x", i) }

// bigMemoryID is the id of the i-th generated memory.
func bigMemoryID(i int) string { return fmt.Sprintf("mem_01a1cb5743e371b7bab946c6%08x", i) }

// fixBigOldRepo writes a v0.1.17 project with bigTasks tasks and as many
// memories (every other one with v0.1.17's "" for "no tasklist" or "not
// superseded", which the migration writes as null), and commits it.
func fixBigOldRepo(t testing.TB, c *teamharness.Clone) {
	t.Helper()
	c.WriteFile(".lore/data/_meta.json", `{"_v": 1, "project_id": "`+fixProject+`"}`+"\n")
	c.WriteFile(".lore/data/projects/"+fixProject+".json", `{"_table": "projects", "_v": 1, "archived_at": null, "created_at": "2026-11-03T10:00:00.000000000Z", "id": "`+fixProject+
		`", "name": "widgets", "origin_url": null, "updated_at": "2026-11-03T10:00:00.000000000Z"}`+"\n")
	for i := range bigTasks {
		list := ""
		if i%2 == 0 {
			list = `"tasklist_id": "", `
		}
		c.WriteFile(".lore/data/tasks/"+bigTaskID(i)+".json", `{"_table": "tasks", "_v": 1, "commitment": "accepted", "created_at": "2026-11-03T10:00:02.000000000Z", "id": "`+bigTaskID(i)+
			`", "priority": "medium", "project_id": "`+fixProject+`", "status": "todo", `+list+`"title": "widget step `+fmt.Sprint(i)+`", "updated_at": "2026-11-03T10:00:02.000000000Z"}`+"\n")
		c.WriteFile(".lore/data/memories/"+bigMemoryID(i)+".json", `{"_table": "memories", "_v": 1, "body": "widget note `+fmt.Sprint(i)+`", "created_at": "2026-11-03T10:00:01.000000000Z", "id": "`+bigMemoryID(i)+
			`", "kind": "retrieved", "project_id": "`+fixProject+`", "source_kind": "manual", `+strings.Replace(list, "tasklist_id", "superseded_by_id", 1)+`"trust_score": 0.5, "updated_at": "2026-11-03T10:00:01.000000000Z"}`+"\n")
	}
	fixCheckoutMode(t, c, ".lore/data/_meta.json")
	fixCheckoutMode(t, c, ".lore/data/projects/"+fixProject+".json")
	for i := range bigTasks {
		fixCheckoutMode(t, c, ".lore/data/tasks/"+bigTaskID(i)+".json")
		fixCheckoutMode(t, c, ".lore/data/memories/"+bigMemoryID(i)+".json")
	}
	c.Git("add", "-A")
	c.Git("commit", "-q", "--no-verify", "-m", "lore 0.1.17 rows")
}

func TestJourneyUpgraderFixBigRepo(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	dana := w.Persona("Dana Reyes", "dana@widgets.example")
	lap := dana.Laptop()
	var c *teamharness.Clone

	w.Step("day1/a-big-old-repo", func() {
		c = lap.Clone(w.Remote("github.com/widgets/big"), "big")
		fixBigOldRepo(t, c)
	})

	w.Step("day1/the-first-migration-is-killed-half-way", func() {
		lap.SetEnv("LORE_TEST_MIGRATE_CRASH_AT", "table:1") // killed as kill -9 after the first table
		r := lap.LoreIn(c, "migrate", "run", "--no-push")
		lap.SetEnv("LORE_TEST_MIGRATE_CRASH_AT", "")
		if r.Code == 0 {
			t.Fatalf("the crash point did not stop the run: %s", r.String())
		}
		if lock, ok := c.ReadFile(".lore/state/migrate.lock"); !ok || strings.TrimSpace(lock) == "" {
			t.Fatal("the killed run left no lock to take over")
		}
	})

	w.Step("day1/lore-fix-takes-over-and-finishes", func() {
		start := time.Now()
		r := lap.LoreIn(c, "fix", "--yes")
		r.Says(t, "resuming the interrupted migration").Never(t, "E_MIGRATE_LOCKED", "picked up a hand edit")
		t.Logf("lore fix on %d rows took %s", 2*bigTasks, time.Since(start).Round(time.Millisecond))
		for i := range bigTasks {
			for _, rel := range []string{".lore/data/tasks/" + bigTaskID(i) + ".json", ".lore/data/memories/" + bigMemoryID(i) + ".json"} {
				if doc, _ := c.ReadFile(rel); !strings.Contains(doc, `"_v": 2`) {
					t.Fatalf("%s is not at _v 2 after the resumed run", rel)
				}
			}
		}
		lap.LoreIn(c, "fix").Never(t, "Upgraded", "resuming") // nothing left to migrate
	})

	w.Step("day1/a-read-before-the-migration-commit-picks-up-nothing", func() {
		before := c.Git("status", "--porcelain")
		start := time.Now()
		lap.LoreIn(c, "task", "list").Says(t, "widget step").Never(t, "hand edit")
		lap.LoreIn(c, "memory", "list").Says(t, "widget note").Never(t, "hand edit")
		t.Logf("task list with %d modified row files took %s", strings.Count(before, "\n")+1, time.Since(start).Round(time.Millisecond))
		if after := c.Git("status", "--porcelain"); after != before {
			t.Fatal("a read changed row files of the uncommitted migration")
		}
	})
}
