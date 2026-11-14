package wire

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
)

// TestRestoreOfStoreRowInOldFormatRepo (H-13): in a repo on the lore
// 0.1.17 format, a trash restore of a row that lives in another store
// changes none of the repo's files, so it is gated as a write into that
// store, as the delete of the same row is; a restore of the repo's own
// row is still refused for the format (D13).
func TestRestoreOfStoreRowInOldFormatRepo(t *testing.T) {
	t.Parallel()
	repo, _, _, _ := folderStoreRepo(t)
	seed := newTestApp(t, repo)
	withProject(t, seed)
	project := projectID(t, seed)
	out, err := run(t, seed, opsmemory.OpAdd, `{"body":"the org keeps one runbook","to":"northwind/acme"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	inStore := idOf(t, out)
	if _, err = run(t, seed, opsmemory.OpDelete, `{"id":"`+inStore+`"}`, true); err != nil {
		t.Fatal(err)
	}
	if err = seed.Close(); err != nil {
		t.Fatal(err)
	}
	removeV2Projects(t, repo, project)
	writeV1(t, repo, v1Memory(project, otherActor))
	a := newTestApp(t, repo)
	ctx := context.Background()
	if _, err = a.writeState(ctx); !errs.Has(err, errs.FormatOld) {
		t.Fatalf("the repo does not read as the 0.1.17 format: %v", err)
	}
	res, err := a.Restore(ctx, inStore)
	if err != nil {
		t.Fatalf("restoring a store's row was refused for the repo's format: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0].Row.ID.String() != inStore {
		t.Fatalf("restored %+v, want %s", res.Rows, inStore)
	}
}
