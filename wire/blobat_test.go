package wire

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/internal/partialclone"
	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestBlobAtMissingIsAnError: blobAt, the old-version read of trash
// --from-history, the meta key probe and the key loosening check, tells a
// path no tree names (found=false) from a version the clone lacks (an
// error): the old reads took both as "no such file" and dropped the row or
// skipped the check (store growth SG-3a, devco:D-997).
func TestBlobAtMissingIsAnError(t *testing.T) {
	r := partialclone.Make(t, "rows/keys/k1.json", "{\"v\":1}\n", "{\"v\":2}\n")
	t.Setenv(gitenv.EnvOffline, gitenv.On)
	ctx := context.Background()
	if body, found, err := blobAt(ctx, r.Clone, "HEAD:./rows/keys/k1.json"); err != nil || !found || string(body) != "{\"v\":2}\n" {
		t.Errorf("blobAt of the tip = %q, %v, %v; want v2", body, found, err)
	}
	for _, name := range []string{"HEAD~1:rows/keys/none.json", "refs/heads/none:rows/keys/k1.json"} {
		if _, found, err := blobAt(ctx, r.Clone, name); found || err != nil {
			t.Errorf("blobAt(%s) = found %v, %v; want not found, no error", name, found, err)
		}
	}
	if _, found, err := blobAt(ctx, r.Clone, "HEAD~1:./rows/keys/k1.json"); found || !errs.Has(err, errs.Offline) {
		t.Errorf("blobAt of a version the clone lacks offline = found %v, %v; want E_OFFLINE", found, err)
	}
}
