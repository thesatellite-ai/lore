package app

// untouched_every_test.go — S-2, the standing rule "a repo with no lore
// data is never touched" (internal/untouchedwalk): a git repo with no
// .lore/ stays byte-identical (its tree, .git/config, .git/hooks,
// .git/info, .git/lore) after every read and every command that is not
// init, setup or link. The walk is over the real command tree (every
// registry operation plus the host's own commands), so a new command is
// covered the day it is added; only the commands it may not run are named
// (untouchedwalk.Skip, each with its reason).
//
// Each command runs in its own fresh repo and lore home, with every
// required input filled with a placeholder and --yes when it can ask, so
// a write gets as far as it can. Whether it succeeds is not the point
// (most refuse: lore isn't set up there); the tree is. Before the rule
// held, 250 commands failed here: every write took the live lock in
// .git/lore, about twenty wired hooks and the merge driver, and some (lore
// vars set, task heartbeat, render) made .lore/ by themselves.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/internal/untouchedwalk"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/wire"
)

// untouchedDeadline bounds one command of the walk: a one-shot command in
// a scratch repo answers in seconds, so a run past this is a hang.
const untouchedDeadline = 90 * time.Second

// untouchedMinCommands keeps the walk from passing by finding nothing.
const untouchedMinCommands = 300

// TestUntouchedRepoEveryCommand is S-2's walk: every command but the
// skipped ones, run in a git repo with no lore data, leaves it as it was.
func TestUntouchedRepoEveryCommand(t *testing.T) {
	t.Parallel()
	entries := untouchedEntries(t)
	var cmds [][]string
	seen := map[string]bool{}
	for _, path := range untouchedLeaves(newDriftRoot(t)) {
		key := strings.Join(path, " ")
		if seen[key] {
			continue
		}
		seen[key] = true
		e := entries[key] // nil for a host command
		if untouchedwalk.Walked(path, e) {
			cmds = append(cmds, untouchedwalk.Args(path, e))
		}
	}
	if len(cmds) < untouchedMinCommands {
		t.Fatalf("the walk found only %d commands: the command tree walk is broken", len(cmds))
	}
	for _, args := range cmds {
		name := strings.Join(args, " ")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newCLIWorld(t).person().unscoped().author().hermeticPath(t)
			w.setAll(map[string]string{"EDITOR": "true", "VISUAL": "true", "BROWSER": "true"})
			dir := w.root
			w.git(t, dir, "init", "-q", "-b", "main")
			if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("a repo with no lore data\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			w.git(t, dir, "add", "README.md")
			w.git(t, dir, "commit", "-q", "-m", "first")
			before := untouchedTree(t, dir)
			_, out, errOut := w.loreWithin(t, dir, untouchedDeadline, args...)
			if diff := untouchedwalk.Diff(before, untouchedTree(t, dir)); diff != "" {
				t.Fatalf("lore %s changed a repo with no lore data:\n%s\nstdout:\n%s\nstderr:\n%s", name, diff, out, errOut)
			}
		})
	}
}

// untouchedTree is untouchedwalk.Tree failing the test on error.
func untouchedTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	tree, err := untouchedwalk.Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// untouchedEntries maps each registry operation's CLI path to its entry.
func untouchedEntries(t *testing.T) map[string]op.Entry {
	t.Helper()
	wd, home := t.TempDir(), t.TempDir()
	base, _ := wire.StandaloneOptionsOrg(wd, home, wd, version)
	a, err := wire.New(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	out := map[string]op.Entry{}
	for _, e := range a.Registry().Entries() {
		out[strings.Join(op.CLIPath(e.Spec().Name), " ")] = e
	}
	return out
}

// untouchedLeaves is the CLI path of every runnable command under root.
func untouchedLeaves(root *cobra.Command) [][]string {
	var out [][]string
	var walk func(c *cobra.Command, path []string)
	walk = func(c *cobra.Command, path []string) {
		if c.Runnable() && len(path) > 0 {
			out = append(out, path)
		}
		for _, s := range c.Commands() {
			walk(s, append(slices.Clone(path), s.Name()))
		}
	}
	walk(root, nil)
	return out
}
