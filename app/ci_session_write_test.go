package app

// Writes inside a CI run. lore records the run as this machine's session:
// tool_session_id is "<run id>/<attempt>", and the ci and run_id fields
// carry the same id. The session-id rule refuses a known tool session id
// in row text, and before the fix it refused the session row's own ci
// field: every write in GitHub Actions (or any CI lore detects) stored the
// row, then failed its audit with "ci looks like a secret (session-id)".

import (
	"strings"
	"testing"
)

// TestWriteInsideGitHubActions: in a GitHub Actions run (the variables the
// runner sets), a person's init and writes all succeed, and the
// run id still never lands in a shared row's text.
func TestWriteInsideGitHubActions(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person()
	// the run's variables before the first write: the first write of a run
	// creates its actor row, whose audit creates the session row
	w.setAll(map[string]string{
		"CI": "true", "GITHUB_ACTIONS": "true", "GITHUB_RUN_ID": "42854833954", "GITHUB_RUN_ATTEMPT": "1",
		"GITHUB_REPOSITORY": "octo/app", "GITHUB_WORKFLOW": "check", "GITHUB_JOB": "test",
		"GITHUB_SHA": "3f30ab64c0ffee0000000000000000000000beef",
	})
	dir := w.initRepo(t)
	if code, out, errOut := w.lore(t, dir, "policy", "set", "agents.person_only", "memory.delete", "--yes"); code != 0 {
		t.Fatalf("policy set in GitHub Actions: exit %d\n%s\n%s", code, out, errOut)
	}
	if code, out, errOut := w.lore(t, dir, "memory", "add", "--body", "written in CI"); code != 0 {
		t.Fatalf("memory add in GitHub Actions: exit %d\n%s\n%s", code, out, errOut)
	}
	if code, out, errOut := w.lore(t, dir, "task", "add", "a CI task", "--commitment", "accepted"); code != 0 {
		t.Fatalf("task add in GitHub Actions: exit %d\n%s\n%s", code, out, errOut)
	}
	// a second session in the same run: an agent's. Its session row's ci
	// field names the run, whose id the person's session row already holds
	// as a known tool session id.
	w.set("LORE_HUMAN", "")
	w.set("LORE_ACTOR", "agent:ci-bot")
	w.set("LORE_SESSION", "s-ci-bot")
	if code, out, errOut := w.lore(t, dir, "memory", "add", "--body", "an agent wrote this in CI"); code != 0 {
		t.Fatalf("agent memory add in GitHub Actions: exit %d\n%s\n%s", code, out, errOut)
	}
	// the session-id rule still guards free text: the run id in a row body is refused
	code, out, errOut := w.lore(t, dir, "memory", "add", "--body", "run 42854833954/1 failed")
	if code == 0 || !strings.Contains(out+errOut, "session-id") {
		t.Fatalf("a row naming the run id was not refused by the session-id rule: exit %d\n%s\n%s", code, out, errOut)
	}
}
