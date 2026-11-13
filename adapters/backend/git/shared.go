package git

// shared.go — the store clone as other lore code uses it besides this
// backend: the org link code (adapters/orgcfg: `lore org link`, the link
// index, `store show|doctor`) reads and commits in the SAME clone the
// backend opens, under the same lock (1.4: no second clone of a store).
//
//	LockClone      ─▶ the one store lock (.git/lore/lock), reentrant per ctx
//	PrepareClone   ─▶ git init + origin + row attributes, no network
//	FinishCheckout ─▶ a clone whose first open was never finished gets
//	                  its branch checked out from what was fetched
//	AdvanceBranch  ─▶ point a store branch at a new commit; when that
//	                  branch is checked out, the files follow

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
)

// LockClone takes the store lock of the clone at dir (creating its
// .git/lore folder) and returns ctx carrying it: a Lock taken with the
// returned ctx on the same clone, by this backend or another caller of
// LockClone, reuses it instead of waiting (rowfs.Root.Hold). release drops
// it. A clone on a network file system is allowed here: the backend's own
// open already refused it unless the person allowed it.
func LockClone(ctx context.Context, dir string) (context.Context, func(), error) {
	return lockClone(ctx, dir, 0)
}

// TryLockClone is LockClone waiting at most wait (a reader that can use
// the clone as it is when a writer holds it: the org link index's fetch).
func TryLockClone(ctx context.Context, dir string, wait time.Duration) (context.Context, func(), error) {
	return lockClone(ctx, dir, wait)
}

// lockClone is LockClone with a wait (0 = rowfs's default lock timeout).
func lockClone(ctx context.Context, dir string, wait time.Duration) (context.Context, func(), error) {
	p := PathsOf(dir)
	if err := os.MkdirAll(p.StateDir, cloneDirMode); err != nil {
		return ctx, nil, errs.FileIO("create the clone state folder", p.StateDir, err)
	}
	root, err := rowfs.Open(dir, rowfs.WithLockPath(p.Lock), rowfs.WithAllowNetworkFS(true), rowfs.WithLockTimeout(wait))
	if err != nil {
		return ctx, nil, err
	}
	held, lock, err := root.Hold(ctx)
	if err != nil {
		return ctx, nil, err
	}
	return held, func() { _ = lock.Release() }, nil // a failed release only delays the next writer
}

// sharedBackend is a Backend value for the helpers below: the runner and
// folder of the clone at dir, nothing opened.
func sharedBackend(dir string, own bool) (*Backend, error) {
	bin, err := lookGit(DefaultBinary)
	if err != nil {
		return nil, err
	}
	return &Backend{git: runner{bin: bin, owned: true, noHooks: PathsOf(dir).StateDir + string(os.PathSeparator) + noHooksName}, mode: modeClone, work: dir, cfg: config{own: own, keepSpelling: true}}, nil
}

// PrepareClone makes dir a store clone of remote when it is not one yet
// (git init, origin, the row attributes; HEAD waits on the pending branch
// until the store's branch is known), and otherwise points origin at
// remote. It never
// touches the network. An origin naming the same store in another
// spelling is kept (the backend's open may have written its own). The
// caller holds the clone lock (LockClone).
func PrepareClone(ctx context.Context, dir, remote string, own bool) error {
	p := PathsOf(dir)
	if err := checkCloneFolder(p, own); err != nil {
		return err
	}
	if NoClone(ctx) && !exists(p.GitDir+string(os.PathSeparator)+headFileName) {
		return errs.New(errs.StoreUnreachable, "git: the store "+remote+" has no clone on this machine yet, and this run makes none (dry run)").
			WithHint("a run that is not a dry run clones it to " + dir)
	}
	if err := os.MkdirAll(p.StateDir, cloneDirMode); err != nil {
		return errs.FileIO("create the clone state folder", p.StateDir, err)
	}
	b, err := sharedBackend(dir, own)
	if err != nil {
		return err
	}
	if !exists(p.GitDir + string(os.PathSeparator) + headFileName) {
		if err := b.initClone(ctx, remote); err != nil {
			return err
		}
	} else if err := b.ensureRemote(ctx, remote); err != nil {
		return err
	}
	return b.ensureAttributes()
}

