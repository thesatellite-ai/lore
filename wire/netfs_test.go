package wire

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
)

// TestAllowNetworkFS: a code repo inside a Dropbox folder is refused with
// E_NETWORK_FS unless Options.AllowNetworkFS (LORE_ALLOW_NETWORK_FS=1 in
// the CLI) is set, as v0.1.17 honoured the override; with it a core write
// works.
func TestAllowNetworkFS(t *testing.T) {
	for _, allow := range []bool{false, true} {
		repo := filepath.Join(t.TempDir(), "Dropbox", "repo")
		if err := os.MkdirAll(repo, 0o750); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("git", "init", "-q")
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init: %v %s", err, out)
		}
		a := openWith(t, repo, Options{AllowNetworkFS: allow})
		if !allow {
			if _, err := a.Engine(background()); !errs.Has(err, errs.NetworkFS) {
				t.Fatalf("default: want E_NETWORK_FS, got %v", err)
			}
			continue
		}
		withProject(t, a)
		if _, err := run(t, a, opsmemory.OpAdd, `{"body":"note in a synced folder"}`, false); err != nil {
			t.Fatalf("allowed: %v", err)
		}
	}
}
