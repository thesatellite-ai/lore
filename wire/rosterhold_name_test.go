package wire

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// devco T-23: a hold is named under the store whose clone holds its
// commit (the clone's remote), not under whatever the attached name
// points at after sync fetched a new link.
func TestHeldStoreNameIsTheClonesStore(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".lore")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "orgs:\n  - name: initech\n    stores:\n      - url: git@git.example.test:initech/team-store.git\n        org_folder: org\n      - url: git@git.example.test:initech/client-store.git\n"
	if err := os.WriteFile(configPath(home), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &App{opts: Options{Home: home}}
	got := a.heldStoreName(context.Background(), heldChange{src: "initech", url: "git@git.example.test:initech/team-store.git"})
	if got != "initech" { // the org's main store is named by the org, as store list does
		t.Fatalf("hold of the team store's clone named %q, want initech", got)
	}
	got = a.heldStoreName(context.Background(), heldChange{src: "initech", url: "git@git.example.test:initech/client-store.git", folder: "shop"})
	if !strings.HasPrefix(got, "client-store/shop") {
		t.Fatalf("hold of the client store's clone named %q, want client-store/shop (here: initech)", got)
	}
}
