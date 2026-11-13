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
//
// "The clone had the branch" cannot rest on the tracking ref alone: every
// fetch prunes, and another fetch of the same clone (a read's refresh, the
// org link index) may have pruned it before the sync looks (B2-28: an org
// store emptied and refilled with every old commit). So each fetch that
// sees the branch, and each push that lands, records its tip in
// <clone>/.git/lore/remote-tip (noteRemoteTip); the check reads it when
// the tracking ref is gone.

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

// remoteTipName is the last remote tip of the clone's branch a fetch or
// push saw, under the clone's StateDir.
const remoteTipName = "remote-tip"

// remoteTipPath is that file in the clone at dir.
func remoteTipPath(dir string) string {
	return filepath.Join(PathsOf(dir).StateDir, remoteTipName)
}

// noteRemoteTip records the tip of the store branch on the remote after a
// fetch or push saw it ("" records nothing: an absent branch must not
// erase what the clone knew). Best effort: a lost note only weakens the
// reset check back to the tracking ref.
func (b *Backend) noteRemoteTip(ctx context.Context, tip string) {
	if b.branch == "" || b.work == "" {
		return
	}
	if tip == "" {
		t, err := b.resolve(ctx, b.work, trackingRefNS+b.branch)
		if err != nil || t == "" {
			return
		}
		tip = t
	}
	p := remoteTipPath(b.work)
	if old, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(old)) == tip {
		return
	}
	_ = os.WriteFile(p, []byte(tip+"\n"), 0o600) // best effort (above)
}

// knownRemoteTip is the remote tip the clone last saw ("" when none).
func knownRemoteTip(dir string) string {
	b, err := os.ReadFile(remoteTipPath(dir))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

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

// remoteResetError is the refusal of a sync into a reset remote. remote
// and h are the remote tip and the clone's head: when the remote now holds
// another lore store (its marker names a different store id), someone
// started it fresh, so the only way out for this machine is to drop its
// old copy and clone the new store (B2-34); --restore would be refused by
// the same check, so it is not offered.
func (b *Backend) remoteResetError(ctx context.Context, why, remote, h string) error {
	shown := b.git.remote
	if o, err := OriginOf(shown); err == nil {
		shown = o
	}
	if newID, oldID := b.storeIDAtCommit(ctx, remote), b.storeIDAtCommit(ctx, h); newID != "" && oldID != "" && newID != oldID {
		return errs.Wrap(errs.Refused, "git: the store at "+shown+" was started fresh by someone (it is now store "+newID+"); this machine's clone "+b.work+" still holds the old store "+oldID+", which lore will not push there", ErrRemoteReset).
			WithHint("drop this machine's old copy with lore store forget --url " + shown + " --yes, then run lore sync to clone the new store (or ask whoever reset it)")
	}
	return errs.Wrap(errs.Refused, "git: the store at "+shown+" was reset ("+why+"), but this machine's clone "+b.work+" still holds the old store; lore will not push it there on its own", ErrRemoteReset).
		WithHint("put this copy back: lore store push --restore " + shown + "; or start fresh: lore store forget --url " + shown + ", then lore store init --name <name> " + shown)
}

// checkRemoteReset decides, after a fetch, whether the remote was reset:
// before is the tracking tip before the fetch, remote the one after it, h
// the clone's head. A reset found is marked on disk and returned as the
// refusal; a clone already marked stays refused.
func (b *Backend) checkRemoteReset(ctx context.Context, before, remote, h string) error {
	if RemoteResetAt(b.work) {
		// B2-30: someone put the store back (store push --restore on
		// another machine): the remote shares history with this clone
		// again, so the mark goes and the sync runs as usual.
		if back, err := b.sharesHistory(ctx, h, remote); err != nil {
			return err
		} else if !back {
			return b.remoteResetError(ctx, "found when lore last reached it", remote, h)
		}
		if err := RestoreRemote(b.work); err != nil {
			return err
		}
		b.cameBack.Store(true)
		b.noteRemoteTip(ctx, remote)
		return nil
	}
	if h == "" {
		return nil // nothing of ours to push
	}
	if before == "" {
		before = knownRemoteTip(b.work) // the tracking ref may have been pruned by another fetch
	}
	why := ""
	switch {
	case remote == "" && before != "":
		why = "its branch " + b.branch + " is gone"
	case remote == "" && pushedBefore(b.work) && !firstPushAllowed(ctx) && b.remoteHasNoHeads(ctx):
		// L-2: a clone from a build that kept no remote tip, its tracking
		// ref pruned: lore's push stamp still says it pushed this store
		// before, so an empty remote now is a reset, not a new store.
		why = "its branch " + b.branch + " is gone"
	case remote != "":
		if back, err := b.sharesHistory(ctx, h, remote); err != nil || back {
			return err
		}
		why = "its history shares no commit with this clone's"
	default:
		return nil // a remote that never had the branch: a new store's first push
	}
	if err := os.WriteFile(remoteResetPath(b.work), []byte(strings.TrimSpace(before)+"\n"), 0o600); err != nil {
		return errs.FileIO("write", remoteResetPath(b.work), err)
	}
	return b.remoteResetError(ctx, why, remote, h)
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
	for _, p := range []string{remoteResetPath(dir), remoteTipPath(dir)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return errs.FileIO("remove", p, err)
		}
	}
	return nil
}

