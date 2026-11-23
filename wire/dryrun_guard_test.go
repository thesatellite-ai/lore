package wire

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Every write operation with --dry-run is in exactly one of the three lists
// below (devco:D-928, H-186): a dry run used to be each operation's own
// preview code, so it could pass where the write fails, and meta set --id
// … --dry-run wrote. Since then Run marks a write's preview check-only
// (engine/checkonly.go): every engine write runs all its checks and writes
// nothing.

// dryRunChecked are the writes whose --dry-run runs the real write path,
// check-only: invalid input fails with the write's own code, valid input
// writes nothing. dryRunCases proves the ones it names.
var dryRunChecked = map[string]string{
	"batch.apply":              "runs every line's real op in a stage it discards, check-only (ops/batch tests prove it)",
	"meta.set":                 "the one-row form (metaSetDry) runs the real write check-only after its own checks",
	"meta.unset":               "the one-row form (metaUnsetDry) runs the real write check-only after its own checks",
	"task.edit":                "--where runs every task's edit check-only",
	"task.delete":              "--where runs every root's delete check-only",
	"memory.delete":            "wheredel.Run: the real delete, check-only",
	"rule.delete":              "wheredel.Run: the real delete, check-only",
	"plan.delete":              "wheredel.Run: the real delete, check-only",
	"mission.delete":           "wheredel.Run: the real delete, check-only",
	"tasklist.delete":          "wheredel.Run: the real delete, check-only",
	"memory.archive":           "wheredel.Archive: every row's archive, check-only",
	"rule.archive":             "wheredel.Archive: every row's archive, check-only",
	"decision.archive":         "wheredel.Archive: every row's archive, check-only",
	"hotfix.archive":           "wheredel.Archive: every row's archive, check-only",
	"pattern.archive":          "wheredel.Archive (content): every row's archive, check-only",
	"playbook.archive":         "wheredel.Archive (content): every row's archive, check-only",
	"prompt.archive":           "wheredel.Archive (content): every row's archive, check-only",
	"snapshot.archive":         "wheredel.Archive (content): every row's archive, check-only",
	"architecturenote.archive": "wheredel.Archive (content): every row's archive, check-only",
	"behaviour.archive":        "wheredel.Archive (content): every row's archive, check-only",
	"cookbookrecipe.archive":   "wheredel.Archive (content): every row's archive, check-only",
	"incident.archive":         "wheredel.Archive (content): every row's archive, check-only",
	"suggestion.archive":       "wheredel.Archive (content): every row's archive, check-only",
	"tastepref.archive":        "wheredel.Archive (content): every row's archive, check-only",
	"techdoc.archive":          "wheredel.Archive (content): every row's archive, check-only",
	"workflow.archive":         "wheredel.Archive (content): every row's archive, check-only",
	"workspace.archive":        "wheredel.Archive (content): every row's archive, check-only",
}

// dryRunNotRows are writes whose dry run previews something other than a
// row write (git refs, workflow files, a code branch, the live overlay, a
// store's history): the engine's check-only mode does not reach them, so
// each keeps its own preview.
var dryRunNotRows = map[string]string{
	"branch.adopt":          "moves a code branch's layer rows by git ref",
	"branch.drop":           "drops a code branch's layer (git ref)",
	"branch.install_action": "writes a CI workflow file",
	"land.run":              "lands a branch layer (git)",
	"live.compact":          "rewrites refs/lore/live",
	"live.purge":            "rewrites refs/lore/live",
	"migrate.run":           "rewrites row files of an old format",
	"sync.ci_merge":         "merges store branches in CI (git)",
	"sync.install_action":   "writes a CI workflow file",
	"sync.purge":            "purges this machine's sync trash",
	"trash.purge":           "purges trashed rows for good (its own count, then engine.PurgeOlder)",
}

