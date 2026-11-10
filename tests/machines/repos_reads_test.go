//go:build machines && !windows

package machines

// repos_reads_test.go — a READ never sets a repo up (decided 2026-11-08):
// every operation that only reads (op.Spec.OnlyReads), run through the real binary
// in a git repo lore was never set up in, leaves the repo byte for byte as
// it was: no .lore/, cache db, .gitattributes, merge-driver config or
// hooks. The read operations come from the registry itself (op metadata),
// not from a hand list, so a new read is covered the day it is added.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/op"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	"github.com/thesatellite-ai/lore/wire"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// readSkip are read operations that do not end on their own (they serve a
// page or a terminal UI until stopped); everything else runs.
var readSkip = map[string]bool{}

// placeholderID is the id given to a read that takes one: it names no row,
// so the read answers "not found" — it must still write nothing.
const placeholderID = "mem_00000000000000000000000000000000"

// readCommands is the CLI words (with placeholder positionals) of every
// read operation of the registry the binary is built from.
func readCommands(t *testing.T) [][]string {
	t.Helper()
	dir, home := t.TempDir(), t.TempDir()
	app, err := wire.New(hosthooks.Options(wire.StandaloneOptions(dir, home, dir, "test"), hosthooks.Default(dir)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	var out [][]string
	for _, e := range app.Registry().Entries() {
		s := e.Spec()
		if !(s.OnlyReads() || s.Name == opsdoctor.OpRun) || s.Hidden || readSkip[s.Name] {
			continue
		}
		args := op.CLIPath(s.Name)
		var pos []op.FieldInfo
		for _, f := range e.InputFields() {
			if f.Arg != op.NoArg {
				pos = append(pos, f)
			}
		}
		slices.SortFunc(pos, func(a, b op.FieldInfo) int { return a.Arg - b.Arg })
		for range pos {
			args = append(args, placeholderID)
		}
		out = append(out, args)
	}
	if len(out) < 50 {
		t.Fatalf("only %d read operations in the registry: %v", len(out), out)
	}
	return out
}

// gitWiring is what a repo's own git folder holds that lore's setup would
// change: the config and every hook that is not a sample.
func gitWiring(t testing.TB, repo string) map[string]string {
	t.Helper()
	snap := treeSnap(t, filepath.Join(repo, ".git"), "objects", "logs", "refs", "index")
	for k := range snap {
		if strings.HasSuffix(k, ".sample") {
			delete(snap, k)
		}
	}
	return snap
}

func TestReposReadsCreateNothing(t *testing.T) {
	cmds := readCommands(t)
	// Four machines, each running a quarter of the reads in its own
	// never-set-up repo, checked after every single command.
	const shards = 4
	var mu sync.Mutex
	ran := 0
	for shard := range shards {
		t.Run("shard"+string(rune('0'+shard)), func(t *testing.T) {
			t.Parallel()
			m := Build(t, ShapeFresh, loreBin)
			repo := m.Repos[RepoPlain]
			work := treeSnap(t, repo, ".git")
			gw := gitWiring(t, repo)
			for i, args := range cmds {
				if i%shards != shard {
					continue
				}
				r := m.Lore(t, repo, args...)
				if strings.Contains(r.Stderr, "panic") || strings.Contains(r.Stderr, "goroutine ") {
					t.Errorf("lore %s panicked: %s", strings.Join(args, " "), r)
				}
				if d := diffSnap(work, treeSnap(t, repo, ".git")); len(d) > 0 {
					t.Errorf("read `lore %s` wrote into a repo lore is not set up in: %v", strings.Join(args, " "), d)
					work = treeSnap(t, repo, ".git")
				}
				if d := diffSnap(gw, gitWiring(t, repo)); len(d) > 0 {
					t.Errorf("read `lore %s` wired git in a repo lore is not set up in: %v", strings.Join(args, " "), d)
					gw = gitWiring(t, repo)
				}
				mu.Lock()
				ran++
				mu.Unlock()
			}
		})
	}
	t.Cleanup(func() { t.Logf("%d read commands run", ran) })
}

// TestReposReadsAnswer: the reads a person types first in a fresh clone
// answer (or name lore init) instead of failing obscurely, and a write
// still sets the repo up as before.
func TestReposReadsAnswer(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeFresh, loreBin)
	repo := m.Repos[RepoPlain]
	for _, args := range [][]string{{"whoami"}, {"setup", "list"}, {"doctor"}, {"store", "list"}, {"sync", "status"}, {"memory", "list"}} {
		r := m.Lore(t, repo, args...)
		if r.Code != 0 && (!strings.Contains(r.Stderr, "lore init") || !strings.Contains(r.Stderr, "isn't set up")) {
			t.Errorf("lore %s in a repo lore is not set up in: %s", strings.Join(args, " "), r)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".lore")); err == nil {
		t.Fatalf("a read made .lore/")
	}
	// A write keeps the implicit setup.
	loreOK(t, m.Lore(t, repo, "init"))
	loreOK(t, m.Lore(t, repo, "memory", "add", "after-init"))
	if len(memoryFiles(repo, "after-init")) != 1 {
		t.Errorf("memory add after init wrote no row")
	}
}
