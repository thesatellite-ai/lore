package wire

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestClaimHintsCarryPushD607: every hint that sends the person to another
// claim names --push where a claim needs it (devco:D-607: E_LOST_CLAIM,
// the hold refusals and task next's "take it:" printed `lore task next
// --claim`, which a repo with a remote or a store link refuses without
// --push), and names none where it does not.
func TestClaimHintsCarryPushD607(t *testing.T) {
	t.Parallel()
	const id = "tsk_0123456789abcdef0123456789abcdef"
	cases := map[string]error{
		"lost":     &coord.FencedError{Kind: coord.FenceLost, Task: id},
		"held":     &signals.HeldError{By: signals.Signal{Task: id}},
		"sameheld": &signals.HeldError{By: signals.Signal{Task: id}, SameActor: true},
		"children": &coord.HasOpenChildrenError{Task: id, Children: []string{id}},
		"notopen":  coord.ErrNotOpen,
	}
	for name, in := range cases {
		var e *errs.Error
		if !errors.As(coordErrPush(in, " --push"), &e) {
			t.Fatalf("%s: not an errs.Error", name)
		}
		for _, claim := range []string{"lore task next --claim", "lore task claim --id " + id, "(--claim"} {
			for rest := e.Hint; ; {
				i := strings.Index(rest, claim)
				if i < 0 {
					break
				}
				rest = rest[i+len(claim):]
				tail := strings.TrimPrefix(rest, " --coordinate")
				if !strings.HasPrefix(tail, " --push") {
					t.Errorf("%s: hint %q names %q without --push", name, e.Hint, claim)
				}
			}
		}
		if !strings.Contains(e.Hint, "--push") {
			t.Errorf("%s: hint %q names no claim with --push", name, e.Hint)
		}
		if strings.Contains(errs.EnvelopeOf(coordErr(in)).Error.Hint, "--push") {
			t.Errorf("%s: a place whose claims need no --push names it: %q", name, errs.EnvelopeOf(coordErr(in)).Error.Hint)
		}
	}
	got := strings.Join(readLines(coord.Peek{Holding: id}, false, " --push"), "\n")
	if !strings.Contains(got, "lore task next --claim --push resumes it") {
		t.Errorf("task next while holding = %q, want the resume with --push", got)
	}
}

// TestSignalsWriteLabelD608: a revoke in a repo linked to a store folder
// names the store's signals ref as a claim and a release do ("of store
// <org>/<folder>"), not "pushed <ref> to origin" (devco:D-608).
func TestSignalsWriteLabelD608(t *testing.T) {
	t.Parallel()
	w := &coordWorld{remoteName: "origin", spot: sigSpot{store: true, label: "acme/web", ref: "refs/lore/signals/main", remoteName: "origin"}}
	line := w.overridePushedLine(false)
	if want := "on " + w.claimPlace(w.spot.ref); !strings.Contains(line, want) {
		t.Errorf("revoke line = %q, want it to name %q", line, want)
	}
	if !strings.Contains(line, "of store acme/web") || strings.Contains(line, "pushed refs/lore/signals/main to origin") {
		t.Errorf("revoke line = %q, want the store label and no \"pushed <ref> to origin\"", line)
	}
}

// TestPurgeDropsCopiesOnAStoreOnlyOpenD609: a purge in a linked repo or the
// home folder runs on a store-only open, whose repoTop refuses; the safety
// copies of the purged row in the checkout it runs in still go, and the
// purge never fails after its tombstone is written (devco:D-609).
func TestPurgeDropsCopiesOnAStoreOnlyOpenD609(t *testing.T) {
	t.Parallel()
	const id = "mem_0123456789abcdef0123456789abcdef"
	repo := gitRepo(t)
	home := t.TempDir()
	a := openWith(t, repo, Options{Home: home, Version: "0.1.18", OrgCommands: &standaloneOrg{home: home},
		RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	plantV017Trash(t, repo, true, id)
	// what openStoreOnlyOn leaves: the repo-only state refuses
	a.mu.Lock()
	a.cfgOpened, a.cfgErr = true, errs.New(errs.NotAStore, "wire: this command needs a repo")
	a.mu.Unlock()
	n, err := a.DropLocalCopies(background(), []string{id})
	if err != nil || n != 1 {
		t.Fatalf("DropLocalCopies on a store-only open = %d, %v; want 1 copy dropped, no error", n, err)
	}
	// outside any git repo there is no checkout and no copy: nothing to do
	b := openWith(t, t.TempDir(), Options{Home: home, Version: "0.1.18", OrgCommands: &standaloneOrg{home: home}})
	b.mu.Lock()
	b.cfgOpened, b.cfgErr = true, errs.New(errs.NotAStore, "wire: this command needs a repo")
	b.mu.Unlock()
	if n, err = b.DropLocalCopies(background(), []string{id}); err != nil || n != 0 {
		t.Fatalf("DropLocalCopies outside a repo = %d, %v; want 0, no error", n, err)
	}
}

// TestClientPersonalOnlyNoteD610: the note a personal-only read prints in
// an unlinked client repo says the org's folders were not read and names
// the same steps the refusal does (devco:D-610).
func TestClientPersonalOnlyNoteD610(t *testing.T) {
	t.Parallel()
	c := ClientRepo{Org: "acme"}
	got := clientPersonalOnlyNote(c)
	for _, want := range []string{"personal store only, not acme's folders", clientRepoStep(c), "--store acme/<folder>"} {
		if !strings.Contains(got, want) {
			t.Errorf("note = %q, want %q", got, want)
		}
	}
}