// dryRunLeftToConvert are row writes whose --dry-run still returns its
// own preview before the write path runs, so a dry run can still pass
// where the write fails. Each moves to dryRunChecked (with a case) when
// its dry run runs the write check-only. The engine already guarantees
// none of them writes in a dry run (Run's check-only context).
var dryRunLeftToConvert = map[string]string{
	"actor.merge":           "counts rows naming <from>",
	"member.add":            "lists who would be added",
	"meta.adopt":            "lists keys to re-file",
	"meta.edit":             "--type: reports what converts",
	"meta.import":           "lists what would be written",
	"meta.rebuild":          "lists what would change",
	"meta.rehome":           "lists keys and rows to rewrite",
	"mission.apply":         "lists the tasks it would write",
	"repo.merge_ids":        "plans the id merge",
	"resource.check":        "checks values, writes nothing",
	"resource.delete":       "bulk: lists what would change",
	"resource.edit":         "bulk: lists what would change",
	"resource.field.rename": "lists what would change",
	"resource.field.retype": "lists what would change",
	"resource.import":       "lists what would be written",
	"resource.merge":        "plans the merge",
	"resource.repoint":      "lists what would change",
	"row.move":              "--from: lists what would move",
	"sync.fix_projects":     "lists project rows to fix",
	"sync.promote":          "lists what would be promoted",
	"task.add":              "--template: lists the tasks",
	"task.import":           "lists what would be created (after the secret scan)",
	"task.next":             "ranks without claiming",
	"task.split":            "lists the split",
	"task.stale":            "--close: lists what would be cancelled",
	"trash.add":             "lists what would be trashed",
}

