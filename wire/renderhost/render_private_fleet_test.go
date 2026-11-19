package renderhost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/wire"
)

// Store ids of the attach-order test (arbitrary sto_ ids).
const (
	privOrderAlphaID = "sto_01a1a5aca7f57659be7320c5f4b700e1"
	privOrderZetaID  = "sto_01a1a5aca7f57659be7320c5f4b700e2"
)

// TestPrivateRenderIgnoresAttachOrder (K-12): lore fix said HEALTHY and
// the lore fix --dry-run right after it called the private index stale,
// because that open attached the org store before `me` and the index
// listed the stores in attach order. The private files now go in the
// order of their names, so two opens attaching the same stores in another
// order render the same index and find nothing stale.
func TestPrivateRenderIgnoresAttachOrder(t *testing.T) {
	ctx := context.Background()
	alpha := internalStoreAt(t, privOrderAlphaID, rowfs.VisibilityInternal)
	zeta := internalStoreAt(t, privOrderZetaID, rowfs.VisibilityInternal)
	a, repo := privWorld(t, []privSrc{
		{name: "zeta", url: zeta, word: "zetaword"},
		{name: "alpha", url: alpha, word: "alphaword"},
	}, nil)
	home := t.TempDir()
	h := New(Options{App: a, Home: home})
	var w strings.Builder
	if err := h.renderPrivate(ctx, &w, repo, false, false); err != nil {
		t.Fatal(err)
	}
	if found, err := h.privateCopies(ctx, repo); err != nil || len(found) != 0 {
		t.Fatalf("right after the render: %+v %v", found, err)
	}

	// the same stores, attached in the other order
	swapped := "stores:\n  - name: alpha\n    url: " + alpha + "\n  - name: zeta\n    url: " + zeta + "\n"
	if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(attach.FileName)), []byte(swapped), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := wire.New(wire.Options{RepoDir: repo, Home: t.TempDir(), FlagActor: "human:alice@example.com",
		RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	hb := New(Options{App: b, Home: home})
	found, err := hb.privateCopies(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Errorf("another attach order finds %s: %s", f.File, f.Message)
	}
}

// TestPrivateRenderDropsTheProjectIDFolder (K-16): a checkout renders its
// private context under its prj_ project id until the repo id arrives by
// git pull; the next render moves to the rep_ folder and removes the
// prj_ one, and a prj_ folder still left beside it (a machine that
// rendered before this lore) is a doctor finding the next render clears.
func TestPrivateRenderDropsTheProjectIDFolder(t *testing.T) {
	ctx := context.Background()
	a, repo := privApp(t)
	home := t.TempDir()
	h := New(Options{App: a, Home: home})
	var w strings.Builder
	if err := h.renderPrivate(ctx, &w, repo, false, false); err != nil {
		t.Fatal(err)
	}
	before, err := h.planPrivate(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(before.dirName, before.projectID) {
		t.Fatalf("unstamped render folder %s, want one named by the project id", before.dirName)
	}
	oldDir := filepath.Join(home, renderDirName, before.dirName)
	if _, err = os.Stat(filepath.Join(oldDir, privateIndexFile)); err != nil {
		t.Fatalf("first render: %v", err)
	}

	// the repo id arrives (what the pull of `lore: record repo address` brings)
	pid, err := a.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.SeedRepoMeta(ctx, pid); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.EnsureRepoIdentity(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := h.planPrivate(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if after.dirName == before.dirName {
		t.Fatalf("the render folder did not move to the repo id: %s", after.dirName)
	}
	if err = h.renderPrivate(ctx, &w, repo, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("the prj_ render folder is still beside the rep_ one: %v", err)
	}

	// a leftover prj_ folder (rendered by an earlier lore) is named, and
	// the next render removes it
	if err = os.MkdirAll(oldDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(oldDir, privateIndexFile), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := h.privateCopies(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || !strings.Contains(found[0].Message, "earlier private render folder") || found[0].Fix != renderFixCommand {
		t.Fatalf("leftover folder findings %+v, want one naming it with lore render", found)
	}
	if err = h.renderPrivate(ctx, &w, repo, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("lore render left the earlier folder: %v", err)
	}
	if found, err = h.privateCopies(ctx, repo); err != nil || len(found) != 0 {
		t.Fatalf("after the render: %+v %v", found, err)
	}
}
