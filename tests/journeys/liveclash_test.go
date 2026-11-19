//go:build journeys && !windows

package journeys

// liveclash_test.go — two people retitle one task at once (devco D-219,
// finding L-19). Mei and Tom both edit the title, body and priority of the
// same live task before either syncs. The second sync used to write git
// conflict markers into the title and body and push them to the shared
// refs/lore/live: every clone listed a five-line title, and Freya, who
// never touched the task, could not sync (E_STALE) until someone ran
// `lore sync resolve`. Now the newer edit is kept, the sync that settles
// it names what it replaced, no marker reaches any row or the live ref,
// and every sync succeeds.

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// lcSlug is the placeholder remote of this journey.
const lcSlug = "github.com/umbrella-test/board"

// lcTaskID matches the id `task add` prints.
var lcTaskID = regexp.MustCompile(`tsk_[0-9a-f]+`)

// lcMarkers are the conflict marker shapes, raw and as JSON escapes.
var lcMarkers = []string{"<<<<<<<", ">>>>>>>", `\u003c\u003c\u003c`, `\u003e\u003e\u003e`}

func TestJourneyLiveTitleClash(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	freya := w.Persona("Freya Holm", "freya.holm@example.test").Laptop()
	mei := w.Persona("Mei Lin", "mei.lin@example.test").Laptop()
	tom := w.Persona("Tom Ash", "tom.ash@example.test").Laptop()
	remote := w.Remote(lcSlug)
	f := freya.Clone(remote, "board")
	var id string
	var m, tc *teamharness.Clone

	w.Step("day1/freya-starts-the-board", func() {
		f.WriteFile("README.md", "# board\n")
		freya.LoreIn(f, "init").OK(t)
		f.CommitAll("lore init")
		f.Git("push", "-q", "origin", "HEAD:main")
		r := freya.LoreIn(f, "task", "add", "Fixture task", "--commitment", "accepted").OK(t)
		if id = lcTaskID.FindString(r.Out()); id == "" {
			t.Fatalf("task add printed no id: %s", r)
		}
		freya.LoreIn(f, "sync").OK(t)
		m, tc = mei.Clone(remote, "board"), tom.Clone(remote, "board")
		mei.LoreIn(m, "sync").OK(t)
		tom.LoreIn(tc, "sync").OK(t)
		mei.LoreIn(m, "task", "show", id).OK(t).Says(t, "Fixture task")
	})

	w.Step("day2/both-retitle-it-then-everyone-syncs", func() {
		w.AdvanceDays(1)
		mei.LoreIn(m, "task", "edit", id, "--title", "Fixture task (Mei)", "--body", "line one from Mei\nline two", "--priority", "high").OK(t)
		w.Advance(time.Minute) // Tom's edit is the newer one
		tom.LoreIn(tc, "task", "edit", id, "--title", "Fixture task (Tom)", "--body", "line one from Tom\nline two", "--priority", "low").OK(t)
		mei.LoreIn(m, "sync").OK(t)
		r := tom.LoreIn(tc, "sync").OK(t)
		t.Logf("tom's sync:\n%s", r)
		r.Says(t, id, "kept the newer edit", `title was "Fixture task (Mei)"`, "priority was").Never(t, "conflict markers", "E_STALE")
		mei.LoreIn(m, "sync").OK(t).Never(t, "E_STALE")
		freya.LoreIn(f, "sync").OK(t).Never(t, "E_STALE", "conflict")
		for _, c := range []struct {
			m *Machine
			c *teamharness.Clone
		}{{freya, f}, {mei, m}, {tom, tc}} {
			got := c.m.LoreIn(c.c, "task", "show", id).OK(t)
			got.Says(t, "Fixture task (Tom)", "line one from Tom", "priority: low")
			for _, mk := range lcMarkers {
				if strings.Contains(got.Out(), mk) {
					t.Fatalf("%s shows a conflict marker:\n%s", c.c.Dir, got)
				}
			}
			c.m.LoreIn(c.c, "task", "list").OK(t).Never(t, lcMarkers...)
			c.m.LoreIn(c.c, "sync", "conflicts").OK(t).Says(t, "no conflicts")
		}
		for _, p := range remote.Files("refs/lore/live") {
			body, _ := remote.Read("refs/lore/live", p)
			for _, mk := range lcMarkers {
				if strings.Contains(body, mk) {
					t.Fatalf("the live ref holds a conflict marker in %s:\n%s", p, body)
				}
			}
		}
	})
}

