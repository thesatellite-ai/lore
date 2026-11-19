package wire

import (
	"context"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
)

// TestStoreOnlyAppKeepsTheCommandsAuditSink pins devco D-694 (the write
// guard's roster): a store-only App opened beside the command's own (the
// roster rosterApp reads, StoreOnlyRoster) never rebinds the command's
// audit sink. When it did, closing it left the sink pointing at a closed
// App, and the command's own write to another store printed
// "the audit record of … will be added by a later write: wire: the app
// is closed (W_AUDIT_DEFERRED)".
func TestStoreOnlyAppKeepsTheCommandsAuditSink(t *testing.T) {
	t.Parallel()
	ctx := background()
	orgURL := folderStoreAt(t, orgStoreID, rowfs.VisibilityInternal)
	hook := func(context.Context) ([]attach.Store, error) {
		return []attach.Store{{Name: rosterStore, URL: orgURL, Folder: "org"}}, nil
	}
	home, dir := t.TempDir(), t.TempDir()
	sink := NewChainAudit(home, dir, time.Now)
	opts := Options{RepoDir: dir, Home: home, FlagActor: testEmail, OrgStores: hook, Wall: time.Now, Audit: sink}
	main, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = main.Close() })
	bound, ok := sink.(boundAppSource)
	if !ok || bound.boundApp() != main {
		t.Fatal("New did not bind the command's sink to the command's App")
	}

	side, alone, err := StoreOnlyRoster(ctx, opts, rosterStore)
	if err != nil || !alone {
		t.Fatalf("store-only roster: alone %v err %v", alone, err)
	}
	if err := side.Close(); err != nil {
		t.Fatal(err)
	}
	if got := bound.boundApp(); got != main {
		t.Fatalf("the side App took the command's audit sink (bound to %p, want the command's App %p)", got, main)
	}
}