// FinishCheckout checks branch out in a clone whose HEAD still waits on
// the pending branch (made by PrepareClone, or a first open cut short),
// from what an earlier fetch brought: the local branch, else the tracking
// one. With neither (an empty store, or never fetched) it leaves the clone
// as it is: the backend's next open fetches and decides. A clone already
// on a branch is left alone. The caller holds the clone lock.
func FinishCheckout(ctx context.Context, dir, branch string) error {
	if !validBranch(branch) {
		return errs.New(errs.InvalidInput, "git: the branch name is not valid")
	}
	b, err := sharedBackend(dir, false)
	if err != nil {
		return err
	}
	out, err := b.git.run(ctx, call{dir: dir, args: []string{"symbolic-ref", "--short", "HEAD"}})
	if err != nil || trimmed(out) != pendingBranch {
		return nil // on a branch already, or detached by a person: theirs
	}
	b.branch = branch
	local, err := b.resolve(ctx, dir, headsPrefix+branch)
	if err != nil {
		return err
	}
	tracking, err := b.resolve(ctx, dir, trackingRefNS+branch)
	if err != nil || (local == "" && tracking == "") {
		return err
	}
	return b.switchTo(ctx, local, tracking)
}

// AdvanceBranch points the store branch at commit oid, from old (the
// commit the branch named, "" when it had none). When that branch is the
// one checked out in the clone, the files and the index follow it: `git
// reset --keep` keeps a person's uncommitted edits and refuses when one of
// them is in the way (the error names the next step); a branch with no
// commit yet gets the commit's files with `git read-tree -m -u`, which
// never overwrites an untracked file. A branch not checked out only moves
// its ref (compare-and-swap on old). The caller holds the clone lock.
func AdvanceBranch(ctx context.Context, dir, branch, oid, old string) error {
	b, err := sharedBackend(dir, false)
	if err != nil {
		return err
	}
	if err := b.refusePersonGitOp(ctx, dir); err != nil {
		return err // B2-9: reset --keep would end the person's merge
	}
	ref := headsPrefix + branch
	head, herr := b.git.run(ctx, call{dir: dir, args: []string{"symbolic-ref", "--quiet", "HEAD"}})
	if herr != nil || trimmed(head) != ref {
		_, err = b.git.run(ctx, call{dir: dir, args: []string{"update-ref", ref, oid, old}})
		if err != nil {
			return errs.Wrap(errs.Internal, "git: move the store branch "+branch, err)
		}
		return nil
	}
	if old == "" {
		if _, err = b.git.run(ctx, call{dir: dir, args: []string{"read-tree", "-m", "-u", oid}}); err != nil {
			return checkedOutRefusal(dir, branch, err)
		}
		if _, err = b.git.run(ctx, call{dir: dir, args: []string{"update-ref", ref, oid, ""}}); err != nil {
			return errs.Wrap(errs.Internal, "git: move the store branch "+branch, err)
		}
		return nil
	}
	if _, err = b.git.run(ctx, call{dir: dir, args: []string{"reset", "--quiet", "--keep", oid}}); err != nil {
		return checkedOutRefusal(dir, branch, err)
	}
	return nil
}

// checkedOutRefusal is AdvanceBranch's error when a person's change in the
// clone is in the way of the new commit's files.
func checkedOutRefusal(dir, branch string, err error) error {
	msg := stderrOf(err)
	if msg == "" {
		msg = err.Error()
	}
	return errs.Wrap(errs.Refused, "git: the store clone "+dir+" has a change of its own to a file the new "+branch+" commit also changes ("+firstLine(msg)+")", errors.New(msg)).
		WithHint("commit or move that change away in " + dir + " (`git -C " + dir + " status` lists it), then run the command again")
}

// firstLine is s up to its first newline, trimmed.
func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}
