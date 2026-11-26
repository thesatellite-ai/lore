package orgcfg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/partialclone"
)

// TestOrgFetchInBloblessCloneBringsBlobs: the org link code's own fetch of
// a store clone (link index, org refresh) brings new history with every
// blob in a blobless clone, as the backend's fetches do. Without it the
// commits arrived blobless and the backend's later --no-filter fetch had
// nothing new to ask for, so a new row read offline failed. Break-verified:
// without FetchArgsIn the new version's blob stayed missing (store growth
// SG-3b, devco:D-1001).
func TestOrgFetchInBloblessCloneBringsBlobs(t *testing.T) {
	r := partialclone.Make(t, "rows/task/t1.json", "{\"v\":1}\n", "{\"v\":2}\n")
	full := filepath.Join(r.Origin, "rows", "task", "t1.json")
	if err := os.WriteFile(full, []byte("{\"v\":3}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	partialclone.Git(t, r.Origin, "commit", "-q", "-am", "version 3")
	fresh := partialclone.Git(t, r.Origin, "rev-parse", "HEAD:rows/task/t1.json")
	if err := fetch(context.Background(), nil, r.Clone); err != nil {
		t.Fatal(err)
	}
	missing := partialclone.Git(t, r.Clone, "rev-list", "--objects", "--missing=print", "--all")
	if strings.Contains(missing, "?"+fresh) {
		t.Fatalf("the org fetch brought the new version without its blob %s:\n%s", fresh, missing)
	}
}
