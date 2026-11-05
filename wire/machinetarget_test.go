package wire

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestMachineTargetSkipsReadOnly (OV59): the automatic machine-profile
// target passes over an own store attached read_only and takes the next
// own store; naming the read-only store with --to is E_STORE_READ_ONLY;
// with only read-only own stores there is nothing to write (E_REFUSED,
// which the refresh after a write treats as "nothing to do").
func TestMachineTargetSkipsReadOnly(t *testing.T) {
	ctx := context.Background()
	ro := folderStoreAt(t, shareStoreID, rowfs.VisibilityInternal)
	rw := folderStoreAt(t, innerStoreID, rowfs.VisibilityInternal)

	repo := gitRepo(t)
	writeStoresYAML(t, repo, "stores:\n  - name: ro\n    url: "+ro+"\n    read_only: true\n  - name: kb\n    url: "+rw+"\n")
	a := openWith(t, repo, Options{})
	got, err := a.MachineTarget(ctx, "")
	if err != nil || got != "kb" {
		t.Fatalf("auto target = %q, %v; want kb", got, err)
	}
	if _, err = a.MachineTarget(ctx, "ro"); !errs.Has(err, errs.StoreReadOnly) {
		t.Fatalf("--to ro: err = %v, want E_STORE_READ_ONLY", err)
	}

	only := gitRepo(t)
	writeStoresYAML(t, only, "stores:\n  - name: ro\n    url: "+ro+"\n    read_only: true\n")
	if _, err = openWith(t, only, Options{}).MachineTarget(ctx, ""); !errs.Has(err, errs.Refused) {
		t.Fatalf("only read-only: err = %v, want E_REFUSED", err)
	}
}
