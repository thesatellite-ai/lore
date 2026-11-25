package git

import (
	"context"
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/internal/partialclone"
	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// TestReadBlobsMissingBlobIsAnError: a blob the history names that the
// clone does not hold (a partial clone offline, or its remote gone) is an
// error from readBlobs, never an absent entry the replay and the row reads
// take as "no such row". A path no tree names stays absent. Break-verified:
// the old readBlobs dropped the missing blob and returned no error
// (store growth SG-3a, devco:D-997).
func TestReadBlobsMissingBlobIsAnError(t *testing.T) {
	r := partialclone.Make(t, "rows/task/t1.json", "{\"v\":1}\n", "{\"v\":2}\n")
	bin, err := lookGit(DefaultBinary)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	b := &Backend{work: r.Clone, git: runner{bin: bin}}
	old := r.Blobs[0]

	t.Run("offline", func(t *testing.T) {
		t.Setenv(gitenv.EnvOffline, gitenv.On)
		for _, names := range [][]string{{old}, {"HEAD~1:rows/task/t1.json"}, {"HEAD~1:./rows/task/t1.json"}, {"HEAD:rows/task/t1.json", old}} {
			got, err := b.readBlobs(ctx, names)
			if !errs.Has(err, errs.Offline) {
				t.Errorf("readBlobs(%v) offline = %v, %v; want E_OFFLINE (the blob is not in the clone)", names, keysOf(got), err)
			}
		}
		got, err := b.readBlobs(ctx, []string{"HEAD~1:rows/task/none.json", "refs/heads/none:rows/task/t1.json", "HEAD:rows/task/t1.json"})
		if err != nil || len(got) != 1 || string(got["HEAD:rows/task/t1.json"]) != "{\"v\":2}\n" {
			t.Errorf("readBlobs of absent paths and the tip offline = %v, %v; want only the tip, no error", keysOf(got), err)
		}
		_, err = b.rowAtBlob(ctx, old, row.Table("task"), ids.AnyID{})
		if errors.Is(err, store.ErrNotFound) || !errs.Has(err, errs.Offline) {
			t.Errorf("rowAtBlob of a held row's accepted blob offline = %v; want E_OFFLINE, never not-found", err)
		}
	})

	t.Run("remote gone", func(t *testing.T) {
		r.CutOff(t)
		if got, err := b.readBlobs(ctx, []string{old}); err == nil {
			t.Errorf("readBlobs of a blob the clone lacks with its remote gone = %v, nil; want an error", keysOf(got))
		}
	})
}

// TestReadBlobsLazyFetchOnline: online, a blob the partial clone lacks is
// fetched by git on demand (gitfast falls back instead of saying missing).
func TestReadBlobsLazyFetchOnline(t *testing.T) {
	r := partialclone.Make(t, "rows/task/t1.json", "{\"v\":1}\n", "{\"v\":2}\n")
	bin, err := lookGit(DefaultBinary)
	if err != nil {
		t.Fatal(err)
	}
	b := &Backend{work: r.Clone, git: runner{bin: bin}}
	got, err := b.readBlobs(context.Background(), []string{r.Blobs[0]})
	if err != nil || string(got[r.Blobs[0]]) != "{\"v\":1}\n" {
		t.Errorf("readBlobs online = %v, %v; want the old version fetched", keysOf(got), err)
	}
}

// keysOf lists m's keys, for a failure message.
func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
