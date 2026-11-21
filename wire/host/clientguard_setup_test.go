package host

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsorg "github.com/thesatellite-ai/lore/ops/orgcmd"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
	"github.com/thesatellite-ai/lore/wire"
)

// TestSetupInstallsClientGuard (devco:D-802): a member's setup step run
// in a client repo with no .lore/ installs both guard blocks in
// .git/hooks and marks the clone, as the link does; a second step finds
// it on and writes nothing more. A command that is no setup step leaves
// the clone as it is.
func TestSetupInstallsClientGuard(t *testing.T) {
	for _, name := range []string{opssetup.OpApply, opssetup.OpJoin, opssetup.OpFix, opssetup.OpRepair} {
		t.Run(name, func(t *testing.T) {
			h, errw, dir := clientGuardRepo(t)
			if err := h.requireClientGuard(context.Background(), opsorg.OpUnlink); err != nil {
				t.Fatalf("a command that is no setup step: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", hookCommitMsg)); err == nil {
				t.Fatal("a command that is no setup step installed the guard")
			}
			if err := h.requireClientGuard(context.Background(), name); err != nil {
				t.Fatalf("%s in a client clone: %v", name, err)
			}
			for _, n := range clientGuardHookNames {
				marker, _ := clientGuardBlock(n)
				if b, err := os.ReadFile(filepath.Join(dir, ".git", "hooks", n)); err != nil || !strings.Contains(string(b), marker) {
					t.Fatalf("%s left no lore block in .git/hooks/%s (err %v); stderr: %s", name, n, err, errw)
				}
			}
			out, _ := exec.Command("git", "-C", dir, "config", "--local", "--get", wire.ClientRepoGitKey).Output()
			if got := strings.TrimSpace(string(out)); got != clientRepoMarkOn {
				t.Fatalf("%s did not mark the clone a client repo: %q", name, got)
			}
			if !strings.Contains(errw.String(), "leak guard installed in this clone") {
				t.Fatalf("%s does not say it installed the guard: %s", name, errw)
			}
			errw.Reset()
			if err := h.requireClientGuard(context.Background(), name); err != nil || errw.Len() != 0 {
				t.Fatalf("a second step in a guarded clone: err %v, stderr %q", err, errw)
			}
		})
	}
}

// TestSetupClientGuardFailsClosed (devco:D-802): when lore cannot install
// the guard, the setup step stops with E_REFUSED naming why and the way
// out, never a warning line the step runs past: a tracked hooks folder
// (lore never writes the client's files) names the lines to add and
// leaves the folder as it was; a hooks folder lore cannot write names
// `lore doctor --fix` as the next command.
func TestSetupClientGuardFailsClosed(t *testing.T) {
	t.Run("tracked hooks folder", func(t *testing.T) {
		h, _, dir := clientGuardRepo(t)
		own := []byte("#!/bin/sh\nexit 0\n")
		if err := os.MkdirAll(filepath.Join(dir, ".githooks"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".githooks", hookCommitMsg), own, 0o600); err != nil {
			t.Fatal(err)
		}
		gitT(t, dir, "config", "--local", "core.hooksPath", ".githooks")
		err := h.requireClientGuard(context.Background(), opssetup.OpApply)
		if !errs.Has(err, errs.Refused) {
			t.Fatalf("setup in a clone whose tracked hooks folder lore may not write: %v, want E_REFUSED", err)
		}
		for _, want := range []string{"setup stopped", "NOT checked", clientGuardHandLines[hookCommitMsg], clientGuardHandLines[hookPrePush]} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("the refusal does not say %q: %v", want, err)
			}
		}
		if b, _ := os.ReadFile(filepath.Join(dir, ".githooks", hookCommitMsg)); string(b) != string(own) {
			t.Fatalf("lore changed the client's tracked commit-msg hook:\n%s", b)
		}
	})
	t.Run("hooks folder lore cannot write", func(t *testing.T) {
		h, _, dir := clientGuardRepo(t)
		hooks := filepath.Join(dir, ".git", "hooks")
		if err := os.RemoveAll(hooks); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(hooks, []byte("not a folder\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := h.requireClientGuard(context.Background(), opssetup.OpJoin)
		if !errs.Has(err, errs.Refused) || !strings.Contains(err.Error(), "could not install the leak guard") {
			t.Fatalf("setup where the hooks folder cannot be written: %v, want E_REFUSED naming the failed install", err)
		}
		var e *errs.Error
		if !errors.As(err, &e) || e.Next == nil || strings.Join(append([]string{e.Next.Command}, e.Next.Args...), " ") != "lore doctor --fix" || !strings.Contains(e.Hint, "`lore doctor --fix`") {
			t.Fatalf("the refusal does not name `lore doctor --fix` as the next step: %+v", e)
		}
	})
}
