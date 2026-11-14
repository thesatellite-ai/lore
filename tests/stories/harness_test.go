//go:build stories && !windows

package stories

// harness_test.go — the harness's own tests: every standing-rule check bites
// (a checker that never fails checks nothing), the fixture generators build
// the shapes they promise, and one short replay drives the real binary over
// generated repos the way every story does.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// gitInit makes an empty git repo at dir with the harness's made-up identity.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q", "-b", "main", dir}, {"-C", dir, "config", "user.email", "seed@example.test"}, {"-C", dir, "config", "user.name", "Seed Bot"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// TestHarnessTreeOfBites: a changed work-tree file, a new file, a changed
// .git/config and a new hook all show; git's own index refresh does not.
func TestHarnessTreeOfBites(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "brick")
	gitInit(t, dir)
	PlainRepo(t, dir)
	before := TreeOf(t, dir)
	if d := TreeDiff(before, TreeOf(t, dir)); len(d) != 0 {
		t.Fatalf("same tree differs: %v", d)
	}
	writeFile(t, filepath.Join(dir, ".git", "index"), "refreshed by git status")
	if d := TreeDiff(before, TreeOf(t, dir)); len(d) != 0 {
		t.Fatalf("an index refresh counted as a change: %v", d)
	}
	for name, change := range map[string]func(){
		"work-tree file": func() { appendFile(t, filepath.Join(dir, "README.md"), "x") },
		"new .lore":      func() { writeFile(t, filepath.Join(dir, ".lore", "data", "_meta.json"), "{}") },
		".git/config":    func() { appendFile(t, filepath.Join(dir, ".git", "config"), "[lore]\n") },
		"hook":           func() { writeFile(t, filepath.Join(dir, ".git", "hooks", "pre-commit"), "#!/bin/sh\n") },
		".git/info":      func() { appendFile(t, filepath.Join(dir, ".git", "info", "exclude"), ".lore/\n") },
	} {
		snap := TreeOf(t, dir)
		change()
		if d := TreeDiff(snap, TreeOf(t, dir)); len(d) == 0 {
			t.Errorf("TreeOf missed a change of %s", name)
		}
	}
}

// TestHarnessV017Shapes: the generator writes exactly the shape's rows, all
// in format 1 (NewFormatFiles empty), the big shape is 5.5k rows, and a
// single row in a newer format is caught.
func TestHarnessV017Shapes(t *testing.T) {
	t.Parallel()
	if n := HubShape.Rows(); n != 5500 {
		t.Fatalf("HubShape has %d rows, want the big repo's 5500", n)
	}
	dir := filepath.Join(t.TempDir(), "hub")
	gitInit(t, dir)
	start := time.Now()
	p := V017Repo(t, dir, "hub", HubShape)
	t.Logf("5500-row 0.1.17 repo generated in %v", time.Since(start).Round(time.Millisecond))
	if p.Count != HubShape.Rows() || len(p.Rows["tasks"]) != HubShape["tasks"] {
		t.Fatalf("generated %d rows (%d tasks), want %d (%d)", p.Count, len(p.Rows["tasks"]), HubShape.Rows(), HubShape["tasks"])
	}
	if bad := NewFormatFiles(t, dir); len(bad) != 0 {
		t.Fatalf("a fresh 0.1.17 repo has newer-format files: %v", bad[:min(3, len(bad))])
	}
	row := filepath.Join(dir, ".lore", "data", "tasks", p.Rows["tasks"][0]+".json")
	b, err := os.ReadFile(row)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("row is not JSON: %v", err)
	}
	m["_v"] = json.RawMessage("2")
	nb, _ := json.Marshal(m)
	writeFile(t, row, string(nb))
	if bad := NewFormatFiles(t, dir); len(bad) != 1 || !strings.HasSuffix(bad[0], p.Rows["tasks"][0]+".json") {
		t.Fatalf("NewFormatFiles = %v, want the one rewritten task", bad)
	}
}

// TestHarnessHintsAndSecretsBite: the named-flags and secret checks catch
// what they guard and pass the right forms.
func TestHarnessHintsAndSecretsBite(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{
		`run: lore task add "fix the cache"`,
		"next: lore task show tsk_01a14000000000001",
		"  lore memory edit 'x'",
	} {
		if len(PositionalHints(bad)) != 1 {
			t.Errorf("PositionalHints missed %q", bad)
		}
	}
	for _, good := range []string{
		`run: lore task add --title "fix the cache"`,
		"next: lore task show --id tsk_01a14000000000001",
		"lore migrate run",
		"lore sync",
	} {
		if b := PositionalHints(good); len(b) != 0 {
			t.Errorf("PositionalHints flagged the named form %q", good)
		}
	}
	sec := FakeSecret("bite")
	if !strings.HasPrefix(sec, "ghp_") || len(sec) != 40 {
		t.Fatalf("FakeSecret shape: len %d", len(sec))
	}
	rs := []teamharness.Result{
		{Args: []string{"memory", "show"}, Stdout: "body: " + sec},
		{Args: []string{"check", "leaks"}, Stderr: "found ****" + sec[len(sec)-12:]},
		{Args: []string{"task", "list"}, Stdout: "ghp_**** (masked)"},
	}
	if got := Printed(sec, rs...); len(got) != 2 {
		t.Fatalf("Printed caught %d of the 2 runs that printed the secret: %v", len(got), got)
	}
}

// TestHarnessReplay: one short story on the real binary over generated
// repos: a 0.1.17 project and a repo with no lore data are seeded on the
// fake GitHub, a person clones both and reads; the old project stays format
// 1, the brick stays byte-identical, and no remote ref moves.
func TestHarnessReplay(t *testing.T) {
	s := New(t, "harness")
	svc := s.Seed(s.Remote("github.com/acme/svc"), func(dir string) { V017Repo(t, dir, "svc", SmallShape) })
	brick := s.Seed(s.Remote("github.com/acme/brick"), func(dir string) { PlainRepo(t, dir) })
	dev := s.Person("Dev Harness", "dev.harness@example.test")
	var cs, cb *teamharness.Clone
	var svcRefs, brickRefs, brickTree string
	s.Step("day1/clone", func() {
		cs, cb = dev.Clone(svc, "svc"), dev.Clone(brick, "brick")
		svcRefs, brickRefs, brickTree = s.RefsOf(svc), s.RefsOf(brick), TreeOf(t, cb.Dir)
	})
	s.Step("day1/read", func() {
		r := dev.LoreIn(cs, "task", "list", "--all", "--json").OK(t)
		var out struct {
			Data []json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
			t.Fatalf("task list --json: %v\n%s", err, r)
		}
		if len(out.Data) != SmallShape["tasks"] {
			t.Errorf("task list shows %d tasks of the 0.1.17 project, want %d\n%s\nmigrate status: %s", len(out.Data), SmallShape["tasks"], r, dev.LoreIn(cs, "migrate", "status"))
		}
		s.NamedFlags(t, r, dev.LoreIn(cs, "migrate", "status"))
		dev.LoreIn(cb, "task", "list")
		dev.LoreIn(cb, "doctor")
	})
	s.Step("day1/rules", func() {
		if bad := NewFormatFiles(t, cs.Dir); len(bad) != 0 {
			t.Errorf("reads raised the 0.1.17 project's format: %v", bad)
		}
		s.Untouched(t, cb.Dir, brickTree, "task list and doctor")
		s.NoPushSince(t, svc, svcRefs, "reads")
		s.NoPushSince(t, brick, brickRefs, "reads")
	})
}
