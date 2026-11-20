package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withVersion runs the in-process binary as release v for the rest of the
// test (version is the ldflag-stamped package variable).
func withVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

// workflowAt reads the project's lore-sync-merge workflow.
func workflowAt(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(filepath.Join(root, ".github", "workflows", "lore-sync-merge.yml")))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestWorkflowPinFollowsLoreUpgrade (E-FATES-5; devco:D-742): after an
// upgrade a read-only command leaves the auto-pinned workflow alone and a
// writing command (from a plain subfolder too, E-FATES-6) leaves it byte for
// byte as it was, printing the line that names `lore sync install-action`;
// that command, run as printed, moves the project's one file; a development
// build never downgrades it.
func TestWorkflowPinFollowsLoreUpgrade(t *testing.T) {
	asPerson(t)
	root := envScopeRepo(t)
	withVersion(t, "0.1.98")
	if code, _, errOut := runCaptured(t, "sync", "install-action", "--branch", "main"); code != 0 {
		t.Fatalf("install-action: %d %s", code, errOut)
	}
	if !strings.Contains(workflowAt(t, root), "/v0.1.98/") {
		t.Fatal("setup: workflow not at v0.1.98")
	}
	version = "0.1.99"
	for _, args := range [][]string{{"rule", "list"}, {"memory", "list"}, {"sync", "status"}} {
		if code, _, errOut := runCaptured(t, args...); code != 0 {
			t.Fatalf("lore %v: %d %s", args, code, errOut)
		}
		if strings.Contains(workflowAt(t, root), "/v0.1.99/") {
			t.Fatalf("read-only lore %v refreshed the workflow", args)
		}
	}
	t.Setenv("LORE_READ_ONLY", "1")
	_, _, _ = runCaptured(t, "memory", "add", "--body", "refused under read-only") // refused: the point is the file
	if strings.Contains(workflowAt(t, root), "/v0.1.99/") {
		t.Fatal("a write under LORE_READ_ONLY=1 refreshed the workflow")
	}
	t.Setenv("LORE_READ_ONLY", "")
	sub := filepath.Join(root, "src", "deep")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	before := workflowAt(t, root)
	code, _, errOut := runCaptured(t, "memory", "add", "--body", "after the upgrade")
	if code != 0 || strings.Count(errOut, "lore: .github/workflows/lore-sync-merge.yml pins lore v0.1.98; run `lore sync install-action` to update it to v0.1.99") != 1 {
		t.Fatalf("stale pin not reported once: %d %s", code, errOut)
	}
	if workflowAt(t, root) != before {
		t.Fatal("a writing command rewrote the tracked workflow (devco:D-742)")
	}
	if code, _, errOut := runCaptured(t, "sync", "install-action"); code != 0 || !strings.Contains(workflowAt(t, root), "/v0.1.99/") {
		t.Fatalf("lore sync install-action as printed did not move the pin: %d %s", code, errOut)
	}
	if entries, err := os.ReadDir(filepath.Join(root, ".github", "workflows")); err != nil || len(entries) != 1 {
		t.Fatalf("workflow files: %v %v", entries, err)
	}
	version = "dev"
	if code, _, errOut := runCaptured(t, "memory", "add", "--body", "dev build"); code != 0 {
		t.Fatalf("memory add: %d %s", code, errOut)
	}
	if !strings.Contains(workflowAt(t, root), "/v0.1.99/") {
		t.Fatal("a development build downgraded the workflow")
	}
}

// TestWorkflowUserPinUntouched: a workflow pinned with --lore-version never
// moves, whatever the command (E-FATES-5).
func TestWorkflowUserPinUntouched(t *testing.T) {
	asPerson(t)
	root := envScopeRepo(t)
	withVersion(t, "0.1.98")
	if code, _, errOut := runCaptured(t, "sync", "install-action", "--branch", "main", "--lore-version", "v0.1.98"); code != 0 {
		t.Fatalf("install-action: %d %s", code, errOut)
	}
	before := workflowAt(t, root)
	version = "0.1.99"
	code, _, errOut := runCaptured(t, "memory", "add", "--body", "after the upgrade")
	if code != 0 || strings.Contains(errOut, "lore-sync-merge") {
		t.Fatalf("memory add: %d %s", code, errOut)
	}
	if workflowAt(t, root) != before {
		t.Fatal("a user-pinned workflow was moved")
	}
}
