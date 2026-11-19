package wire

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestStoreOwnerFollowsSetURLMove (devco:D-514): set-url opens the new
// address before orgs[].stores names it, so the org must own the store at
// the new address for that open (the clone got the personal lore identity
// and said "your lore identity"), and still after the entry moved.
func TestStoreOwnerFollowsSetURLMove(t *testing.T) {
	const oldURL, newURL = "git@github.example:acme-old/kb.git", "git@github.example:acme-new/kb.git"
	entry := []string{oldURL}
	s := storeIdentityFunc(Options{OrgSigners: func(context.Context) ([]OrgSigner, error) {
		return []OrgSigner{{Name: "Ana Org", Email: "ana@acme.example", StoreURLs: entry}}, nil
	}})
	ctx := context.Background()
	if _, ok := s.owner(ctx, newURL); ok {
		t.Fatalf("no move: the new address has no owner yet")
	}
	moving := withStoreMove(ctx, oldURL, newURL)
	if sig, ok := s.owner(moving, newURL); !ok || sig.Email != "ana@acme.example" {
		t.Fatalf("during the move: owner(%s) = %+v, %v; want the org identity", newURL, sig, ok)
	}
	entry[0] = newURL // the entry now names the new address
	if sig, ok := s.owner(moving, newURL); !ok || sig.Email != "ana@acme.example" {
		t.Fatalf("after the entry moved: owner = %+v, %v; want the org identity", sig, ok)
	}
	if got := ownerAddresses(moving, "git@github.example:acme-new/other.git"); len(got) != 1 {
		t.Fatalf("another store must not take the moved store's owner: %v", got)
	}
}

// TestSetStoreURLMovesPushedClone (devco:D-515): a fully pushed old clone
// moves to the new address's folder like a clone holding work; it was
// left at the old folder with a `lore store forget` hint.
func TestSetStoreURLMovesPushedClone(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts := histGitWorld(t)
	oldURL, oldDir := bareStore(t, hosts, "acme/kb")
	home := t.TempDir()
	repo := gitRepo(t)
	writeStoresYAML(t, repo, "stores: []\n")
	a := openWith(t, repo, Options{Home: home})
	withProject(t, a)
	if _, err := run(t, a, opsstores.OpInit, `{"name":"kb","url":"`+oldURL+`"}`, true); err != nil {
		t.Fatalf("init: %v", err)
	}
	_ = a.Close()
	a = openWith(t, repo, Options{Home: home})
	histAddMemory(t, a, "kb", "pushed before the move")
	if _, err := a.SyncStores(background(), "kb"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	_ = a.Close()
	before, err := gitbackend.ScanClones(home)
	if err != nil || len(before) != 1 {
		t.Fatalf("want one clone before the move, got %+v (%v)", before, err)
	}

	newURL := "https://" + histHost + "/acme/kb-moved"
	if err = os.Rename(oldDir, filepath.Join(hosts, "acme", "kb-moved")); err != nil {
		t.Fatal(err)
	}
	a = openWith(t, repo, Options{Home: home})
	defer func() { _ = a.Close() }()
	res, err := run(t, a, opsstores.OpSetURL, `{"name":"kb","url":"`+newURL+`"}`, true)
	if err != nil {
		t.Fatalf("set-url: %v", err)
	}
	var out opsstores.SetURLOut
	if err = json.Unmarshal([]byte(res), &out); err != nil {
		t.Fatal(err)
	}
	found, err := gitbackend.ScanClones(home)
	if err != nil || len(found) != 1 {
		t.Fatalf("want one clone after the move, got %+v (%v)", found, err)
	}
	if !strings.Contains(found[0].Paths.Dir, "kb-moved") || !strings.Contains(out.Clone, before[0].Paths.Dir+" moved to "+found[0].Paths.Dir) || strings.Contains(out.Clone, "forget") {
		t.Fatalf("the clone serving kb is %s; set-url said %q", found[0].Paths.Dir, out.Clone)
	}
	if _, err = os.Stat(before[0].Paths.Dir); !os.IsNotExist(err) {
		t.Fatalf("the old clone folder is still there: %v", err)
	}
	// the carried clone sits at the new address's default folder: nothing
	// recorded, neither the old address nor an absolute default path (D-534)
	if rec := gitbackend.ReadCloneRecord(home).Clones; len(rec) != 0 {
		t.Fatalf("the clone record keeps the old address or pins the default path: %+v", rec)
	}
}
