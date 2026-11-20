//go:build journeys && !windows

package journeys

// workflow_pin_test.go — devco D-742: `lore row move` in a repo rewrote the
// tracked .github/workflows/lore-sync-merge.yml (its auto pin moved from
// the old release to the running one, with a new checksum line) as a side
// effect, and the change rode into the next commit. Now writing commands
// and sync leave the file byte for byte as it was and each run prints one
// line naming `lore sync install-action`; that command (run as printed)
// updates it, `lore doctor` warns with the same command and `lore doctor
// --fix` updates it too.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

const (
	// wfPinEnv is the loretest seam for the release the workflow pins (a
	// test binary is a development build, which never pins one).
	wfPinEnv  = "LORE_TEST_PIN_VERSION"
	wfFile    = ".github/workflows/lore-sync-merge.yml"
	wfOld     = "0.1.18"
	wfNew     = "0.1.23"
	wfStale   = "lore: " + wfFile + " pins lore v" + wfOld + "; run `lore sync install-action` to update it to v" + wfNew
	wfInstall = "lore sync install-action"
)

func TestJourneyWorkflowPinNeverRewrittenAsSideEffect(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	app := w.Remote("github.com/orchard-dev/pinned")
	ida := w.Persona("Ida Marsh", "ida@orchard.example").Laptop()
	var c *teamharness.Clone
	var installed string

	w.Step("day1/the-repo-commits-its-workflow-at-the-old-release", func() {
		c = ida.Clone(app, "pinned")
		ida.LoreIn(c, "init", "--target", "claude_code").OK(t)
		ida.SetEnv(wfPinEnv, wfOld)
		ida.LoreIn(c, "sync", "install-action").OK(t)
		installed, _ = c.ReadFile(wfFile)
		if !strings.Contains(installed, "/v"+wfOld+"/") {
			t.Fatalf("the workflow does not pin v%s:\n%s", wfOld, installed)
		}
		c.CommitAll("lore and its workflow")
	})

	w.Step("day2/after-an-upgrade-writes-and-sync-leave-the-workflow-alone", func() {
		ida.SetEnv(wfPinEnv, wfNew)
		runs := [][]string{
			{"memory", "add", "--body", "the staging box restarts at midnight"},
			{"task", "add", "--title", "rotate the staging key", "--commitment", "accepted"},
			{"rule", "add", "--body", "run the linter before a push", "--severity", "must"},
			{"sync"},
		}
		for _, args := range runs {
			r := ida.LoreIn(c, args...).OK(t)
			if n := strings.Count(r.Stderr, wfStale); n != 1 {
				t.Fatalf("lore %s: the stale-pin line %d times, want once:\n%s", strings.Join(args, " "), n, r.Stderr)
			}
			if got, _ := c.ReadFile(wfFile); got != installed {
				t.Fatalf("lore %s rewrote %s", strings.Join(args, " "), wfFile)
			}
			if s := strayStatus(c); s != "" {
				t.Fatalf("lore %s left changes outside .lore/:\n%s", strings.Join(args, " "), s)
			}
		}
		r := ida.LoreIn(c, "memory", "add", "--body", "json runs stay quiet", "--json").OK(t)
		if strings.Contains(r.Out(), "pins lore v"+wfOld) {
			t.Fatalf("a --json run printed the stale-pin line:\n%s", r.Out())
		}
	})

	w.Step("day2/doctor-warns-with-the-command", func() {
		ida.LoreIn(c, "doctor").Says(t, wfFile+" pins lore v"+wfOld, "`"+wfInstall+"`", "`lore doctor --fix`")
		if got, _ := c.ReadFile(wfFile); got != installed {
			t.Fatal("lore doctor rewrote the workflow")
		}
	})

	w.Step("day2/install-action-as-printed-updates-it", func() {
		args := strings.Fields(strings.TrimPrefix(wfInstall, "lore "))
		ida.LoreIn(c, args...).OK(t)
		got, _ := c.ReadFile(wfFile)
		if !strings.Contains(got, "/v"+wfNew+"/") {
			t.Fatalf("install-action did not move the pin to v%s:\n%s", wfNew, got)
		}
		ida.LoreIn(c, "memory", "add", "--body", "fresh pin, no line").OK(t).Never(t, "pins lore v")
	})

	w.Step("day3/doctor-fix-updates-a-stale-pin", func() {
		c.Git("checkout", "-q", "--", wfFile) // back to the committed v0.1.18 file
		ida.LoreIn(c, "doctor", "--fix").Says(t, wfFile+" pinned lore v"+wfOld+"; updated now to v"+wfNew)
		if got, _ := c.ReadFile(wfFile); !strings.Contains(got, "/v"+wfNew+"/") {
			t.Fatalf("doctor --fix did not move the pin:\n%s", got)
		}
	})
}
