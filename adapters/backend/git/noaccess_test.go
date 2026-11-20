package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// TestFirstOpenNoAccessRecord (devco D-726): the org link reader already
// recorded that the forge refused this login (.git/lore/no-access), and
// the backend's own first open of that never-cloned store fails with a
// text that does not say so (a local path that is no repository, the way
// a store that is gone answers). The refusal is the access message, never
// "go online once", and AccessRefused says so for wire's org wording.
func TestFirstOpenNoAccessRecord(t *testing.T) {
	t.Parallel()
	const remote = "git@github.com:octo-work/lore-vault.git"
	for name, recorded := range map[string]bool{"recorded": true, "not recorded": false} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(t.TempDir(), "c")
			// the clone as the org link reader leaves it: prepared, never fetched
			if err := PrepareClone(t.Context(), dir, remote, false); err != nil {
				t.Fatal(err)
			}
			if recorded {
				if err := os.MkdirAll(StateDirOf(dir), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(NoAccessPath(dir), []byte("missing 2026-01-02T03:04:05Z\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			route := WithRoute(func(context.Context, string) ([]string, []string) {
				return nil, []string{"GIT_SSH_COMMAND=" + filepath.ToSlash(refusingSSHBin), sshStderrEnv + "=fatal: '/srv/gone.git' does not appear to be a git repository"}
			})
			_, err := OpenClone(t.Context(), remote, dir, rowfs.FolderRoot, store.OpenOptions{}, route)
			if !errs.Has(err, errs.StoreUnreachable) {
				t.Fatalf("err = %v, want E_STORE_UNREACHABLE", err)
			}
			env := errs.EnvelopeOf(err).Error
			if !recorded {
				if AccessRefused(err) || strings.Contains(env.Message, "refused") {
					t.Errorf("no record: %q reads as refused", env.Message)
				}
				return
			}
			if env.Message != msgAccessRefused || env.Hint != HintAccessRefused {
				t.Errorf("message = %q hint = %q, want the access refusal", env.Message, env.Hint)
			}
			if strings.Contains(env.Message+env.Hint, "go online") {
				t.Errorf("a refused store is told to go online: %q / %q", env.Message, env.Hint)
			}
			if !AccessRefused(err) {
				t.Error("AccessRefused = false for a recorded refusal")
			}
		})
	}
}

// TestFetchRemovesNoAccessRecord (devco D-741): a no-access record left
// by one refused fetch (another run's credentials) is removed by the
// backend's next fetch that works, not only by the org link reader's:
// sync, push and open reach the store every day, and the record kept
// every read by id skipping it. A clone with a copy is never skipped for
// a record; one never fetched is.
func TestFetchRemovesNoAccessRecord(t *testing.T) {
	ctx := context.Background()
	b := clone(t, newRemote(t, true))
	write := func() {
		t.Helper()
		if err := os.MkdirAll(StateDirOf(b.work), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(NoAccessPath(b.work), []byte("missing 2026-01-02T03:04:05Z\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	syncOK(t, b)
	if NoAccessRecorded(b.work) {
		t.Fatal("a sync that reached the store kept the no-access record")
	}
	write()
	if NoAccessSkips(b.work) {
		t.Error("a clone with a copy is skipped for its no-access record")
	}
	if err := b.fetch(ctx, b.work); err != nil {
		t.Fatal(err)
	}
	if NoAccessRecorded(b.work) {
		t.Error("a fetch that worked kept the no-access record")
	}
	// a clone never fetched: the record skips it
	never := filepath.Join(t.TempDir(), "never")
	if err := PrepareClone(ctx, never, "git@github.com:octo-work/lore-vault.git", false); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(StateDirOf(never), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(NoAccessPath(never), []byte("denied 2026-01-02T03:04:05Z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !NoAccessSkips(never) {
		t.Error("a never-fetched clone with a record is not skipped")
	}
}
