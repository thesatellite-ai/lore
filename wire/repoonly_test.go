package wire

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	opsdirective "github.com/thesatellite-ai/lore/ops/directive"
	opsfix "github.com/thesatellite-ai/lore/ops/fix"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	opsorgcmd "github.com/thesatellite-ai/lore/ops/orgcmd"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
)

// assertRepoOnlyRefusal checks err is the one repo-only refusal: what
// lives in each code repo, why here is not one (because), cd into the
// repo, --store does not apply, and never a --to or --setup way out.
func assertRepoOnlyRefusal(t *testing.T, name string, err error, because string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: want the repo-only refusal, got an answer", name)
		return
	}
	var le *errs.Error
	if !errors.As(err, &le) || le.Code != errs.Unsupported {
		t.Errorf("%s: want E_UNSUPPORTED, got %v", name, err)
		return
	}
	if !strings.Contains(le.Message, " in each code repo: ") || !strings.Contains(le.Message, because) {
		t.Errorf("%s: message does not say what lives in each code repo and %q: %q", name, because, le.Message)
	}
	if !strings.Contains(le.Hint, "cd into the code repo") || !strings.Contains(le.Hint, "--store does not apply") {
		t.Errorf("%s: hint must say cd into the repo and that --store does not apply: %q", name, le.Hint)
	}
	for _, not := range []string{"--to <org>", "--setup", "git init"} {
		if strings.Contains(le.Hint, not) {
			t.Errorf("%s: hint names %s: %q", name, not, le.Hint)
		}
	}
}

// TestEveryRepoOnlyReadRefusesOutsideARepo (devco D-509): every read of
// repoOnlyOps, run from a plain folder (no repo, no store clone, no
// setup), refuses with the one repo-only refusal. Before, migrate status,
// session list and why-context each refused E_NOT_A_STORE in their own
// words (git's "cd into a git checkout; a write can also name its
// store…"), none saying --store does not apply.
func TestEveryRepoOnlyReadRefusesOutsideARepo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := openWith(t, dir, Options{})
	if len(repoOnlyOps) < 6 {
		t.Fatalf("repoOnlyOps lost entries: %d", len(repoOnlyOps))
	}
	for name, r := range repoOnlyOps {
		if _, ok := a.Registry().Lookup(name); !ok {
			t.Errorf("repoOnlyOps names %s, which is no registered operation", name)
			continue
		}
		if r.outsideWorks {
			continue // works outside a repo by design: TestRepoOnlyWritesInAStoreClone
		}
		_, err := run(t, a, name, `{}`, false)
		assertRepoOnlyRefusal(t, name, err, dir+" is not inside a code repo")
	}
}

