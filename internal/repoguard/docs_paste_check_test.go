package repoguard

// The example blocks in docs/ are pasted from transcripts of real runs
// (tests/docs and tests/team/docs: a section file runs the commands, paste.py
// copies the output into the page). A block edited by hand instead of through
// its section file is undone by the next paste, and `paste --check` then calls
// the page stale (devco:D-843: a config key added by hand to the
// reference#config-check-good block was lost on every paste). The paste check
// ran only in the full gate's docs step, which the fast landing tier skips, so
// the hand edit landed. This test runs it in go test, where every tier sees it:
// --examples-only leaves the help blocks alone, so it needs no lore binary.

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDocsExampleBlocksMatchTranscripts(t *testing.T) {
	t.Parallel()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed; the docs runners need it and run in the full gate")
	}
	for _, script := range []string{"tests/docs/paste.py", "tests/team/docs/paste.py"} {
		cmd := exec.Command(py, filepath.FromSlash(script), "--check", "--examples-only")
		// From the repo root: paste.py resolves pages and transcripts from its own path,
		// and reads page arguments relative to the working folder.
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s --check --examples-only: %v\n%s\nan example block differs from its transcript: change the section file under tests/docs/sections (or tests/team/docs/sections), re-run it and paste; never edit a pasted block by hand", script, err, out)
		}
	}
}
