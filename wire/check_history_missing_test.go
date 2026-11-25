package wire

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/partialclone"
	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/model/errs"
)

// TestHistoryScanMissingBlobRefuses: `lore check leaks --history` in a
// blobless clone offline refuses (E_LEAK_BLOCKED, "is not in this clone",
// run it again online) instead of skipping the file versions the clone
// lacks as nothing to scan; the size pass refuses a "<oid> missing" line
// too (store growth SG-3a, devco:D-997). Break-verified on the message:
// the old refusal said "store unreachable and nothing cached".
func TestHistoryScanMissingBlobRefuses(t *testing.T) {
	key := "AKIA" + strings.Repeat("Q", 16) // built at run time, never a literal key
	r := partialclone.Make(t, "notes/keys.txt", "aws "+key+"\n", "removed\n")
	t.Setenv(gitenv.EnvOffline, gitenv.On)
	blobs, err := historyBlobs(context.Background(), r.Clone, []string{"HEAD"})
	if !errs.Has(err, errs.LeakBlocked) || !strings.Contains(err.Error(), "not in this clone") {
		t.Fatalf("historyBlobs offline in a blobless clone = %d blobs, %v; want E_LEAK_BLOCKED naming the version not in this clone", len(blobs), err)
	}
}
