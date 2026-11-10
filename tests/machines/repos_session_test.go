//go:build machines && !windows

package machines

// repos_session_test.go — a repo under a folder named after the agent's
// tool session (a scratchpad) can be set up: its local-path origin names
// the session id as a folder, which is no leak. The same id written as a
// value (prose, an https url) is still refused.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sessionPlaceholder is a placeholder tool session id (uuid-shaped).
const sessionPlaceholder = "3f2a9c1e-7b4d-4e8a-9c2f-1a2b3c4d5e6f"

func TestReposSessionFolder(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeFresh, loreBin)
	m.ExtraEnv = append(m.ExtraEnv, "CLAUDE_CODE_CHILD_SESSION=1", "CLAUDE_CODE_SESSION_ID="+sessionPlaceholder)
	scratch := filepath.Join(m.Home, "scratch", sessionPlaceholder)
	lib := filepath.Join(scratch, "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	m.mustGit(t, lib, "init", "-q")
	m.mustGit(t, lib, "commit", "-q", "--allow-empty", "-m", "start")
	// The session becomes known to lore (a session row) by any command.
	loreOK(t, m.Lore(t, m.Repos[RepoLore], "whoami"))
	app := m.Repos[RepoPlain]
	m.mustGit(t, app, "remote", "add", "origin", lib)
	if r := m.Lore(t, app, "init", "--name", "app"); r.Code != 0 {
		t.Fatalf("lore init with an origin under a session-named folder: %s", r)
	}
	loreOK(t, m.Lore(t, app, "memory", "add", "notes in "+filepath.Join(scratch, "notes.md")))
	for _, text := range []string{"session " + sessionPlaceholder + " was slow", "see https://example.test/s?id=" + sessionPlaceholder} {
		r := m.Lore(t, app, "memory", "add", text)
		if r.Code == 0 || !strings.Contains(r.Stderr, "session-id") || strings.Contains(r.Stderr, sessionPlaceholder) {
			t.Errorf("the session id as a value (%q): %s", text, r)
		}
	}
	m.CheckMachine(t, "repos/session folder", Gone{})
}

// TestReposFailedInitUndoes: a lore init that fails after it opened the
// repo store (here: its origin url carries the session id as a value, so
// the project row is refused) leaves the repo exactly as it was — no
// .lore/, cache db, state/audit, hooks, git config or .gitattributes.
func TestReposFailedInitUndoes(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeFresh, loreBin)
	m.ExtraEnv = append(m.ExtraEnv, "CLAUDE_CODE_CHILD_SESSION=1", "CLAUDE_CODE_SESSION_ID="+sessionPlaceholder)
	loreOK(t, m.Lore(t, m.Repos[RepoLore], "whoami"))
	app := m.Repos[RepoPlain]
	m.mustGit(t, app, "remote", "add", "origin", "https://example.test/"+sessionPlaceholder+"/app.git")
	work, gw := treeSnap(t, app, ".git"), gitWiring(t, app)
	r := m.Lore(t, app, "init", "--name", "app")
	if r.Code == 0 || !strings.Contains(r.Stderr, "session-id") {
		t.Fatalf("init with the session id in its origin url: %s", r)
	}
	if d := diffSnap(work, treeSnap(t, app, ".git")); len(d) > 0 {
		t.Errorf("the failed init left files behind: %v", d)
	}
	if d := diffSnap(gw, gitWiring(t, app)); len(d) > 0 {
		t.Errorf("the failed init left git wiring behind: %v", d)
	}
	// The repo can still be set up once the cause is gone.
	m.mustGit(t, app, "remote", "set-url", "origin", "https://example.test/pat/app.git")
	loreOK(t, m.Lore(t, app, "init", "--name", "app"))
}
