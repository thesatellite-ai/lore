//go:build journeys && !windows

package journeys

// fix_test.go — one command fixes a project (owner item 11, decisions
// D-ak, D-al): Dana opens three repos last touched by lore 0.1.17 (v1 row
// files, no migration). A read says exactly "run lore fix" and changes
// nothing; `lore fix` prints each step and HEALTHY at the end; a second run
// changes nothing and says everything is current; the first write in
// another old repo fixes it by itself with one line and then writes;
// `lore fix --all` goes over every repo in the setup's folder.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Fixture ids (the shapes of a lore 0.1.17 checkout).
const (
	fixProject = "prj_01a1cb57169c7c73a3a5dd7f0e1b8c6d"
	fixMemory  = "mem_01a1cb5743e371b7bab946c6cf489ddf"
)

// fixOldRepo writes a v0.1.17 project into c (v1 rows, its _meta.json)
// and commits it.
func fixOldRepo(t testing.TB, c *teamharness.Clone) {
	t.Helper()
	c.WriteFile(".lore/data/_meta.json", `{"_v": 1, "project_id": "`+fixProject+`"}`+"\n")
	c.WriteFile(".lore/data/projects/"+fixProject+".json", `{"_table": "projects", "_v": 1, "archived_at": null, "created_at": "2026-11-03T10:00:00.000000000Z", "id": "`+fixProject+
		`", "name": "widgets", "origin_url": null, "updated_at": "2026-11-03T10:00:00.000000000Z"}`+"\n")
	c.WriteFile(".lore/data/memories/"+fixMemory+".json", `{"_table": "memories", "_v": 1, "body": "the widget cache warms at boot", "created_at": "2026-11-03T10:00:01.000000000Z", "id": "`+fixMemory+
		`", "kind": "retrieved", "project_id": "`+fixProject+`", "source_kind": "manual", "trust_score": 0.5, "updated_at": "2026-11-03T10:00:01.000000000Z"}`+"\n")
	for _, rel := range []string{"_meta.json", "projects/" + fixProject + ".json", "memories/" + fixMemory + ".json"} {
		// a checkout gives committed files 0644; the harness writes 0600
		if err := os.Chmod(filepath.Join(c.Dir, ".lore", "data", filepath.FromSlash(rel)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c.CommitAll("lore 0.1.17 rows")
}

// fixV1Left reports whether a memory row file in c is still at _v: 1.
func fixV1Left(t testing.TB, c *teamharness.Clone) bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(c.Dir, ".lore", "data", "memories", fixMemory+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(b), `"_v": 1,`)
}

func TestJourneyUpgraderFixOldProjects(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	dana := w.Persona("Dana Reyes", "dana@widgets.example")
	lap := dana.Laptop()
	var one, two, three *teamharness.Clone

	w.Step("day1/three-old-repos", func() {
		one = lap.Clone(w.Remote("github.com/widgets/one"), "one")
		two = lap.Clone(w.Remote("github.com/widgets/two"), "two")
		three = lap.Clone(w.Remote("github.com/widgets/three"), "three")
		for _, c := range []*teamharness.Clone{one, two, three} {
			fixOldRepo(t, c)
		}
	})

	w.Step("day1/a-read-says-run-lore-migrate-and-changes-nothing", func() {
		before := one.Git("status", "--porcelain")
		lap.LoreIn(one, "memory", "list").OK(t).Says(t, "run lore migrate run", "the widget cache warms at boot")
		if after := one.Git("status", "--porcelain"); after != before || !fixV1Left(t, one) {
			t.Fatalf("a read changed the repo: before %q after %q", before, after)
		}
	})

	w.Step("day1/lore-fix-never-migrates-unasked", func() {
		// D15: the project's format is the rule; plain lore fix (and its dry
		// run) refuses a 0.1.17-format project with one line naming the
		// command, and changes nothing
		before := one.Git("status", "--porcelain")
		for _, args := range [][]string{{"fix"}, {"fix", "--dry-run"}} {
			r := lap.LoreIn(one, args...).Fails(t, 24, "E_FORMAT_OLD", "is on the lore 0.1.17 format; run lore migrate run to upgrade it (teammates need lore 0.1.18)")
			r.Never(t, "Upgraded")
		}
		if after := one.Git("status", "--porcelain"); after != before || !fixV1Left(t, one) {
			t.Fatalf("lore fix without --migrate changed the repo: before %q after %q", before, after)
		}
	})

	w.Step("day1/lore-fix-prints-each-step-and-healthy", func() {
		// a dry run changes nothing, so the status is today's: the ride-along
		// hook blocks a read never installs are a note in a clone lore never
		// set up (M-5: a fresh clone is not broken), so it is healthy
		lap.LoreIn(one, "fix", "--migrate", "--dry-run").OK(t).Says(t, "migrate", "Would upgrade 2 rows", "Nothing changed (dry run).", "Status: HEALTHY")
		if !fixV1Left(t, one) {
			t.Fatal("lore fix --dry-run migrated the rows")
		}
		r := lap.LoreIn(one, "fix", "--migrate").OK(t)
		r.Says(t, "lore fix: "+one.Dir, "migrate", "Upgraded 2 rows", "render", "agent hooks", "old clones", "doctor fixes", "Status: HEALTHY")
		if !strings.HasSuffix(strings.TrimSpace(r.Stdout), "Status: HEALTHY") {
			t.Fatalf("the status is not the last line:\n%s", r.Stdout)
		}
		if fixV1Left(t, one) {
			t.Fatal("lore fix left v1 rows")
		}
	})

	w.Step("day1/second-run-is-a-no-op", func() {
		lap.LoreIn(one, "fix").OK(t).Says(t, "Everything is current.", "Status: HEALTHY").Never(t, "Upgraded")
		lap.LoreIn(one, "doctor", "--fix").Says(t, "lore doctor --fix runs the same steps as lore fix")
	})

	w.Step("day1/a-write-in-another-old-repo-is-refused", func() {
		// D13: no write migrates by itself; it is refused with one line and
		// changes nothing until lore migrate run
		before := two.Git("status", "--porcelain")
		r := lap.LoreIn(two, "memory", "add", "--body", "the widget queue drains at noon")
		r.Fails(t, 24, "E_FORMAT_OLD", "run lore migrate run to upgrade it")
		if after := two.Git("status", "--porcelain"); after != before || !fixV1Left(t, two) {
			t.Fatalf("a refused write changed the repo: before %q after %q", before, after)
		}
	})

	var four *teamharness.Clone
	w.Step("day1/a-repo-without-a-lore-project", func() {
		// B1-7: a repo whose .lore/ holds only a LORE.md, with a committed
		// Claude Code settings file: lore fix --all must leave its tool
		// settings alone (a teammate without lore would get failing hooks)
		four = lap.Clone(w.Remote("github.com/widgets/four"), "four")
		four.WriteFile(".lore/LORE.md", "# notes\n")
		four.WriteFile(".claude/settings.local.json", "{}\n")
		// a checkout gives committed files 0644; the harness writes 0600
		if err := os.Chmod(filepath.Join(four.Dir, ".lore", "LORE.md"), 0o644); err != nil {
			t.Fatal(err)
		}
		four.CommitAll("no lore project")
	})

	w.Step("day1/fix-all-takes-named-folders", func() {
		// L-6: a lore 0.1.17 repo outside the home folder that no lore
		// command ran in is not found by --all (no setup folders yet)
		// until its folder is named on the command line
		five := lap.Clone(w.Remote("github.com/widgets/five"), "five")
		fixOldRepo(t, five)
		code := filepath.Join(filepath.Dir(lap.Home), "elsewhere", "code")
		if err := os.MkdirAll(code, 0o750); err != nil {
			t.Fatal(err)
		}
		lap.MoveClone(t, five, filepath.Join(code, "five"))
		lap.Lore("", "fix", "--all").OK(t).Never(t, "== "+five.Dir)
		if !fixV1Left(t, five) {
			t.Fatal("setup: the repo outside home was fixed without being named")
		}
		// D15: --all lists it with its command and leaves it as it is
		lap.Lore("", "fix", "--all", code).OK(t).Says(t, "== "+five.Dir, "is on the lore 0.1.17 format", "cd "+five.Dir+" && lore fix --migrate", "on the lore 0.1.17 format, not migrated").Never(t, "Upgraded")
		if !fixV1Left(t, five) {
			t.Fatal("lore fix --all migrated without --migrate")
		}
		lap.Lore("", "fix", "--all", "--migrate", code).OK(t).Says(t, "== "+five.Dir, "Upgraded 2 rows")
		if fixV1Left(t, five) {
			t.Fatal("lore fix --all <folder> left v1 rows in the repo under it")
		}
		lap.Lore("", "fix", "--all", filepath.Join(code, "missing")).Fails(t, 20, "is not a folder", "lore fix --all --dirs ~/code --dirs ~/work")
	})

	w.Step("day1/fix-all-over-the-setup-folder", func() {
		folder := filepath.Dir(three.Dir)
		cfg := filepath.Join(lap.Home, ".lore", "config.yaml")
		if err := os.WriteFile(cfg, []byte("setups:\n  - name: widgets\n    kind: solo\n    folders: [\""+folder+"/**\"]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		r := lap.Lore("", "fix", "--all", "--migrate").OK(t)
		r.Says(t, "== "+three.Dir, "== "+two.Dir, "== "+one.Dir, "none failed")
		if fixV1Left(t, three) {
			t.Fatal("lore fix --all left v1 rows in the third repo")
		}
		r.Says(t, "== "+four.Dir, "skipped: no lore project here")
		if st := four.Git("status", "--porcelain"); strings.Contains(st, ".claude") {
			t.Fatalf("lore fix --all changed the tool settings of a repo without a lore project:\n%s", st)
		}
		lap.Lore("", "fix", "--all").OK(t).Says(t, "Everything is current.").Never(t, "Upgraded")
	})
}
