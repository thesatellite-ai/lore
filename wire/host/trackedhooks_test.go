package host

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/internal/testenv"
	opsorg "github.com/thesatellite-ai/lore/ops/orgcmd"
)

// githooksRepo is a repo whose core.hooksPath is a committed .githooks
// (the team's pre-push gate in it). It returns the top and the folder.
func githooksRepo(t *testing.T) (top, hooks string) {
	t.Helper()
	top = t.TempDir()
	gitT(t, top, "init", "-q", "-b", "main")
	gitT(t, top, "config", "user.email", "dev@example.test")
	gitT(t, top, "config", "user.name", "Dev")
	hooks = filepath.Join(top, ".githooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, hookPrePush), []byte("#!/bin/sh\necho team gate\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	gitT(t, top, "add", "-A")
	gitT(t, top, "commit", "-qm", "init")
	gitT(t, top, "config", "core.hooksPath", ".githooks")
	return top, hooks
}

// TestLoreOnlyHook: only a file that is a shebang plus lore's blocks is
// lore's own; any other line keeps it the person's (D-732).
func TestLoreOnlyHook(t *testing.T) {
	t.Parallel()
	block := "# >>> " + gitBlockMarker + " >>>\nlore x\n# <<< " + gitBlockMarker + " <<<\n"
	ride := "# >>> " + rideHookMarker + " >>>\nlore y\n# <<< " + rideHookMarker + " <<<\n"
	for _, c := range []struct {
		name, body string
		want       bool
	}{
		{"one block", "#!/bin/sh\n" + block, true},
		{"two blocks", "#!/bin/sh\n" + block + "\n" + ride, true},
		{"sub-folder block", "#!/bin/sh\n# >>> " + gitBlockMarker + ":pkg/a >>>\nlore z\n# <<< " + gitBlockMarker + ":pkg/a <<<\n", true},
		{"own line too", "#!/bin/sh\necho mine\n" + block, false},
		{"no block", "#!/bin/sh\necho mine\n", false},
		{"open block", "#!/bin/sh\n# >>> " + gitBlockMarker + " >>>\nlore x\n", false},
		{"empty", "", false},
	} {
		if got := loreOnlyHook(c.body); got != c.want {
			t.Errorf("%s: loreOnlyHook = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestDoctorTrackedHooksRemovesLoreOnlyKeepsMixed (devco D-732): in a repo
// whose hooks folder is committed, an untracked hook file holding only
// lore's blocks (an older lore's) is one doctor warning naming `lore
// doctor --fix`, and --fix removes it; a file with the person's own lines
// stays; a file `lore fix --tracked-hooks` created on the person's yes
// stays. Doctor prints the blocks as paste lines, and pasted as printed
// lore finds its block there. Break-verified: without doctorTrackedHooks
// no warning, no removal, no paste lines.
func TestDoctorTrackedHooksRemovesLoreOnlyKeepsMixed(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	ctx := context.Background()
	top, hooks := githooksRepo(t)
	if err := os.MkdirAll(filepath.Join(top, ".lore", "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	repo, err := gitref.Open(ctx, top, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &liveSession{repo: repo, top: top, h: &liveHost{}, stateDir: t.TempDir()}
	loreOnly := "#!/bin/sh\n# >>> " + gitBlockMarker + " >>>\nlore task hook commit-msg \"$1\" || true\n# <<< " + gitBlockMarker + " <<<\n"
	mixed := "#!/bin/sh\necho mine\n# >>> " + rideHookMarker + " >>>\nlore sync hook post-commit || true\n# <<< " + rideHookMarker + " <<<\n"
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(hooks, name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write(hookCommitMsg, loreOnly)
	write(hookPostCommit, mixed)
	run := func(fix bool) (warns, notes []string) {
		s.doctorTrackedHooks(ctx, hooks, false, fix, func(w string) { warns = append(warns, w) }, func(n string) { notes = append(notes, n) })
		return warns, notes
	}
	warns, notes := run(false)
	if len(warns) != 1 || !strings.Contains(warns[0], "commit-msg") || strings.Contains(warns[0], hookPostCommit) || !strings.Contains(warns[0], "`lore doctor --fix` removes them") {
		t.Fatalf("doctor: warnings %q, want one naming commit-msg (not the mixed post-commit) and doctor --fix", warns)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "paste into "+filepath.Join(hooks, hookPreCommit)) || !strings.Contains(notes[0], "# >>> "+gitBlockMarker+" >>>") {
		t.Fatalf("doctor: notes %q, want the paste lines for pre-commit", notes)
	}
	// pasted as printed: lore finds its pre-commit block there
	text := notes[0][strings.Index(notes[0], "paste into "+filepath.Join(hooks, hookPreCommit)):]
	text = text[strings.Index(text, "\n")+1:]
	if i := strings.Index(text, "\npaste into "); i >= 0 {
		text = text[:i]
	}
	write(hookPreCommit, "#!/bin/sh\n"+text+"\n")
	for _, m := range missingGuardHooks(hooks) {
		if m == hookPreCommit {
			t.Fatalf("pasted pre-commit lines %q: lore still reads the block as missing", text)
		}
	}
	_ = os.Remove(filepath.Join(hooks, hookPreCommit))

	warns, _ = run(true)
	if len(warns) != 1 || !strings.Contains(warns[0], "removed untracked file(s) commit-msg") {
		t.Fatalf("doctor --fix: warnings %q, want the removal named", warns)
	}
	if _, err := os.Stat(filepath.Join(hooks, hookCommitMsg)); !os.IsNotExist(err) {
		t.Fatalf("doctor --fix kept the lore-only commit-msg (%v)", err)
	}
	if b, _ := os.ReadFile(filepath.Join(hooks, hookPostCommit)); string(b) != mixed {
		t.Fatalf("doctor --fix changed the mixed post-commit: %q", b)
	}
	// created on the person's yes (`lore fix --tracked-hooks`): kept
	write(hookCommitMsg, loreOnly)
	s.noteTrackedAsked([]string{hookCommitMsg})
	if warns, _ = run(true); len(warns) != 0 {
		t.Fatalf("a file made on the person's yes: warnings %q, want none", warns)
	}
	if _, err := os.Stat(filepath.Join(hooks, hookCommitMsg)); err != nil {
		t.Fatalf("doctor --fix removed the file the person asked for: %v", err)
	}
	// a tracked lore-only file is the team's: never removed
	gitT(t, top, "add", ".githooks/commit-msg")
	gitT(t, top, "commit", "-qm", "hooks", "--no-verify")
	if err := os.Remove(filepath.Join(s.stateDir, trackedAskedFile)); err != nil {
		t.Fatal(err)
	}
	if warns, _ = run(true); len(warns) != 0 {
		t.Fatalf("a tracked lore-only file: warnings %q, want none", warns)
	}
}

// TestClientGuardTrackedHooksWritesNothing (devco D-732): linking a client
// repo whose core.hooksPath is a committed .githooks wrote a new
// .githooks/commit-msg, an untracked file in every `git status`. Now the
// folder stays byte for byte as it was and the line names the folder and
// the lines to paste into the repo's own hook. Break-verified: without the
// hooksFolderTrackedAt check the file is created.
func TestClientGuardTrackedHooksWritesNothing(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	ctx := context.Background()
	top, hooks := githooksRepo(t)
	var out bytes.Buffer
	h := &liveHost{cfg: LiveConfig{WorkDir: top, Stderr: &out, ClientRepo: func(context.Context) bool { return true }}}
	h.installClientGuard(ctx, opsorg.OpLink)
	if _, err := os.Stat(filepath.Join(hooks, hookCommitMsg)); !os.IsNotExist(err) {
		t.Fatalf("client guard wrote %s into the tracked hooks folder (%v)", hookCommitMsg, err)
	}
	st, err := gitref.Exec{}.Run(ctx, gitref.Invocation{Dir: top, Args: []string{"status", "--porcelain", "--untracked-files=all"}})
	if err != nil || strings.TrimSpace(string(st)) != "" {
		t.Fatalf("git status after the link: %q %v, want clean", st, err)
	}
	// the line names the committed folder and the lines to add to its hooks (the client guard's hand text, D-730)
	if !strings.Contains(out.String(), "a folder committed with the repo") || !strings.Contains(out.String(), "lore check leaks --staged --commit-msg") {
		t.Fatalf("client guard line %q: want the committed folder %s named and the lines to add", out.String(), hooks)
	}
}
