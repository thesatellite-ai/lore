package wire

// storehere_alias_test.go — devco D-479: inside a store folder the folder's
// store answers for the repo store, so the lists a reader joins with the
// repo store (ShareableStores) leave it out and RepoStandsFor names it.
// Before, `lore context` in a store folder read the folder as `repo` and
// again by its own name: every rule and memory printed twice.

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

func TestStoreFolderRepoAliasIsReadOnce(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts := histGitWorld(t)
	url, _ := bareStore(t, hosts, "acme/kb")
	home := t.TempDir()
	repo := openWith(t, gitRepo(t), Options{Home: home})
	if _, err := run(t, repo, opsstores.OpInit, `{"name":"kb","url":"`+url+`"}`, true); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := run(t, repo, opsstores.OpFolderAdd, `{"store":"kb","folder":"app1"}`, true); err != nil {
		t.Fatalf("folder add: %v", err)
	}
	// shareable, so a committed render's source list would name it
	if _, err := run(t, repo, opsstores.OpVisibility, `{"name":"kb","visibility":"shareable"}`, true); err != nil {
		t.Fatalf("visibility: %v", err)
	}
	ctx := context.Background()
	if alias, err := repo.RepoStandsFor(ctx); err != nil || alias != "" {
		t.Fatalf("RepoStandsFor in a code repo = %q, %v; want \"\"", alias, err)
	}

	folder := filepath.Join(home, gitbackend.StoresDir, "acme", "kb", "app1")
	in := openWith(t, folder, Options{Home: home})
	alias, err := in.RepoStandsFor(ctx)
	if err != nil || alias == "" {
		t.Fatalf("RepoStandsFor in the store folder = %q, %v; want the folder's store", alias, err)
	}
	idents, err := in.StoreIdentities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(idents, func(si StoreIdentity) bool { return si.Name == alias }) {
		t.Fatalf("the folder's store %q is not attached: %+v", alias, idents)
	}
	shared, err := in.ShareableStores(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(shared, alias) {
		t.Fatalf("ShareableStores in the store folder lists %q, which the repo store already reads: %v", alias, shared)
	}
}
