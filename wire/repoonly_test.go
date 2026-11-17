package wire

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
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
// repoOnlyReads, run from a plain folder (no repo, no store clone, no
// setup), refuses with the one repo-only refusal. Before, migrate status,
// session list and why-context each refused E_NOT_A_STORE in their own
// words (git's "cd into a git checkout; a write can also name its
// store…"), none saying --store does not apply.
func TestEveryRepoOnlyReadRefusesOutsideARepo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := openWith(t, dir, Options{})
	if len(repoOnlyReads) < 6 {
		t.Fatalf("repoOnlyReads lost entries: %d", len(repoOnlyReads))
	}
	for name := range repoOnlyReads {
		if _, ok := a.Registry().Lookup(name); !ok {
			t.Errorf("repoOnlyReads names %s, which is no registered operation", name)
			continue
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
