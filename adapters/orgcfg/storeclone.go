package orgcfg

// storeclone.go — the org layer's use of a store's clone (STORE layout
// 1.4, 2026-11-12). There is one clone of a store on this machine, the
// git store backend's (<home>/stores/<owner>/<repo>, or a person's own
// clone recorded by `lore store use`); the org layer reads and commits in
// that same clone, under the same lock (<clone>/.git/lore/lock), with its
// state beside the backend's in <clone>/.git/lore/.
//
//	cloneDir      ─▶ adapters/backend/git CloneDirFor (default branch clone)
//	ensureClone   ─▶ lock ─▶ PrepareClone (git init + origin; no network)
//	fetchLocked   ─▶ lock ─▶ fetch ─▶ stamp + default branch ─▶ check the
//	                 default branch out if the clone never was
//
// Why commits still move refs directly (commitStore, AdvanceBranch) rather
// than writing files and running `git commit`: a link or org file commit
// is built on the REMOTE's tip after a lost push race, which may not be
// the branch the clone has checked out (a pinned or staging branch), and
// must never pick up a person's unrelated edits in the checkout. So the
// commit is made with plumbing (gitref.MakeCommit on the tip, no index or
// files involved) and the branch then advanced with
// gitbackend.AdvanceBranch, which keeps the checkout in step when that
// branch is the one checked out (`git reset --keep`, refusing rather than
// overwriting a person's change). No second clone is needed for that.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/osrename"
	"github.com/thesatellite-ai/lore/model/errs"
)

// cloneDir is the store clone of a normalised store url (host/path) on
// this machine and whether it is a person's own clone. A relative home
// (never set by lore) falls back to the default path under it.
func cloneDir(home, norm string) (string, bool) {
	r := gitbackend.Remote{Norm: norm}
	dir, own, err := gitbackend.CloneDirFor(home, r, "")
	if err != nil {
		_, path, _ := strings.Cut(norm, "/")
		return filepath.Join(home, gitbackend.StoresDir, filepath.FromSlash(path)), false
	}
	return dir, own
}

// ensureClone makes dir a clone of the store at url when it is not one
// yet (no network), under the store lock. The origin is the person's own
// spelling without credentials or lore's query (gitbackend.OriginOf, as
// the backend writes it); a url OriginOf cannot parse (a local path) is
// used as given.
func ensureClone(ctx context.Context, dir, url string, own bool) error {
	origin, err := gitbackend.OriginOf(url)
	if err != nil {
		origin = url
	}
	if cur := gitbackend.OriginAt(dir); cur != "" && (cur == origin || sameStoreURL(cur, origin)) {
		return nil // already this store's clone: nothing to write, no lock to wait for
	}
	if gitbackend.NoClone(ctx) {
		// a dry run makes no clone, not even its lock (B4-14)
		return errs.New(errs.StoreUnreachable, "orgcfg: the store "+url+" has no clone on this machine yet, and this run makes none (dry run)")
	}
	lctx, unlock, err := gitbackend.LockClone(ctx, dir)
	if err != nil {
		return err
	}
	defer unlock()
	return gitbackend.PrepareClone(lctx, dir, origin, own)
}

// fetchLocked fetches the clone through r under the store lock; on
// success it writes the fetch stamp and the store's default branch and,
// for a clone whose files were never checked out, checks out branch (""
// = the store's default branch), so the clone a person opens is never an
// empty folder. An error means unreachable.
func fetchLocked(ctx context.Context, r Router, dir string, now time.Time, branch ...string) error {
	// A writer holding the store (a row batch, a sync) may be fetching it
	// right now: the link index then reads the clone as it is (the stale
	// rule, live.fetch_ttl / link_index.max_age) instead of waiting.
	lctx, unlock, err := gitbackend.TryLockClone(ctx, dir, fetchLockWait)
	if err != nil {
		return err
	}
	defer unlock()
	if err := fetch(lctx, r, dir); err != nil {
		return err
	}
	want := recordFetch(lctx, r, dir, now)
	if len(branch) > 0 && branch[0] != "" {
		want = branch[0]
	}
	if want != "" {
		_ = gitbackend.FinishCheckout(lctx, dir, want) // best effort: the backend's next open does it too
	}
	return nil
}

// fetchLockWait is how long fetchLocked waits for the store lock.
const fetchLockWait = time.Second

// sameStoreURL reports whether two urls name one store (host/path).
func sameStoreURL(a, b string) bool {
	ra, ea := gitbackend.RemoteOfRaw(a)
	rb, eb := gitbackend.RemoteOfRaw(b)
	return ea == nil && eb == nil && ra.Norm == rb.Norm
}

// fetch updates the clone's tracking refs through r; an error means
// unreachable. The caller holds the store lock.
func fetch(ctx context.Context, r Router, dir string) error {
	args := append([]string{"fetch", "--quiet", "--prune", remoteName}, fetchRefspecs...)
	_, err := netGit(ctx, r, dir, args...)
	return err
}

// recordFetch writes the fetch stamp and the store's HEAD branch (best
// effort: an empty store has no HEAD) and returns that branch ("" when
// unknown).
func recordFetch(ctx context.Context, r Router, dir string, at time.Time) string {
	writeState(dir, fetchStampFile, at.UTC().Format(time.RFC3339Nano))
	out, err := netGit(ctx, r, dir, "ls-remote", "--symref", "--", remoteName, "HEAD")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, headRefPrefix); ok {
			if name, _, _ := strings.Cut(rest, "\t"); name != "" {
				writeState(dir, defaultBranchFile, name)
				return name
			}
			return ""
		}
	}
	return ""
}

// writeState writes one state file in the clone's .git/lore atomically (a
// unique temp file per writer). Best effort: a failed stamp only means the
// next command fetches again.
func writeState(dir, name, value string) {
	sdir := gitbackend.StateDirOf(dir)
	if os.MkdirAll(sdir, cloneDirMode) != nil {
		return
	}
	f, err := os.CreateTemp(sdir, name+".*.tmp")
	if err != nil {
		return
	}
	tmp := f.Name()
	_, werr := f.WriteString(value)
	cerr := f.Close()
	if errors.Join(werr, cerr, os.Chmod(tmp, cloneFileMode)) != nil || osrename.Rename(tmp, filepath.Join(sdir, name)) != nil {
		_ = os.Remove(tmp) // best effort cleanup of a failed stamp
	}
}

// lastFetch reads the fetch stamp (zero when never fetched).
func lastFetch(dir string) time.Time {
	b, err := os.ReadFile(filepath.Clean(filepath.Join(gitbackend.StateDirOf(dir), fetchStampFile)))
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
	if err != nil {
		return time.Time{}
	}
	return t
}
