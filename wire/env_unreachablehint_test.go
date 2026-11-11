package wire

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/tests/fakeexec"
)

// storeOpenErr is the real E_STORE_UNREACHABLE of a first clone whose ssh
// prints sshStderr and exits 255 (as GitHub's ssh answers, or a dead network).
func storeOpenErr(t *testing.T, sshStderr string) error {
	t.Helper()
	dir := t.TempDir()
	sshFake := filepath.Join(dir, "ssh")
	sshFake = fakeexec.Write(t, sshFake, "#!/bin/sh\necho '"+sshStderr+"' >&2\nexit 255\n", 0o755)
	route := git.WithRoute(func(context.Context, string) ([]string, []string) {
		return nil, []string{"GIT_SSH_COMMAND=" + filepath.ToSlash(sshFake)}
	})
	_, err := git.OpenClone(t.Context(), "git@github.com:octo-work/lore-me.git", filepath.Join(dir, "c"), rowfs.FolderRoot, store.OpenOptions{}, route)
	if !errs.Has(err, errs.StoreUnreachable) {
		t.Fatalf("open: %v, want E_STORE_UNREACHABLE", err)
	}
	return err
}

// TestUnreachableHint: the write guards' hint says "go online" only when no
// unreachable store refused this machine's access; a refused one (wrong key,
// no key) gets the account hint. The guard's own tail is kept either way.
func TestUnreachableHint(t *testing.T) {
	t.Parallel()
	offline := storeOpenErr(t, "ssh: Could not resolve hostname github.com: Name or service not known")
	wrongKey := storeOpenErr(t, "ERROR: Repository not found.")
	noKey := storeOpenErr(t, "git@github.com: Permission denied (publickey).")
	other := errs.New(errs.StoreUnreachable, "attach: store team_notes could not be opened")
	const off, rest = "go online once", ", then write again"
	cases := map[string]struct {
		in     map[store.Source]error
		access bool
	}{
		"offline":                {map[store.Source]error{"team": offline}, false},
		"other unreachable":      {map[store.Source]error{"team": other}, false},
		"wrong key":              {map[store.Source]error{"me": wrongKey}, true},
		"no key":                 {map[store.Source]error{"me": noKey}, true},
		"offline and refused":    {map[store.Source]error{"team": offline, "me": wrongKey}, true},
		"other and refused":      {map[store.Source]error{"team": other, "me": noKey}, true},
		"no unreachable (empty)": {map[store.Source]error{}, false},
	}
	for name, c := range cases {
		got := unreachableHint(c.in, off, rest)
		if !strings.HasSuffix(got, rest) {
			t.Errorf("%s: hint %q lost the guard's tail", name, got)
		}
		if c.access {
			if !strings.HasPrefix(got, git.HintAccessRefused) || strings.Contains(got, "go online") {
				t.Errorf("%s: hint = %q, want the access hint, no \"go online\"", name, got)
			}
		} else if got != off+rest {
			t.Errorf("%s: hint = %q, want %q", name, got, off+rest)
		}
	}
}
