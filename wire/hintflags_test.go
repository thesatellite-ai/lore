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

// TestHomeHintNeverSaysStoreDoesNotApply (devco:D-586): from the home
// folder `resource check --id <name>` refused "this command works on the
// folder it runs in, so --store does not apply" although the resource was
// in a store. Every operation's home-folder hint, with and without
// --setup, names --store when the operation takes it and never says
// "--store does not apply"; the resource reads and the writes that find a
// resource by name take --store.
func TestHomeHintNeverSaysStoreDoesNotApply(t *testing.T) {
	reg, err := Registry(&App{})
	if err != nil {
		t.Fatal(err)
	}
	takesStore := func(e op.Entry) bool {
		for _, f := range e.InputFields() {
			if f.Name == readStoreField {
				return true
			}
		}
		return false
	}
	for _, e := range reg.Entries() {
		for _, setup := range []bool{false, true} {
			h := homeHint(e, setup)
			if strings.Contains(h, "does not apply") {
				t.Errorf("%s (setup given %v): the home hint says a flag does not apply: %s", e.Spec().Name, setup, h)
			}
			if takesStore(e) && !strings.Contains(h, "--store <org>") {
				t.Errorf("%s takes --store but its home hint does not name it: %s", e.Spec().Name, h)
			}
		}
	}
	for _, name := range []string{"resource.check", "resource.stale", "resource.report", "resource.graph", "resource.edit", "resource.archive", "resource.rename", "resource.merge"} {
		e, ok := reg.Lookup(name)
		if !ok {
			t.Fatalf("no op %s", name)
		}
		if !takesStore(e) {
			t.Errorf("%s takes no --store <org>[/<folder>]", name)
		}
	}
}

// TestHomeHintAlwaysNamesStoreAndSetup (devco:D-622): D-586 reworded the
// two hints of a command with no store flag to "takes no store flag",
// which named no flag at all, so the home-folder refusal of ~90 writes
// (memory delete, task edit, render run, …) left the reader without
// --store and the where-you-stand write matrix red. Every operation's
// home-folder hint, with and without --setup, names the store flag
// (--store or --to), and names --setup unless it was given (D-539).
func TestHomeHintAlwaysNamesStoreAndSetup(t *testing.T) {
	reg, err := Registry(&App{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range reg.Entries() {
		for _, setup := range []bool{false, true} {
			h := homeHint(e, setup)
			if !strings.Contains(h, "--store") && !strings.Contains(h, "--to ") {
				t.Errorf("%s (setup given %v): the home hint names no store flag: %s", e.Spec().Name, setup, h)
			}
			if !setup && !strings.Contains(h, "--setup") {
				t.Errorf("%s (setup given %v): the home hint names no --setup: %s", e.Spec().Name, setup, h)
			}
		}
	}
}

// TestNoStoreHintNamesOnlyFlagsTheCommandTakes (devco:D-674): the refusal
// outside a repo where no org's setup covers the folder (runOnDefaultStore)
// had one fixed hint, "--store … (a write: --to …) or --setup", for every
// command, so `lore context` from the home folder named --to, which context
// does not take. Every operation's hint, with and without --setup, names
// --to only when the operation takes it, --store only when it takes it,
// and --setup only when it was not given.
func TestNoStoreHintNamesOnlyFlagsTheCommandTakes(t *testing.T) {
	reg, err := Registry(&App{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range reg.Entries() {
		to, st, _ := storeFlagsOf(e)
		for _, setup := range []bool{false, true} {
			h := noStoreHint(e, setup)
			if !to && strings.Contains(h, "--to") {
				t.Errorf("%s takes no --to, but its no-store hint names it (setup given %v): %s", e.Spec().Name, setup, h)
			}
			if !st && strings.Contains(h, "--store <org>[/<folder>]") && !strings.Contains(h, notRoutedByStore) {
				t.Errorf("%s takes no --store, but its no-store hint names it (setup given %v): %s", e.Spec().Name, setup, h)
			}
			if setup && strings.Contains(h, "--setup <setup>") && !strings.Contains(h, "lore store list --setup <setup>") {
				t.Errorf("%s was given --setup, but its no-store hint tells to add it: %s", e.Spec().Name, h)
			}
			if !setup && (to || st) && !strings.Contains(h, "--setup <setup>") {
				t.Errorf("%s's no-store hint does not name --setup: %s", e.Spec().Name, h)
			}
		}
	}
	e, ok := reg.Lookup("context.fetch")
	if !ok {
		t.Fatal("no op context.fetch")
	}
	if h := noStoreHint(e, false); strings.Contains(h, "--to") || !strings.Contains(h, "--store <org>[/<folder>]") || !strings.Contains(h, "--setup <setup>") {
		t.Errorf("context's no-store hint = %q, want --store and --setup and no --to", h)
	}
}
