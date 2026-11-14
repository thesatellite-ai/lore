//go:build stories && !windows

package stories

// j3_newuser_test.go — story J3, a new user from zero (tests/stories/J3-new-user.md):
// one person, no team, a code repo on the fake GitHub and a personal store in
// a bare repo on their own disk. The parts the journeys already walk (local
// path store, lists, new laptop, offline, pull --rebase, moved folders,
// remade store) are linked from the page, not repeated here.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// j3SpecIDs matches internal spec and task ids that once leaked into help text
// (B3-3, B3-6, B3-24, B3-26): "TP-14", "MQ11", "TQ3", "BATCH 7", "spec 4.3",
// "TASK 11".
var j3SpecIDs = regexp.MustCompile(`\b(TP-\d+|MQ\d+|TQ\d+|BATCH \d+|[Ss]pec \d+(\.\d+)?|TASK \d+)\b`)

// j3Body is the note text that must never appear in a commit subject.
const j3Body = "the train wifi drops every tunnel so retry with backoff"

// TestStoryJ3NewUser walks a new user's first week.
func TestStoryJ3NewUser(t *testing.T) {
	s := New(t, "J3")
	app := s.Seed(s.Remote("github.com/nia-dev/app"), func(dir string) {
		writeFile(t, filepath.Join(dir, "README.md"), "# app\n")
	})
	nia := s.Person("Nia New", "nia@example.test")
	bare := filepath.Join(s.Root, "disk", "nia-notes.git")
	s.Git(s.Root, "init", "-q", "--bare", "-b", "main", bare)
	me := &teamharness.Remote{Slug: "local/nia-notes", Dir: bare}
	var c *teamharness.Clone
	var appRefs string

	s.Step("day1/init", func() {
		c = nia.Clone(app, "app")
		appRefs = s.RefsOf(app)
		nia.LoreIn(c, "init").OK(t).Says(t, "lore initialized")
		if _, err := os.Stat(filepath.Join(c.Dir, ".lore", "data", "_meta.json")); err != nil {
			t.Errorf("lore init wrote no .lore/data/_meta.json: %v", err)
		}
		// The first commit of the new files goes through lore's hooks.
		c.CommitAll("add lore")
		if st := c.Status(); st != "" {
			t.Errorf("the hooks left changes after the first commit:\n%s", st)
		}
		s.NoPushSince(t, app, appRefs, "lore init and the first commit")
	})

	s.Step("day1/personal-store", func() {
		r := nia.LoreIn(c, "store", "init", "me", bare, "--yes").OK(t).Says(t, "created store me")
		s.NamedFlags(t, r)
		// B3-21: a store at a local path is cloned under stores/_local/<repo>,
		// not under its whole absolute path.
		if _, err := os.Stat(nia.LorePath("stores", "_local", "nia-notes")); err != nil {
			t.Errorf("the local store is not cloned at ~/.lore/stores/_local/nia-notes: %v", err)
		}
	})

	var memID string
	s.Step("day1/first-note", func() {
		before := s.RefsOf(me)
		r := nia.LoreIn(c, "memory", "add", "--body", j3Body, "--to", "me", "--json").OK(t)
		memID = j3ID(t, r)
		s.NoPushSince(t, me, before, "memory add --to me")
		nia.LoreIn(c, "sync").OK(t)
		if s.RefsOf(me) == before {
			t.Error("lore sync did not send the note to the personal store")
		}
		// B3-23: a commit subject never carries the note's text.
		for _, subj := range strings.Split(s.Git(bare, "log", "--all", "--format=%s"), "\n") {
			if strings.Contains(subj, "tunnel") || strings.Contains(subj, "backoff") {
				t.Errorf("a store commit subject carries the note's text: %q", subj)
			}
		}
	})

	s.Step("day2/delete-then-restore", func() {
		nia.LoreIn(c, "memory", "delete", "--id", memID, "--yes").OK(t)
		nia.LoreIn(c, "trash", "list").OK(t).Says(t, memID)
		nia.LoreIn(c, "trash", "restore", "--target", memID).OK(t)
		nia.LoreIn(c, "memory", "show", "--id", memID).OK(t).Says(t, "backoff")
	})

	s.Step("day2/rules", func() {
		keep := nia.LoreIn(c, "rule", "add", "--body", "run the tests before a push", "--json").OK(t)
		old := nia.LoreIn(c, "rule", "add", "--body", "use the old deploy script", "--json").OK(t)
		oldID := j3ID(t, old)
		nia.LoreIn(c, "rule", "archive", "--id", oldID).OK(t)
		// B3-31: --archived lists the archived rules, the plain list the others.
		nia.LoreIn(c, "rule", "list").OK(t).Says(t, j3ID(t, keep)).Never(t, oldID)
		nia.LoreIn(c, "rule", "list", "--archived").OK(t).Says(t, oldID)
	})

	s.Step("day3/help-reads-clean", func() {
		for _, args := range [][]string{
			{"store", "init", "--help"}, {"move", "--help"}, {"trash", "restore", "--help"},
			{"memory", "add", "--help"}, {"task", "add", "--help"}, {"tui", "--help"}, {"init", "--help"},
		} {
			r := nia.LoreIn(c, args...).OK(t)
			if m := j3SpecIDs.FindString(r.Stdout + r.Stderr); m != "" {
				t.Errorf("lore %s shows an internal spec id %q", strings.Join(args, " "), m)
			}
			s.NamedFlags(t, r)
		}
		// B3-17: the help of --to names the personal store.
		nia.LoreIn(c, "memory", "add", "--help").OK(t).Says(t, "me")
	})

	s.Step("day4/store-unreadable", func() {
		// B3-22: a store remote the person cannot read is named as such, not
		// as a missing repository at a wrong address.
		if err := os.Chmod(bare, 0); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chmod(bare, 0o755) }()
		nia.LoreIn(c, "memory", "add", "--body", "written while the disk was locked", "--to", "me").OK(t)
		r := nia.LoreIn(c, "sync")
		r.Never(t, "fix the address", "no git repository")
		// B3-12, B3-22: store doctor prints whole sentences, no fragment of
		// git's stderr under E_INVALID_INPUT, and names the real cause: the
		// folder cannot be read, the address is right.
		d := nia.LoreIn(c, "store", "doctor")
		d.Never(t, "E_INVALID_INPUT").Says(t, "permission denied", "the address is right")
	})

	s.Step("day4/store-back", func() {
		nia.LoreIn(c, "sync").OK(t)
		nia.LoreIn(c, "memory", "list", "--store", "me").OK(t).Says(t, "locked")
		s.NoPushSince(t, app, appRefs, "a week of personal notes")
	})

	s.Step("day5/forget-names-the-url", func() {
		// B3-30: store forget names a local store by the path the person typed,
		// not by an internal key (_local/<abs path>).
		other := filepath.Join(s.Root, "disk", "scratch.git")
		s.Git(s.Root, "init", "-q", "--bare", "-b", "main", other)
		nia.LoreIn(c, "store", "init", "scratch", other, "--yes").OK(t)
		nia.LoreIn(c, "store", "forget", "--url", "scratch", "--yes").OK(t).Says(t, "forgot "+other)
	})
}

// j3ID reads the id of a --json answer ({"id": …} or {"data": {"id": …}}).
func j3ID(t testing.TB, r teamharness.Result) string {
	t.Helper()
	m := regexp.MustCompile(`"id":\s*"([a-z]{3}_[0-9a-f]{32})"`).FindStringSubmatch(r.Stdout)
	if m == nil {
		t.Fatalf("no id in the --json answer: %s", r)
	}
	return m[1]
}
