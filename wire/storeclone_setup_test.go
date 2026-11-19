package wire

// storeclone_setup_test.go — devco D-682: init, render and org link/unlink
// in a store clone refuse before anything is opened, naming the store and
// its folder, and leave the clone as it was (init there committed a
// context.targets policy into the store folder and wrote pointer files,
// a .gitignore and hooks into the clone).

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	opsrender "github.com/thesatellite-ai/lore/ops/render"
	opsrepoinit "github.com/thesatellite-ai/lore/ops/repoinit"
)

// cloneTreeOf lists every path under dir (the .git folder too): what a refused
// command must leave byte-for-byte as it was, at least by name.
func cloneTreeOf(t *testing.T, dir string) string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(dir, func(p string, _ os.DirEntry, err error) error {
		if err == nil {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return strings.Join(out, "\n")
}

func TestRepoSetUpOpsRefuseInAStoreClone(t *testing.T) {
	t.Parallel()
	inputs := map[string]string{
		opsrepoinit.OpRepoInit: `{}`,
		opsrender.OpRun:        `{}`,
		opsorgcmd.OpLink:       `{"org":"acme","project":"web"}`,
		opsorgcmd.OpUnlink:     `{"org":"acme"}`,
	}
	for name := range storeCloneSetUpOps {
		if _, ok := inputs[name]; !ok {
			t.Errorf("%s has no test input", name)
		}
	}
	top, folder := fakeStoreClone(t)
	// a store folder is one with its _meta.json (rowfs.StoreFolderAt)
	meta := filepath.Join(top, filepath.FromSlash(rowfs.LayoutStore.DataPath("web")), rowfs.MetaFileName)
	if err := os.MkdirAll(filepath.Dir(meta), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(meta, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{top, folder} {
		a := openWith(t, dir, Options{})
		for name, in := range inputs {
			before := cloneTreeOf(t, top)
			_, err := run(t, a, name, in, true)
			if err == nil {
				t.Fatalf("%s in %s: no refusal", name, dir)
			}
			env := errs.EnvelopeOf(err).Error
			if env.Code != errs.Unsupported || !strings.Contains(env.Message, "this is the clone of store acme-store") ||
				!strings.Contains(env.Message, "is for code repos: nothing written") {
				t.Errorf("%s in %s: %s %q, want E_UNSUPPORTED naming the store clone", name, dir, env.Code, env.Message)
			}
			if dir == folder && !strings.Contains(env.Message, "(folder web)") {
				t.Errorf("%s in the folder: %q does not name the folder", name, env.Message)
			}
			if after := cloneTreeOf(t, top); after != before {
				t.Errorf("%s in %s changed the clone:\nbefore:\n%s\nafter:\n%s", name, dir, before, after)
			}
		}
	}
	// a read-only render previews the folder (D-432): never refused here
	a := openWith(t, folder, Options{})
	if _, err := run(t, a, opsrender.OpRun, `{"dry_run":true}`, true); err != nil && strings.Contains(err.Error(), "is for code repos") {
		t.Errorf("render --dry-run in a store folder refused: %v", err)
	}
	// a code repo is untouched by the gate
	if err := storeCloneSetUpRefusal(t.TempDir(), t.TempDir(), opsrepoinit.OpRepoInit); err != nil {
		t.Errorf("outside a store clone: %v", err)
	}
}
