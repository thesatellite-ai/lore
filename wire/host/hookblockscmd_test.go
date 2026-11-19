package host

// hookblockscmd_test.go — every line about lore's hook blocks names the
// command that works where it is printed (devco D-709). With the hooks
// folder committed with the repo (core.hooksPath=.githooks, tracked), the
// pre-commit's `lore check leaks` said "`lore sync` … sets up hook
// blocks", while `lore sync` says it will not add blocks there: a loop.
// Only `lore fix --tracked-hooks` writes there (D-501), so that is the one
// command a hook-block line may name in that shape.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// trackedHookRefusers are the commands that leave lore's hook blocks out
// of a hooks folder committed with the repo: a hook-block line naming one
// of them there sends the person round a loop.
var trackedHookRefusers = []string{"lore sync", "lore fix", "lore doctor --fix", "doctor --fix", "lore init"}

// trackedHooksLocalWay is the one place a tracked-folder line may name
// plain `lore fix`: after moving the hooks into .git/hooks, where it works.
const trackedHooksLocalWay = "run `git config --local core.hooksPath .git/hooks`, then `lore fix`"

// backticked finds the `…` spans of a printed line.
var backticked = regexp.MustCompile("`([^`]+)`")

// assertHookLinesNameTrackedFix is the printed-next-step guard for the
// tracked-hooks shape: each line of out that is about hook blocks in dir
// names `lore fix --tracked-hooks` and none of trackedHookRefusers.
func assertHookLinesNameTrackedFix(t *testing.T, shape, dir, out string) {
	t.Helper()
	seen := 0
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "hook block") || !strings.Contains(line, dir) {
			continue
		}
		seen++
		scan := strings.ReplaceAll(line, trackedHooksLocalWay, "")
		named := false
		for _, m := range backticked.FindAllStringSubmatch(scan, -1) {
			cmd := strings.TrimSpace(m[1])
			if cmd == trackedHooksCommand {
				named = true
				continue
			}
			for _, bad := range trackedHookRefusers {
				if cmd == bad {
					t.Errorf("%s: a hook-block line names `%s`, which leaves a tracked hooks folder alone; want `%s`:\n%s", shape, cmd, trackedHooksCommand, line)
				}
			}
		}
		if !named {
			t.Errorf("%s: a hook-block line does not name `%s`:\n%s", shape, trackedHooksCommand, line)
		}
	}
	if seen == 0 {
		t.Fatalf("%s: no line about the hook blocks in %s:\n%s", shape, dir, out)
	}
}

// resetStderr empties the fixture's captured stderr.
func resetStderr(f ltFixture) {
	if b, ok := f.s.h.cfg.Stderr.(*strings.Builder); ok {
		b.Reset()
	}
}

// TestHookBlockHintsNameTrackedHooksFix (devco D-709): in a repo whose
// hooks folder is committed with the repo, every line that says lore's
// hook blocks are missing or stale names `lore fix --tracked-hooks`: a
// read (the pre-commit's check leaks) with blocks an older lore left, a
// read with the guard blocks removed (and its hook-manager reminder), and
// doctor in a fresh clone.
func TestHookBlockHintsNameTrackedHooksFix(t *testing.T) {
	t.Run("read-with-stale-blocks", func(t *testing.T) {
		f := newLiveFixtureWith(t, "", nil, trackedHooksRepo(false))
		commitTeamFiles(t, f)
		hooks := filepath.Join(f.s.top, ".githooks")
		f.s.ensureGitWiring(withTrackedHooksYes(f.ctx), f.s.top, wireInstall)
		staleTrackedBlocks(t, hooks)
		resetStderr(f)

		f.s.ensureGitWiring(f.ctx, f.s.top, wireRead)
		out := stderrOf(f)
		if !strings.Contains(out, "`"+trackedHooksCommand+"` sets up hook blocks in "+hooks) {
			t.Fatalf("a read with stale blocks in the tracked folder does not name %s:\n%s", trackedHooksCommand, out)
		}
		assertHookLinesNameTrackedFix(t, "read-with-stale-blocks", hooks, out)
	})

	t.Run("read-with-guard-blocks-gone", func(t *testing.T) {
		f := newLiveFixtureWith(t, "", nil, trackedHooksRepo(false))
		commitTeamFiles(t, f)
		hooks := filepath.Join(f.s.top, ".githooks")
		f.s.ensureGitWiring(withTrackedHooksYes(f.ctx), f.s.top, wireInstall)
		for _, h := range []string{hookCommitMsg, hookPreCommit} {
			if err := os.Remove(filepath.Join(hooks, h)); err != nil {
				t.Fatal(err)
			}
		}
		resetStderr(f)

		f.s.ensureGitWiring(f.ctx, f.s.top, wireRead)
		out := stderrOf(f)
		if !strings.Contains(out, "leak guard off") {
			t.Fatalf("no leak-guard line for the removed blocks:\n%s", out)
		}
		assertHookLinesNameTrackedFix(t, "read-with-guard-blocks-gone", hooks, out)
	})

	t.Run("doctor-in-a-fresh-clone", func(t *testing.T) {
		f := newLiveFixtureWith(t, "", nil, trackedHooksRepo(false))
		commitTeamFiles(t, f)
		hooks := filepath.Join(f.s.top, ".githooks")
		base, err := liveDoctor{h: f.s.h}.Base(f.ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		assertHookLinesNameTrackedFix(t, "doctor-in-a-fresh-clone", hooks, strings.Join(append(base.Warnings, base.Notes...), "\n"))
	})
}

// staleTrackedBlocks changes one line inside each lore block of dir, as a
// block an older lore wrote reads to this one.
func staleTrackedBlocks(t *testing.T, dir string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	changed := 0
	for _, e := range ents {
		p := filepath.Join(dir, e.Name())
		b, rerr := os.ReadFile(filepath.Clean(p))
		if rerr != nil {
			t.Fatal(rerr)
		}
		next := strings.ReplaceAll(string(b), "lore sync hook", "lore  sync hook")
		if next == string(b) {
			continue
		}
		changed++
		if werr := os.WriteFile(p, []byte(next), 0o700); werr != nil { // #nosec G306 -- a hook git runs
			t.Fatal(werr)
		}
	}
	if changed == 0 {
		t.Fatalf("no lore block to make stale in %s", dir)
	}
}