// TestJourneyLiveStrictMetaClash (devco D-220): Mei and Tom set the strict
// metadata key "cost" of one live task to different values at once. A
// strict key is never settled silently, so the row keeps both values in a
// conflict entry and does not land; but that clash used to make the sync
// of everyone, Freya included, exit 23 E_STALE. Now every sync succeeds,
// the sync names the cause and the next step (`lore meta set --id <id>
// --pair cost=<value>`), and once Tom sets the key the conflict is gone.
func TestJourneyLiveStrictMetaClash(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	freya := w.Persona("Freya Holm", "freya.holm@example.test").Laptop()
	mei := w.Persona("Mei Lin", "mei.lin@example.test").Laptop()
	tom := w.Persona("Tom Ash", "tom.ash@example.test").Laptop()
	remote := w.Remote(lcSlug)
	f := freya.Clone(remote, "board")
	var id string
	var m, tc *teamharness.Clone

	w.Step("day1/freya-adds-a-strict-cost-key-and-a-task", func() {
		f.WriteFile("README.md", "# board\n")
		freya.LoreIn(f, "init").OK(t)
		freya.LoreIn(f, "meta", "add", "cost", "--type", "number", "--strict").OK(t)
		f.CommitAll("lore init")
		f.Git("push", "-q", "origin", "HEAD:main")
		r := freya.LoreIn(f, "task", "add", "Estimate the move", "--commitment", "accepted").OK(t)
		if id = lcTaskID.FindString(r.Out()); id == "" {
			t.Fatalf("task add printed no id: %s", r)
		}
		freya.LoreIn(f, "sync").OK(t)
		m, tc = mei.Clone(remote, "board"), tom.Clone(remote, "board")
		mei.LoreIn(m, "sync").OK(t)
		tom.LoreIn(tc, "sync").OK(t)
	})

	w.Step("day2/both-set-cost-then-everyone-syncs", func() {
		w.AdvanceDays(1)
		mei.LoreIn(m, "task", "edit", id, "--meta", "cost=4").OK(t)
		w.Advance(time.Minute)
		tom.LoreIn(tc, "task", "edit", id, "--meta", "cost=5").OK(t)
		mei.LoreIn(m, "sync").OK(t)
		r := tom.LoreIn(tc, "sync").OK(t)
		t.Logf("tom's sync:\n%s", r)
		r.Says(t, id, "meta.cost", "strict", "lore meta set --id "+id+" --pair cost=<value>").Never(t, "E_STALE", "text field")
		mei.LoreIn(m, "sync").OK(t).Never(t, "E_STALE")
		freya.LoreIn(f, "sync").OK(t).Never(t, "E_STALE")
		tom.LoreIn(tc, "sync", "conflicts").OK(t).Says(t, id, "meta.cost", `"4"`, `"5"`, "lore meta set --id "+id+" --pair cost=<value>")
		for _, p := range remote.Files("refs/lore/live") {
			body, _ := remote.Read("refs/lore/live", p)
			for _, mk := range lcMarkers {
				if strings.Contains(body, mk) {
					t.Fatalf("the live ref holds a conflict marker in %s:\n%s", p, body)
				}
			}
		}
	})

	w.Step("day2/tom-sets-the-cost", func() {
		tom.LoreIn(tc, "meta", "set", "--id", id, "--pair", "cost=5").OK(t)
		tom.LoreIn(tc, "sync").OK(t)
		for _, c := range []struct {
			m *Machine
			c *teamharness.Clone
		}{{freya, f}, {mei, m}, {tom, tc}} {
			c.m.LoreIn(c.c, "sync").OK(t)
			c.m.LoreIn(c.c, "sync", "conflicts").OK(t).Says(t, "no conflicts")
		}
	})
}

// TestJourneyLiveOffRowMerge (devco D-224): with live rows off
// (LORE_LIVE=0) every row is a file on the branch. Mei and Tom set the
// priority of one task in their own clones and commit; Tom's `git pull`
// merges the row through lore's merge driver (the newer edit wins; a text
// field both changed would keep both, as any git merge of row files does)
// instead of stopping on a git conflict in the _hlc and updated_at lines. Before, a run with live rows off never configured the
// driver, so the pull conflicted in .lore/data.
func TestJourneyLiveOffRowMerge(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	mei := w.Persona("Mei Lin", "mei.lin@example.test").Laptop()
	tom := w.Persona("Tom Ash", "tom.ash@example.test").Laptop()
	for _, m := range []*Machine{mei, tom} {
		m.SetEnv("LORE_LIVE", "0")
	}
	remote := w.Remote(lcSlug)
	m := mei.Clone(remote, "board")
	var id string
	var tc *teamharness.Clone

	w.Step("day1/mei-adds-a-task-as-a-file", func() {
		m.WriteFile("README.md", "# board\n")
		mei.LoreIn(m, "init").OK(t)
		r := mei.LoreIn(m, "task", "add", "Fixture task", "--commitment", "accepted").OK(t)
		if id = lcTaskID.FindString(r.Out()); id == "" {
			t.Fatalf("task add printed no id: %s", r)
		}
		m.CommitAll("board and a task")
		m.Git("push", "-q", "origin", "HEAD:main")
		tc = tom.Clone(remote, "board")
		tom.LoreIn(tc, "task", "show", id).OK(t).Says(t, "Fixture task")
	})

	w.Step("day2/both-set-priority-and-tom-pulls", func() {
		w.AdvanceDays(1)
		mei.LoreIn(m, "task", "edit", id, "--priority", "high").OK(t)
		m.CommitAll("mei raises it")
		m.Git("push", "-q", "origin", "HEAD:main")
		w.Advance(time.Minute) // Tom's edit is the newer one
		tom.LoreIn(tc, "task", "edit", id, "--priority", "low").OK(t)
		tc.CommitAll("tom lowers it")
		if drv := tc.Git("config", "--get", "merge.lore.driver"); !strings.Contains(drv, "merge-driver") {
			t.Fatalf("a write with live rows off wires the clone's merge driver: %q", drv)
		}
		if r := tc.TryGit("-c", "pull.rebase=false", "pull", "--no-edit", "origin", "main"); r.Code != 0 {
			t.Fatalf("git pull of two edits of one row must merge through lore's driver: %s\n%s", r, tc.Status())
		}
		tom.LoreIn(tc, "task", "show", id).OK(t).Says(t, "priority: low").Never(t, lcMarkers...)
		if st := tc.Status(); st != "" {
			t.Fatalf("the merge leaves a clean checkout: %q", st)
		}
	})
}