// sharesHistory reports whether the remote tip shares a commit with the
// clone's head h (one is the other's ancestor, or they forked from a
// common commit). An absent remote branch shares nothing; a clone with no
// commit shares with any remote.
func (b *Backend) sharesHistory(ctx context.Context, h, remote string) (bool, error) {
	if remote == "" {
		return false, nil
	}
	if h == "" {
		return true, nil
	}
	_, err := b.git.run(ctx, call{dir: b.work, args: []string{"merge-base", h, remote}})
	if err == nil {
		return true, nil
	}
	if code, ran := exitCode(err); !ran || code != 1 {
		return false, errs.Wrap(errs.Internal, "git: compare the clone with the remote", err)
	}
	return false, nil
}

// TakeCameBack reports, once, that a sync found a store lore had marked
// reset back on its remote and cleared the mark (B2-30); wire says so.
func (b *Backend) TakeCameBack() bool { return b.cameBack.Swap(false) }

// storeIDAtCommit is the store id in the marker at commit ("" when commit
// is "" or holds no readable marker with an id).
func (b *Backend) storeIDAtCommit(ctx context.Context, commit string) string {
	if commit == "" {
		return ""
	}
	m, err := b.markerAt(ctx, commit)
	if err != nil {
		return ""
	}
	return m.ID.String()
}

// pushStampName is the stamp wire writes in the clone's StateDir when a
// push of the store landed (wire/storepushmode.go pushStampFile); its
// presence says this clone pushed the store before, also for a clone
// from a build that kept no remote tip.
const pushStampName = "push_stamp"

// pushedBefore reports whether the clone at dir pushed its store before.
func pushedBefore(dir string) bool {
	_, err := os.Stat(filepath.Join(PathsOf(dir).StateDir, pushStampName))
	return err == nil
}

// firstPushKey marks a context whose sync may push into an empty remote
// although the clone pushed before: store init (the store's first push)
// and store push --restore (the person put the old store back).
type firstPushKey struct{}

// WithFirstPush returns ctx allowing a push into an empty remote.
func WithFirstPush(ctx context.Context) context.Context {
	return context.WithValue(ctx, firstPushKey{}, true)
}

func firstPushAllowed(ctx context.Context) bool {
	v, _ := ctx.Value(firstPushKey{}).(bool)
	return v
}

// GuardPushAt is the reset check for a push lore makes into the clone at
// dir outside a Backend (adapters/orgcfg: org links, lore-org.yaml, the
// org source stamp): after the caller's fetch, h (the commit about to be
// pushed) must not go into a remote lore found reset, or that a sync
// would find reset (checkRemoteReset); a reset found is marked, so every
// later sync and write refuses too. It also records the remote tip the
// fetch saw (noteRemoteTip).
func GuardPushAt(ctx context.Context, dir, branch, h string) error {
	bin, err := lookGit(DefaultBinary)
	if err != nil {
		return err
	}
	b := &Backend{git: runner{bin: bin, owned: true}, work: dir, branch: branch}
	b.git.remote = OriginAt(dir)
	if RemoteResetAt(dir) {
		// no "came back" here: this push path may not have fetched, so a
		// stale tracking ref must not clear the mark (only a sync does)
		return b.remoteResetError(ctx, "found when lore last reached it", "", h)
	}
	remote, err := b.resolve(ctx, dir, trackingRefNS+branch)
	if err != nil {
		return err
	}
	b.noteRemoteTip(ctx, remote)
	return b.checkRemoteReset(ctx, "", remote, h)
}

// NoteRemoteTipAt records the remote tip of branch the clone at dir holds
// now (after a fetch or push lore made outside a Backend). Best effort.
func NoteRemoteTipAt(ctx context.Context, dir, branch, tip string) {
	bin, err := lookGit(DefaultBinary)
	if err != nil {
		return
	}
	b := &Backend{git: runner{bin: bin, owned: true}, work: dir, branch: branch}
	b.noteRemoteTip(ctx, tip)
}

// remoteHasNoHeads reports whether the clone's last fetch found no branch
// at all on the remote (an emptied repo, not one branch removed).
func (b *Backend) remoteHasNoHeads(ctx context.Context) bool {
	if b.headsKnown {
		return b.noHeads // GuardPushGone: the caller asked the remote itself
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"for-each-ref", "--count=1", "--format=%(refname)", trackingRefNS}})
	return err == nil && trimmed(out) == ""
}

// GuardPushGone is GuardPushAt for a caller that asked the remote itself
// (`git ls-remote --heads`, one call) instead of trusting a tracking ref
// that may be stale: the store branch is not on the remote, and noHeads
// says the remote has no branch at all. The reset rule then runs as a
// sync's does (adapters/orgcfg commitStore, before its first push).
func GuardPushGone(ctx context.Context, dir, branch, h string, noHeads bool) error {
	bin, err := lookGit(DefaultBinary)
	if err != nil {
		return err
	}
	b := &Backend{git: runner{bin: bin, owned: true}, work: dir, branch: branch, headsKnown: true, noHeads: noHeads}
	b.git.remote = OriginAt(dir)
	if RemoteResetAt(dir) {
		return b.remoteResetError(ctx, "found when lore last reached it", "", h)
	}
	return b.checkRemoteReset(ctx, "", "", h)
}
