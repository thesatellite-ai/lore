package wire

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// TestHomeHintNamesOnlyFlagsTheCommandTakes (devco:D-539): the home-folder
// E_NOT_A_STORE hint of a command with no --to names no --to, of a command
// with neither store flag names the repo-only way out, and a command given
// --setup is not told to add it.
func TestHomeHintNamesOnlyFlagsTheCommandTakes(t *testing.T) {
	reg, err := Registry(&App{})
	if err != nil {
		t.Fatal(err)
	}
	entry := func(name string) op.Entry {
		e, ok := reg.Lookup(name)
		if !ok {
			t.Fatalf("no op %s", name)
		}
		return e
	}
	home := errs.New(errs.NotAStore, "git: the folder is not inside a git work tree").WithHint("cd into a git checkout, or " + homeHintMarker + " (or --store; --to me for your own) or a setup with --setup <setup>")
	hintOf := func(ctx context.Context, name string) string {
		var le *errs.Error
		err := fitHomeHint(ctx, entry(name), home)
		if !errors.As(err, &le) {
			t.Fatalf("%s: %v", name, err)
		}
		return le.Hint
	}
	ctx := context.Background()
	if h := hintOf(ctx, "memory.edit"); strings.Contains(h, "--to") || !strings.Contains(h, "--setup") {
		t.Errorf("memory edit's hint names --to or lacks --setup: %s", h)
	}
	// context takes --store since devco D-555 (and no --to): its hint names
	// --store and --setup, never --to
	if h := hintOf(ctx, "context.fetch"); strings.Contains(h, "--to") || !strings.Contains(h, "--store") {
		t.Errorf("context's hint names a flag it does not take or misses --store: %s", h)
	}
	if h := hintOf(op.WithSetup(ctx, "acme-admin"), "memory.edit"); strings.Contains(h, "add --setup") {
		t.Errorf("memory edit given --setup is told to add it: %s", h)
	}
	if h := hintOf(ctx, "memory.add"); !strings.Contains(h, "--to <org>/<folder>") || !strings.Contains(h, "--setup") {
		t.Errorf("memory add's hint lacks --to or --setup: %s", h)
	}
}

// TestOrgFilePushNoteNamesStorePush (devco:D-544): an org-file change not
// pushed names `lore store push --store <url>`, never its own command with
// --push alone (which refused "nothing to change").
func TestOrgFilePushNoteNamesStorePush(t *testing.T) {
	got := audiencePushNote(orgcfg.OrgFileResult{}, "git@example.test:acme/lore-team.git")
	if !strings.Contains(got, "push it with: lore store push --store git@example.test:acme/lore-team.git)") || strings.Contains(got, "--push") {
		t.Errorf("push note = %q", got)
	}
}
