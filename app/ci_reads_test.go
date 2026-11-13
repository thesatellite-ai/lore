package app

// ci_reads_test.go — C-8, a CI clone's read-only commands: a fresh clone
// of a repo lore is set up in (row files committed, nothing wired in this
// clone) runs every read and check a CI job runs, and none of them writes
// inside .git (no hook, no config key, no info/exclude) or the work tree.
// `lore render --check` once wired the merge driver and seven hook blocks
// before it checked, and `lore migrate status` appended to info/exclude.
//
// The reads come from the operation registry (op.Spec.OnlyReads), so a new
// read is covered the day it is added; the read forms of write commands
// (render --check/--report/--trend/--dry-run, doctor, fix --dry-run) are
// named. .git/lore is left out of the comparison: it is lore's own runtime
// folder (the read fetch throttle and the live lock, `lore where`), never
// read by git; so is FETCH_HEAD, which a read's fetch rewrites as git does.

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/untouchedwalk"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
)

// ciReadForms are the read forms of commands that also write.
var ciReadForms = [][]string{
	{"doctor"}, {"doctor", "--json"},
	{"render", "--check"}, {"render", "--report"}, {"render", "--trend"}, {"render", "--dry-run"},
	{"fix", "--dry-run"},
}

// ciRuntime are the .git paths a read may change: lore's runtime folder
// and git's own fetch record (see the file comment).
func ciRuntime(rel string) bool {
	rel = filepath.ToSlash(rel)
	return rel == ".git/lore" || strings.HasPrefix(rel, ".git/lore/") || rel == ".git/FETCH_HEAD"
}

// ciTree is untouchedwalk.Tree of dir without ciRuntime.
func ciTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	tree := untouchedTree(t, dir)
	for p := range tree {
		if ciRuntime(p) {
			delete(tree, p)
		}
	}
	return tree
}

// TestCIReadsWriteNothingInGit: see the file comment.
func TestCIReadsWriteNothingInGit(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person().unscoped().author().hermeticPath(t)
	w.setAll(map[string]string{"EDITOR": "true", "VISUAL": "true", "BROWSER": "true"})
	a := w.pushedLore(t, "a")
	w.mustLore(t, a, "memory", "add", "--body", "the build cache lives in the runner's tmp")
	w.mustLore(t, a, "rule", "add", "--body", "never log a request body")
	w.git(t, a, "add", "-A")
	w.git(t, a, "commit", "-q", "--allow-empty", "-m", "rows")
	w.git(t, a, "push", "-q", "origin", "HEAD:main")
	ci := w.path("ci")
	w.git(t, w.root, "clone", "-q", w.path("remote.git"), ci)

	cmds := slices.Clone(ciReadForms)
	entries := untouchedEntries(t)
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		e := entries[k]
		path := strings.Fields(k)
		// lore sync carries read permission but is the explicit command
		// that sets a clone up for git (wire/host readSpec): not a read
		if !e.Spec().OnlyReads() || e.Spec().Hidden || e.Spec().Name == opssync.OpRun {
			continue
		}
		if skip, _ := untouchedwalk.Skipped(path); skip && !untouchedwalk.Walked(path, e) {
			continue
		}
		cmds = append(cmds, untouchedwalk.Args(path, e))
	}
	if len(cmds) < 60 {
		t.Fatalf("only %d read commands found: the registry walk is broken", len(cmds))
	}
	before := ciTree(t, ci)
	for _, args := range cmds {
		_, out, errOut := w.loreWithin(t, ci, untouchedDeadline, args...)
		after := ciTree(t, ci)
		if diff := untouchedwalk.Diff(before, after); diff != "" {
			t.Errorf("lore %s wrote into a CI clone:\n%s\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), diff, out, errOut)
			before = after
		}
	}
}
