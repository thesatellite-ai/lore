package git

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSyncReportsAPushWithNoRows: a local commit that carries no row file
// (a store link, the org file) is pushed by the sync, and the report says
// it pushed although Sent (rows only) is 0; the next sync pushes nothing
// and says so (devco J-4: the summary read "up to date" under the push).
func TestSyncReportsAPushWithNoRows(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	b := orgClone(t, remote)
	rel := "org/lore-org.yaml"
	full := filepath.Join(b.Dir(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("stores: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, b.Dir(), "add", rel)
	gitT(t, b.Dir(), "commit", "--quiet", "-m", "link")
	rep := syncOK(t, b)
	if !rep.Pushed || rep.Sent != 0 {
		t.Fatalf("first sync: pushed=%v sent=%d, want pushed with 0 rows", rep.Pushed, rep.Sent)
	}
	if got := remoteFile(t, remote, rel); got != "stores: []" {
		t.Fatalf("remote %s = %q", rel, got)
	}
	if rep = syncOK(t, b); rep.Pushed {
		t.Fatalf("second sync pushed again: %+v", rep)
	}
}
