//go:build teame2e

package teame2e

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// CONTEXT_BUDGET 6.2.7 / 8.5, CQ10 (and its clarification): folder files
// in a client repo are decided per client org. Until the row is set they
// are off (render --report says so, render writes no folder file); `lore policy set
// context.dir_files on --to team`, run from the client's repo, writes one
// policies row in our store's folder for that client, never in the client
// repo, and the next render writes the folder file. Off again removes it.
func TestCQ10_ClientDirFilesArePerClientRow(t *testing.T) {
	a := newAgency(t, th.Cmd("policy", "set"))
	app := a.aliceApp
	app.WriteFile("src/api/handler.go", "package api\n")
	app.CommitAll("api folder")
	app.MustLore("policy", "set", "context.targets", "claude_code", "--to", "repo", "--yes")
	for _, body := range []string{"api handlers return typed errors", "api routes are versioned", "api input is validated first"} {
		app.MustLore("rule", "add", "--body", body, "--severity", "must", "--scope", "dir:src/api", "--to", "repo")
	}

	// Client repo, no row yet: off until set (CQ10, OV25-5).
	app.MustLore("render", "--report").Says(t, "folder files: off")
	app.MustLore("render")
	if _, ok := app.ReadFile("src/api/LORE.md"); ok {
		t.Fatal("a client repo got a folder file before its client turned them on")
	}

	// The per-client row: our store, that client's folder (CQ10 clarification).
	// on|off are the flag's spellings (P3-CTX-3); the row's place is the
	// org store, not this repo.
	app.MustLore("policy", "set", "context.dir_files", "on", "--to", "team", "--yes").Says(t, "context.dir_files = true", "northwind").Never(t, "this repo")
	app.MustLore("policy", "show", "context.dir_files").Says(t, "true").Never(t, "default")
	if f := app.LeakScan("context.dir_files"); len(f) != 0 {
		t.Fatalf("the per-client row must never land in the client repo: %v", f)
	}

	app.MustLore("render")
	got, ok := app.ReadFile("src/api/LORE.md")
	if !ok || !strings.Contains(got, "api routes are versioned") {
		t.Fatalf("after the client row is on, render writes the folder file: %q", got)
	}

	// Off again: lore's folder file goes, the rules return to the root.
	app.MustLore("policy", "set", "context.dir_files", "off", "--to", "team", "--yes")
	app.MustLore("render", "--report").Says(t, "folder files: off")
	app.MustLore("render")
	if _, ok := app.ReadFile("src/api/LORE.md"); ok {
		t.Fatal("turning the client row off must remove lore's folder file")
	}
	if root, _ := app.ReadFile(".lore/LORE.md"); !strings.Contains(root, "api routes are versioned") {
		t.Fatal("with folder files off the folder rules render in the root file")
	}
}
