package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// trackedHooksRepo prepares a fixture repo whose hooks folder is the
// tracked .githooks (core.hooksPath), holding the team's own pre-push, and
// whose .gitignore lists .lore/ when private.
func trackedHooksRepo(private bool) func(dir string) {
	return func(dir string) {
		must := func(err error) {
			if err != nil {
				panic(err)
			}
		}
		must(os.MkdirAll(filepath.Join(dir, ".githooks"), 0o750))
		must(os.WriteFile(filepath.Join(dir, ".githooks", hookPrePush), []byte(teamPrePush), 0o700)) // #nosec G306 -- a hook git runs
		must(os.WriteFile(filepath.Join(dir, gitAttributesFile), []byte(teamAttrs), 0o600))
		ignore := "bin/\n"
		if private {
			ignore += ".lore/\n"
		}
		must(os.WriteFile(filepath.Join(dir, gitignoreName), []byte(ignore), 0o600))
	}
}

const (
	teamPrePush = "#!/bin/sh\necho team pre-push\n"
	teamAttrs   = "*.sh text eol=lf\n"
)

// commitTeamFiles commits the fixture's team files and points git at the
// tracked hooks folder.
func commitTeamFiles(t *testing.T, f ltFixture) {
	t.Helper()
	gitT(t, f.s.top, "add", ".githooks", gitAttributesFile, gitignoreName)
	gitT(t, f.s.top, "commit", "-q", "-m", "team files")
	gitT(t, f.s.top, "config", "core.hooksPath", ".githooks")
}

// assertTeamFilesUntouched fails when lore changed or created any file the
// team commits (.githooks, .gitattributes).
func assertTeamFilesUntouched(t *testing.T, f ltFixture, when string) {
	t.Helper()
	out, err := os.ReadDir(filepath.Join(f.s.top, ".githooks"))
	if err != nil || len(out) != 1 {
		t.Fatalf("%s: .githooks holds %d files (%v), want only the team's pre-push", when, len(out), err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.s.top, ".githooks", hookPrePush)); string(b) != teamPrePush {
		t.Fatalf("%s: lore changed the tracked .githooks/pre-push:\n%s", when, b)
	}
	if b, _ := os.ReadFile(filepath.Join(f.s.top, gitAttributesFile)); string(b) != teamAttrs {
		t.Fatalf("%s: lore changed the tracked .gitattributes:\n%s", when, b)
	}
}

// TestTrackedHooksFolderNeedsExplicitYes (devco D-501): an install step
// (init, doctor --fix, lore fix) wrote lore's blocks, the lore-sync:live
// pre-push block among them, into a hooks folder committed with the repo
// without asking. Now only the explicit yes (`lore fix --tracked-hooks`)
// writes there, and doctor --fix names that flag instead.
func TestTrackedHooksFolderNeedsExplicitYes(t *testing.T) {
	f := newLiveFixtureWith(t, "", nil, trackedHooksRepo(false))
	commitTeamFiles(t, f)
	hooks := filepath.Join(f.s.top, ".githooks")

	f.s.ensureGitWiring(f.ctx, f.s.top, wireInstall)
	if b, _ := os.ReadFile(filepath.Join(hooks, hookPrePush)); string(b) != teamPrePush {
		t.Fatalf("an install step with no yes changed the tracked pre-push:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(hooks, hookCommitMsg)); !os.IsNotExist(err) {
		t.Fatalf("an install step with no yes created %s in the tracked folder (%v)", hookCommitMsg, err)
	}
	fixing, err := liveDoctor{h: f.s.h}.Base(f.ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(hooks, hookPrePush)); strings.Contains(string(b), rideHookMarker) {
		t.Fatalf("doctor --fix added the %s block to the tracked pre-push:\n%s", rideHookMarker, b)
	}
	if !strings.Contains(strings.Join(fixing.Warnings, "\n"), "`lore fix --"+fixTrackedHooksFlag+"`") {
		t.Fatalf("doctor --fix does not name the explicit yes: %q", fixing.Warnings)
	}

	f.s.ensureGitWiring(withTrackedHooksYes(f.ctx), f.s.top, wireInstall)
	if b, _ := os.ReadFile(filepath.Join(hooks, hookPrePush)); !strings.Contains(string(b), rideHookMarker) || !strings.Contains(string(b), "echo team pre-push") {
		t.Fatalf("lore fix --tracked-hooks did not chain its block into the team's pre-push:\n%s", b)
	}
}

// TestPrivateProjectWritesNoTrackedFile (devco D-500): with .lore/ ignored
// by git the project is private to this machine: neither an install step,
// even with the tracked-hooks yes, nor doctor --fix writes .gitattributes
// or a block into the tracked hooks folder.
func TestPrivateProjectWritesNoTrackedFile(t *testing.T) {
	f := newLiveFixtureWith(t, "", nil, trackedHooksRepo(true))
	commitTeamFiles(t, f)
	f.s.ensureGitWiring(withTrackedHooksYes(f.ctx), f.s.top, wireInstall)
	assertTeamFilesUntouched(t, f, "an install step")
	f.s.ensureGitWiring(f.ctx, f.s.top, wireAuto)
	assertTeamFilesUntouched(t, f, "a write")
	if _, err := (liveDoctor{h: f.s.h}).Base(withTrackedHooksYes(f.ctx), true); err != nil {
		t.Fatal(err)
	}
	assertTeamFilesUntouched(t, f, "doctor --fix")
}
