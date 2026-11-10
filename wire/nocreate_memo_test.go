package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestNoCreateRunRemembersRefusal: within one read run a refused open is
// answered from the run's memory (doctor's ~25 checks each asked the App,
// and each refused open ran git again: 99 git runs for one doctor --all in
// ~); a new run opens afresh (a long-lived `lore api` is not stuck with an
// old refusal).
func TestNoCreateRunRemembersRefusal(t *testing.T) {
	t.Parallel()
	plain := t.TempDir() // no git: the repo store cannot open
	a, err := New(Options{RepoDir: plain, Home: t.TempDir(), FlagActor: "human:pat@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	run := WithNoCreate(context.Background())
	_, err1 := a.Engine(run)
	if !errs.Has(err1, errs.NotAStore) {
		t.Fatalf("first open outside git: want E_NOT_A_STORE, got %v", err1)
	}
	_, cfgErr := a.repoTop(run)
	if !errs.Has(cfgErr, errs.NotAStore) {
		t.Fatalf("first repo-only open outside git: want E_NOT_A_STORE, got %v", cfgErr)
	}
	// Make the folder a lore repo: a fresh open would now get further, so
	// only the run's memory can still answer with the first refusal.
	gitInitIn(t, plain)
	if err := os.MkdirAll(filepath.Join(plain, loreFolderName), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err2 := a.Engine(run); err2 != err1 {
		t.Fatalf("same read run: want the remembered refusal %v, got %v", err1, err2)
	}
	if _, err3 := a.repoTop(run); err3 != cfgErr {
		t.Fatalf("same read run, repo-only open: want the remembered refusal %v, got %v", cfgErr, err3)
	}

	// A WithNoCreate inside the run keeps the run's memory.
	if inner := WithNoCreate(run); noCreateOf(inner) != noCreateOf(run) {
		t.Fatal("a nested WithNoCreate started a new memory")
	}
	// A new run opens again (the App remembered nothing).
	if _, err5 := a.Engine(WithNoCreate(context.Background())); err5 == err1 {
		t.Fatalf("a new read run got the old run's refusal: %v", err5)
	}
}

// TestNoCreateRunTopLevelOnce: gitTopLevel asks git once per folder in a
// read run, and every time outside one.
func TestNoCreateRunTopLevelOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	run := WithNoCreate(context.Background())
	if top := gitTopLevel(run, dir); top != "" {
		t.Fatalf("outside git: %q", top)
	}
	gitInitIn(t, dir)
	if top := gitTopLevel(run, dir); top != "" {
		t.Fatalf("same read run: want the remembered \"\", got %q", top)
	}
	if top := gitTopLevel(context.Background(), dir); top == "" {
		t.Fatal("outside a read run: git was not asked again")
	}
}

// gitInitIn makes dir a git work tree.
func gitInitIn(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	cmd.Env = gitenv.Clean(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
}
