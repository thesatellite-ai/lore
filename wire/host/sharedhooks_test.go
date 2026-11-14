package host

// D-20 / D-21 (the untouched-repo rule): `lore setup` itself never sets the
// repo it runs in up, and lore's hook blocks in a hooks folder every repo
// shares (core.hooksPath from the global git config) start no lore in a
// repo lore is not set up in.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

// TestSetupRunSetsNoRepoUp: setup.run (--agent-hooks, --dry-run,
// --schema-only) is not a set-up command, so the host gives it the
// untouched-repo context; the setup steps still are.
func TestSetupRunSetsNoRepoUp(t *testing.T) {
	if setsUpRepo(opssetup.OpRun) {
		t.Fatalf("%s sets the repo up: lore setup --agent-hooks made .lore/ in a repo nobody initialized", opssetup.OpRun)
	}
	if !setsUpRepo(setUpPrefix + "apply") {
		t.Fatal("the setup steps no longer set a repo up")
	}
}

// TestHooksFolderSharedOnlyFromGlobalConfig: a core.hooksPath of the
// global git config is shared by every repo; the clone's own is not.
func TestHooksFolderSharedOnlyFromGlobalConfig(t *testing.T) {
	f := newLiveFixture(t)
	defer f.s.release()
	ctx := context.Background()
	global := filepath.Join(t.TempDir(), "gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	if _, shared, err := f.s.hooksFolder(ctx); err != nil || shared {
		t.Fatalf("no core.hooksPath: shared=%v err=%v", shared, err)
	}
	hooks := filepath.Join(t.TempDir(), "githooks")
	gitT(t, f.s.top, "config", "--file", global, "core.hooksPath", hooks)
	dir, shared, err := f.s.hooksFolder(ctx)
	if err != nil || !shared || dir != hooks {
		t.Fatalf("global core.hooksPath: dir=%q shared=%v err=%v, want %q shared", dir, shared, err, hooks)
	}
	gitT(t, f.s.top, "config", "core.hooksPath", ".githooks")
	dir, shared, err = f.s.hooksFolder(ctx)
	if want := filepath.Join(f.s.top, ".githooks"); err != nil || shared || dir != want {
		t.Fatalf("local core.hooksPath: dir=%q shared=%v err=%v, want %q not shared", dir, shared, err, want)
	}
}

// TestSharedHookBlocksStartNoLoreOutsideLoreRepos: every line of every
// block in a shared folder starts lore only in a repo with .lore/ (or a
// clone lore wired); in a plain repo none does, so nothing is written there.
func TestSharedHookBlocksStartNoLoreOutsideLoreRepos(t *testing.T) {
	if runtime.GOOS == "windows" {
		return // the hook lines are POSIX sh
	}
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	fake := "#!/bin/sh\necho \"$*\" >> '" + calls + "'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, binaryName), []byte(fake), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(bin, binaryName), fakeLoreMode); err != nil {
		t.Fatal(err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("no git on PATH: %v", err)
	}
	path := bin + string(os.PathListSeparator) + filepath.Dir(git) + string(os.PathListSeparator) + "/usr/bin:/bin"
	runAll := func(work, rel string) string {
		t.Helper()
		_ = os.Remove(calls)
		for name, b := range hookBlocksFor(rel, true) {
			for _, l := range b.lines {
				if !strings.HasPrefix(l, "if [ ") {
					t.Fatalf("%s line not guarded: %s", name, l)
				}
				cmd := exec.CommandContext(context.Background(), "sh", "-s", filepath.Join(work, ".git", "COMMIT_EDITMSG"))
				cmd.Stdin = strings.NewReader(l + "\n")
				cmd.Dir = work
				cmd.Env = []string{"PATH=" + path, "HOME=" + work, "GIT_CONFIG_NOSYSTEM=1"}
				_ = cmd.Run() // the exit code is the hook's; what matters is whether lore ran
			}
		}
		b, _ := os.ReadFile(filepath.Clean(calls))
		return string(b)
	}
	plain := t.TempDir()
	gitT(t, plain, "init", "-q")
	if got := runAll(plain, ""); got != "" {
		t.Fatalf("a repo with no lore data started lore from the shared hooks:\n%s", got)
	}
	// a sub-project block runs lore only where that folder holds .lore/
	if got := runAll(plain, "pkg/a"); got != "" {
		t.Fatalf("a sub-project block ran lore in a repo without pkg/a/.lore:\n%s", got)
	}
	set := t.TempDir()
	gitT(t, set, "init", "-q")
	if err := os.MkdirAll(filepath.Join(set, ".lore", "data", "memories"), 0o750); err != nil {
		t.Fatal(err)
	}
	// a row file to commit: the pre-commit block runs lore only when
	// .lore/data has changes (D-16), so a commit of code alone stays fast
	if err := os.WriteFile(filepath.Join(set, ".lore", "data", "memories", "mem_x.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := runAll(set, ""); !strings.Contains(got, "sync hook pre-commit") || !strings.Contains(got, "check leaks") {
		t.Fatalf("a lore repo's shared hooks did not run lore:\n%s", got)
	}
	wired := t.TempDir() // a clone lore wired with no .lore/ of its own (a client repo)
	gitT(t, wired, "init", "-q")
	if err := os.MkdirAll(filepath.Join(wired, ".git", liveStateDirName), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wired, ".git", liveStateDirName, gitWiringStamp), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := runAll(wired, ""); !strings.Contains(got, "check leaks") {
		t.Fatalf("a wired clone's shared hooks skipped the leak check:\n%s", got)
	}
	// the clone's own hooks folder keeps the old binary's lines byte for byte
	if hookBlocksFor("", false)[hookPreCommit].lines[0] != hookBlocks("")[hookPreCommit].lines[0] {
		t.Fatal("unshared hook lines changed")
	}
}
