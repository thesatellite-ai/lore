package wire

import (
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestStoreOnlyOpenSamePlaceTwiceD683: a store-only open over every folder
// of a setup lists the company folder among the folders and again as the
// level above them (companyFolderOf). The same place named twice is
// attached once; before, `task next --setup <setup>` from the home folder
// failed "[E_CONFIG_INVALID] store acme/org attached twice" with no hint
// (devco:D-683). One name over two different places is still refused.
func TestStoreOnlyOpenSamePlaceTwiceD683(t *testing.T) {
	t.Parallel()
	a, err := New(Options{RepoDir: t.TempDir(), Home: t.TempDir(), FlagActor: testEmail, Wall: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	api := attach.Store{Name: "acme/api", URL: folderStoreAt(t, "sto_01a1a5aca7f57659be7320c5f4b73001", rowfs.VisibilityInternal)}
	org := attach.Store{Name: "acme/org", URL: folderStoreAt(t, "sto_01a1a5aca7f57659be7320c5f4b73002", rowfs.VisibilityInternal)}
	web := attach.Store{Name: "acme/web", URL: folderStoreAt(t, "sto_01a1a5aca7f57659be7320c5f4b73003", rowfs.VisibilityInternal)}
	// the queue's shape: the first folder, the others, then the company folder again
	if err = a.openStoreOnlyOn(background(), api, api.Name, org, web, org); err != nil {
		t.Fatalf("devco:D-683: the company folder named twice for one place refused the open: %v", err)
	}
	other := attach.Store{Name: "acme/org", URL: folderStoreAt(t, "sto_01a1a5aca7f57659be7320c5f4b73004", rowfs.VisibilityInternal)}
	if err = a.openStoreOnlyOn(background(), api, api.Name, org, other); !errs.Has(err, errs.ConfigInvalid) {
		t.Fatalf("one name over two places: want E_CONFIG_INVALID, got %v", err)
	}
}