// fakeStoreClone makes a store clone's shape under a temp folder: a git
// work tree whose top holds the store marker, with one folder inside.
func fakeStoreClone(t *testing.T) (top, folder string) {
	t.Helper()
	top = filepath.Join(t.TempDir(), "acme-store")
	gitRunT(t, t.TempDir(), "init", "-q", top)
	if err := os.WriteFile(filepath.Join(top, rowfs.MarkerFile), []byte("name: acme-store\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	folder = filepath.Join(top, "web")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	return top, folder
}

// TestRepoOnlyReadsInAStoreClone (devco D-509): in a store clone, at its
// top and in a folder, migrate status refuses with the repo-only refusal
// naming the clone (a store clone never held lore 0.1.17 data), and the
// local trash (`lore sync trash list`, `lore trash --local`) reads the
// clone's own copies (none) instead of refusing "this command needs a
// code repo".
func TestRepoOnlyReadsInAStoreClone(t *testing.T) {
	t.Parallel()
	top, folder := fakeStoreClone(t)
	for _, dir := range []string{top, folder} {
		a := openWith(t, dir, Options{})
		_, err := run(t, a, opsmigrate.OpStatus, `{}`, false)
		assertRepoOnlyRefusal(t, opsmigrate.OpStatus+" in "+dir, err, "is a lore store clone, not a code repo")
		copies, err := a.LocalTrash(t.Context())
		if err != nil || len(copies) != 0 {
			t.Errorf("local trash in the store clone at %s: want the empty trash, got %v, %v", dir, copies, err)
		}
	}
}

// TestRepoOnlyWritesInAStoreClone (devco D-523): the writes that change a
// code repo's own files refuse in a store clone (at its top and in a
// folder) with the repo-only refusal, and outside every repo too unless
// they work there by design. Before, each refused in its own words ("it
// edits a repo's .lore/stores.yaml, and … is a lore store", "lore isn't set
// up in … (no .lore folder)", "not inside a git repo here" in a store clone
// that is one), none saying cd into the code repo and --store does not
// apply; a command needing a code repo with no entry of its own (memory
// move in a store clone) says the same through errInStore.
func TestRepoOnlyWritesInAStoreClone(t *testing.T) {
	t.Parallel()
	writes := []string{
		opsfix.OpRun, opsmigrate.OpRun, opsmigrate.OpRollback, opsmigrate.OpUnlock,
		opsdirective.OpInstall, opssync.OpInstallAction,
	}
	top, folder := fakeStoreClone(t)
	for _, dir := range []string{top, folder} {
		a := openWith(t, dir, Options{})
		for _, name := range writes {
			if _, ok := repoOnlyOps[name]; !ok {
				t.Errorf("%s is not in repoOnlyOps", name)
				continue
			}
			_, err := run(t, a, name, `{}`, true)
			assertRepoOnlyRefusal(t, name+" in "+dir, err, "is a lore store clone, not a code repo")
		}
		// the stores.yaml writes refuse through their own gate
		for name, in := range map[string]string{
			opsstores.OpAdd: `{"name":"extra","url":"git@example.test:acme/extra.git"}`, opsstores.OpInit: `{"name":"extra","url":"git@example.test:acme/extra.git"}`,
			opsstores.OpRemove: `{"name":"extra"}`, opsstores.OpSetURL: `{"name":"extra","url":"git@example.test:acme/moved.git"}`,
		} {
			_, err := run(t, a, name, in, true)
			assertStoresYAMLRefusal(t, name+" in "+dir, err, "is a lore store clone, not a code repo")
		}
	}
	// outside every repo store add refuses the same way, and store init
	// --name me still makes the personal store there
	plain := openWith(t, t.TempDir(), Options{})
	_, err := run(t, plain, opsstores.OpAdd, `{"name":"extra","url":"git@example.test:acme/extra.git"}`, true)
	assertStoresYAMLRefusal(t, opsstores.OpAdd+" outside a repo", err, "is not inside a code repo")
	if _, ok := repoOnlyOps[opsorgcmd.OpLink]; !ok {
		t.Errorf("%s is not in repoOnlyOps", opsorgcmd.OpLink)
	}
	sc, _ := storeHere(folder)
	var le *errs.Error
	if err = errInStore(sc); !errors.As(err, &le) || !strings.Contains(le.Hint, "cd into the code repo") || !strings.Contains(le.Hint, "--store does not apply") {
		t.Errorf("errInStore: want the repo-only hint (cd into the code repo, --store does not apply), got %v", err)
	}
}

// assertStoresYAMLRefusal checks err is the repo-only refusal of a
// stores.yaml write: the class's words (cd into the code repo, --store
// does not apply) under the E_NOT_A_STORE that refusal always had.
func assertStoresYAMLRefusal(t *testing.T, name string, err error, because string) {
	t.Helper()
	var le *errs.Error
	if !errors.As(err, &le) || le.Code != errs.NotAStore {
		t.Errorf("%s: want E_NOT_A_STORE, got %v", name, err)
		return
	}
	if !strings.Contains(le.Message, "edits a repo's .lore/stores.yaml") || !strings.Contains(le.Message, because) {
		t.Errorf("%s: message does not name stores.yaml and %q: %q", name, because, le.Message)
	}
	if !strings.Contains(le.Hint, "cd into the code repo") || !strings.Contains(le.Hint, "--store does not apply") {
		t.Errorf("%s: hint must say cd into the repo and that --store does not apply: %q", name, le.Hint)
	}
}
