package app

// migratedryrun_test.go — docs bug B5 (plain files round, 2026-11-12): a
// dry run writes nothing. `lore migrate run --dry-run` in a repo still on
// the v0.1.17 shared-database pointer added lore's local files to the
// clone's .git/info/exclude (and said so). The repo, its .git and the
// shared database stay byte for byte as they were; `lore fix --dry-run`
// likewise.

import (
	"strings"
	"testing"
)

func TestDryRunsWriteNothing(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"migrate", "run", "--dry-run"}, {"fix", "--dry-run"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			w := newCLIWorld(t).person().author()
			repo, shared := modeBReleaseRepo(t, w)
			view := func() string {
				return "repo:\n" + pfSnapshot(t, repo) + "\nshared:\n" + pfSnapshot(t, shared)
			}
			before := view()
			code, out, errOut := w.lore(t, repo, args...)
			if code != 0 && args[0] != "fix" { // fix exits 1 for the findings it would fix
				t.Fatalf("lore %v: exit %d\n%s\n%s", args, code, out, errOut)
			}
			if after := view(); after != before {
				t.Fatalf("lore %v wrote:\n%s\nstderr:\n%s", args, pfDiff(before, after), errOut)
			}
			if strings.Contains(errOut, "info/exclude") {
				t.Fatalf("lore %v says it changed .git/info/exclude:\n%s", args, errOut)
			}
		})
	}
}