// TestEveryWriteDryRunIsListed walks the operation registry: every write
// with a dry_run input is in exactly one list above, and every name in a
// list is such a write. A new write with --dry-run lands in dryRunChecked
// (its dry run runs the write check-only) or says why not.
func TestEveryWriteDryRunIsListed(t *testing.T) {
	t.Parallel()
	a, err := New(Options{RepoDir: t.TempDir(), Home: t.TempDir(), FlagActor: testEmail, Wall: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	seen := map[string]bool{}
	for _, e := range a.Registry().Entries() {
		s := e.Spec()
		if s.Access != op.AccessWrite || s.Local || !slices.ContainsFunc(e.InputFields(), func(f op.FieldInfo) bool { return f.Name == op.InputDryRun }) {
			continue
		}
		seen[s.Name] = true
		n := 0
		for _, l := range []map[string]string{dryRunChecked, dryRunNotRows, dryRunLeftToConvert} {
			if _, ok := l[s.Name]; ok {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s is a write with --dry-run in %d of the lists: put it in exactly one (dryRunChecked when its dry run runs the write check-only)", s.Name, n)
		}
	}
	for _, l := range []map[string]string{dryRunChecked, dryRunNotRows, dryRunLeftToConvert} {
		for name := range l {
			if !seen[name] {
				t.Errorf("%s is listed but is not a write with --dry-run: take it out", name)
			}
		}
	}
}

// dryRunCase is one write of dryRunChecked run with and without --dry-run.
type dryRunCase struct {
	op   string
	seed func(t *testing.T, a *App) string // returns the id the inputs name as {{id}}
	bad  string                            // input the write refuses ("" = none)
	good string                            // input the write accepts
}

// dryRunCases prove dryRunChecked: the bad input fails with the same code
// with and without --dry-run, the good one with --dry-run leaves every
// file of the repo's store and every git ref byte-identical.
var dryRunCases = []dryRunCase{
	{op: "task.edit", seed: seedTask, bad: `{"where":["status:todo"],"priority":"bogus"}`, good: `{"where":["status:todo"],"priority":"high"}`},
	{op: "task.delete", seed: seedTask, good: `{"where":["status:todo"]}`},
	{op: "meta.set", seed: seedMemory, bad: `{"id":"{{id}}","pair":"state=x"}`, good: `{"id":"{{id}}","pair":"release_note=x"}`},
	{op: "meta.set", seed: seedStrictKey, bad: `{"id":"{{id}}","pair":"channel=fax"}`, good: `{"id":"{{id}}","pair":"channel=web"}`},
	{op: "meta.unset", seed: seedChannel, good: `{"id":"{{id}}","key":"channel"}`},
	{op: "memory.archive", seed: seedMemory, good: `{"where":["body~=feeds"]}`},
	{op: "memory.delete", seed: seedMemory, good: `{"where":["body~=feeds"]}`},
}

func seedTask(t *testing.T, a *App) string {
	t.Helper()
	out, err := run(t, a, "task.add", `{"title":"Ship the feed"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	return idOf(t, out)
}

// seedStrictKey is a memory and a strict key "channel" that takes only
// web or app: a value outside them is refused by the write's value check.
func seedStrictKey(t *testing.T, a *App) string {
	t.Helper()
	if _, err := run(t, a, "meta.add", `{"name":"channel","type":"text","strict":true,"values":"web,app"}`, true); err != nil {
		t.Fatal(err)
	}
	return seedMemory(t, a)
}

// seedChannel is seedStrictKey with channel=web set on the memory.
func seedChannel(t *testing.T, a *App) string {
	t.Helper()
	id := seedStrictKey(t, a)
	if _, err := run(t, a, "meta.set", `{"id":"`+id+`","pair":"channel=web"}`, true); err != nil {
		t.Fatal(err)
	}
	return id
}

func seedMemory(t *testing.T, a *App) string {
	t.Helper()
	out, err := run(t, a, "memory.add", `{"body":"The feeds refresh every ten minutes."}`, true)
	if err != nil {
		t.Fatal(err)
	}
	return idOf(t, out)
}

// TestDryRunFailsLikeTheWrite (devco:D-928, H-186): before, `task edit
// --where … --priority bogus --dry-run` said "would change 1 tasks" while
// the write failed E_INVALID_INPUT, and `meta set --id … --dry-run` wrote.
func TestDryRunFailsLikeTheWrite(t *testing.T) {
	t.Parallel()
	for _, c := range dryRunCases {
		t.Run(c.op, func(t *testing.T) {
			t.Parallel()
			if _, ok := dryRunChecked[c.op]; !ok {
				t.Fatalf("%s has a case but is not in dryRunChecked", c.op)
			}
			repo := gitRepo(t)
			a, err := New(Options{RepoDir: repo, Home: t.TempDir(), FlagActor: testEmail, Wall: time.Now})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			withProject(t, a)
			id := c.seed(t, a)
			in := func(s string, dry bool) string {
				s = strings.ReplaceAll(s, "{{id}}", id)
				if dry {
					s = strings.TrimSuffix(s, "}") + `,"dry_run":true}`
				}
				return s
			}
			if c.bad != "" {
				_, real := run(t, a, c.op, in(c.bad, false), true)
				_, dry := run(t, a, c.op, in(c.bad, true), false)
				if real == nil || dry == nil || errs.CodeOf(real) != errs.CodeOf(dry) {
					t.Errorf("bad input %s: write %v, dry run %v; want the same error code", c.bad, real, dry)
				}
			}
			before := treeSum(t, repo)
			out, err := run(t, a, c.op, in(c.good, true), false)
			if err != nil {
				t.Fatalf("dry run of good input %s: %v", c.good, err)
			}
			if after := treeSum(t, repo); !maps2Equal(before, after) {
				t.Errorf("dry run of %s wrote: %s (output %s)", c.good, treeDiff(before, after), out)
			}
			if _, err := run(t, a, c.op, in(c.good, false), true); err != nil {
				t.Errorf("the write of good input %s: %v", c.good, err)
			}
		})
	}
}

// treeSum is the sha256 of every file of the repo's store (.lore) and of
// its git refs, by path.
func treeSum(t *testing.T, repo string) map[string]string {
	t.Helper()
	sums := map[string]string{}
	for _, root := range []string{filepath.Join(repo, ".lore"), filepath.Join(repo, ".git", "refs")} {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, rerr := os.ReadFile(filepath.Clean(p))
			if rerr != nil {
				return rerr
			}
			h := sha256.Sum256(b)
			sums[strings.TrimPrefix(p, repo)] = hex.EncodeToString(h[:])
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	return sums
}

func maps2Equal(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// treeDiff names the paths that differ.
func treeDiff(a, b map[string]string) string {
	var d []string
	for k, v := range b {
		if a[k] != v {
			d = append(d, k)
		}
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			d = append(d, "-"+k)
		}
	}
	sort.Strings(d)
	j, _ := json.Marshal(d)
	return string(j)
}
