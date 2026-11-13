package git

// remotereset.go — a store remote that was deleted and made again (B3-29).
// A person who deletes a store's repo and creates an empty one at the
// same address (or another team that reuses the address) means "start
// fresh"; this machine's clone still holds the old store. Pushing it on
// the next sync would silently put every old row back. So the clone
// notices instead:
//
//	syncClone: tracking tip before the fetch, remote tip after it
//	   ├─ the clone had fetched or pushed the branch, the remote now lacks it
//	   ├─ or the remote's branch shares no commit with the clone's
//	   └─▶ remote reset: <clone>/.git/lore/remote-reset is written (the
//	        fetch pruned the tracking ref, so the next sync would not see it
//	        again) and the sync fails with E_REFUSED naming both ways out:
//	          lore store push --restore <store>   put this copy back
//	          lore store forget <url>, then init  start fresh
//
// Only RestoreRemote (`store push --restore`) clears the mark; forgetting
// the clone removes it with the folder.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// remoteResetName is the mark of a reset remote under the clone's
// StateDir (machine-local, never committed).
const remoteResetName = "remote-reset"

// ErrRemoteReset marks the refusal of a sync whose remote was reset; wire
// matches it to name the store in the way out (errors.Is).
var ErrRemoteReset = errors.New("git: the store's remote was reset")

// remoteResetPath is the mark's file in the clone at dir.
func remoteResetPath(dir string) string {
	return filepath.Join(PathsOf(dir).StateDir, remoteResetName)
}

// RemoteResetAt reports whether the clone at dir is marked as holding a
// store whose remote was reset (a previous sync found it).
func RemoteResetAt(dir string) bool {
	_, err := os.Stat(remoteResetPath(dir))
	return err == nil
}

// remoteResetError is the refusal of a sync into a reset remote.
func (b *Backend) remoteResetError(why string) error {
	shown := b.git.remote
	if o, err := OriginOf(shown); err == nil {
		shown = o
	}
	return errs.Wrap(errs.Refused, "git: the store at "+shown+" was reset ("+why+"), but this machine's clone "+b.work+" still holds the old store; lore will not push it there on its own", ErrRemoteReset).
		WithHint("put this copy back: lore store push --restore " + shown + "; or start fresh: lore store forget " + shown + ", then lore store init <name> " + shown)
}

// checkRemoteReset decides, after a fetch, whether the remote was reset:
// before is the tracking tip before the fetch, remote the one after it, h
// the clone's head. A reset found is marked on disk and returned as the
// refusal; a clone already marked stays refused.
func (b *Backend) checkRemoteReset(ctx context.Context, before, remote, h string) error {
	if RemoteResetAt(b.work) {
		return b.remoteResetError("lore found it reset earlier")
	}
	if h == "" {
		return nil // nothing of ours to push
	}
	why := ""
	switch {
	case remote == "" && before != "":
		why = "its branch " + b.branch + " is gone"
	case remote != "":
		_, err := b.git.run(ctx, call{dir: b.work, args: []string{"merge-base", h, remote}})
		if err == nil {
			return nil
		}
		if code, ran := exitCode(err); !ran || code != 1 {
			return errs.Wrap(errs.Internal, "git: compare the clone with the remote", err)
		}
		why = "its history shares no commit with this clone's"
	default:
		return nil // a remote that never had the branch: a new store's first push
	}
	if err := os.WriteFile(remoteResetPath(b.work), []byte(strings.TrimSpace(before)+"\n"), 0o600); err != nil {
		return errs.FileIO("write", remoteResetPath(b.work), err)
	}
	return b.remoteResetError(why)
}

// RemoteReset fetches the store and reports whether its remote was reset
// compared to this clone (the rule of checkRemoteReset), marking it when
// so. Offline is not a reset. store init uses it before it trusts the
// clone's marker over the remote's own content.
func (b *Backend) RemoteReset(ctx context.Context) (bool, error) {
	lock, err := b.root.Lock(ctx)
	if err != nil {
		return false, err
	}
	defer func() { err = releaseKeep(err, lock) }()
	before, err := b.resolve(ctx, b.work, trackingRefNS+b.branch)
	if err != nil {
		return false, err
	}
	if ferr := b.runFetch(ctx, b.work, "fetch", "--quiet", "--no-tags", "--prune", "--no-write-fetch-head", "--", RemoteName, headsRefspec); ferr != nil {
		if isOffline(ferr) {
			return false, nil
		}
		return false, ferr
	}
	remote, err := b.resolve(ctx, b.work, trackingRefNS+b.branch)
	if err != nil {
		return false, err
	}
	h, err := b.head(ctx)
	if err != nil {
		return false, err
	}
	if rerr := b.checkRemoteReset(ctx, before, remote, h); rerr != nil {
		if errors.Is(rerr, ErrRemoteReset) {
			return true, nil
		}
		return false, rerr
	}
	return false, nil
}

// RestoreRemote clears the reset mark of the clone at dir, so the next
// sync pushes this copy of the store into the remote again (`lore store
// push --restore`). No mark is no error.
func RestoreRemote(dir string) error {
	if err := os.Remove(remoteResetPath(dir)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return errs.FileIO("remove", remoteResetPath(dir), err)
	}
	return nil
}
