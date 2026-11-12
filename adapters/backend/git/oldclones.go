package git

// oldclones.go — moving the store clones an earlier 0.1.18 build made into
// the plain layout (plain files round, brief 1.6). The old layout was:
//
//	<home>/clones/<host>/<owner>/<repo>.git    a bare clone (git data)
//	<home>/clones/<host>/<owner>/<repo>/work   a linked worktree of it
//	<home>/clones/<host>/<owner>/<repo>/lock   the clone lock
//	<home>/clones/<host>/<owner>/<repo>/state  the lock's journal
//	<home>/clones/<host>/<owner>/<repo>@<branch>…  a pinned branch's clone
//	<home>/clones/<host>/<owner>/<repo>.git    alone, no <repo>/ beside it:
//	                                           lore 0.1.17's bare clone (it
//	                                           wrote commits into the bare
//	                                           clone, never had a worktree)
//	<home>/clones-v017/<host>/<path>.git       the org link code's bare
//	                                           clones (not moved: the org
//	                                           link code now uses the store
//	                                           clone; reported only)
//
// MoveOldClone makes the new clone at <home>/stores/<owner>/<repo> (the
// host never in the path, an ssh alias included) from the old one without
// the network: every branch, tracking branch and backup ref is fetched
// from the old bare clone, so a commit that was never pushed is still a
// commit the remote lacks, and lore's next push sends it. Uncommitted
// changes in the old worktree are copied over as uncommitted changes. A
// worktree in the middle of a merge or rebase is refused with the reason
// and left as it is.
//
// The new clone is built in <dir>.moving and renamed into place, so a run
// cut short leaves either no clone (the next run starts over) or a whole
// one. Each move is written to <home>/state/layout-move.json with the old
// tip and a digest of the carried changes: a later run with the same tip
// and the same changes does nothing; `lore doctor` reads the same record
// to say when the old clone may be removed. lore never deletes the old
// clone.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/osrename"
	"github.com/thesatellite-ai/lore/model/errs"
)

// Old layout names.
const (
	// OldClonesDir is the folder of an earlier build's store clones.
	OldClonesDir = "clones"
	// OldOrgClonesDir is the folder of an earlier build's org link clones.
	OldOrgClonesDir = "clones-v017"
	oldBareSuffix   = ".git"
	oldWorkDir      = "work"
	oldLockName     = "lock"
	// MoveRecordFile is the record of moved clones, under <home>/state.
	MoveRecordFile = "layout-move.json"
	// MoveLockFile serialises two lore processes moving at once (lore where names it).
	MoveLockFile = "layout-move.lock"
	// movingSuffix ends the folder a new clone is built in.
	movingSuffix = ".moving"
	// movedRefNS holds an old clone's branch fetched into a clone that
	// already existed, while it is merged in.
	movedRefNS = "refs/lore/moved/"
	// moveLockWait bounds the wait for an old clone's lock (an older lore
	// still running on it).
	moveLockWait = 10 * time.Second
)

// OldClone is one store clone of the old layout.
type OldClone struct {
	// Rel is the clone's stem relative to <home>/clones, slash separated
	// (host/owner/repo, host/owner/repo@branch).
	Rel string
	// Base is the stem folder (worktree, lock, journal inside).
	Base string
	// Bare is the bare clone beside it (Base + ".git").
	Bare string
	// Work is the linked worktree (Base/work).
	Work string
	// Lock is the old clone lock (Base/lock).
	Lock string
	// Pinned is the branch of a pinned-branch clone ("" otherwise).
	Pinned string
	// Whole is false when one half is missing (nothing to move: its rows
	// are wherever the other half is, or gone). A 0.1.17 bare clone (V017)
	// is whole on its own.
	Whole bool
	// V017 marks lore 0.1.17's clone: the bare clone alone (no stem folder,
	// no worktree, no lock). Its commits are on the bare clone's branches;
	// it has no uncommitted changes to carry.
	V017 bool
}

// GitDir is the folder git commands about the old clone run in (and a
// person's check names): the worktree, or a 0.1.17 bare clone itself.
func (o OldClone) GitDir() string {
	if o.V017 {
		return o.Bare
	}
	return o.Work
}

// RemoveCommand is the exact command that removes the old clone (both
// halves); doctor prints it, and lore fix removes the same two folders
// once MovedSafely proves it safe.
func (o OldClone) RemoveCommand() string {
	if o.V017 {
		return "rm -rf " + shellPath(o.Bare)
	}
	return "rm -rf " + shellPath(o.Base) + " " + shellPath(o.Bare)
}

// Paths are the folders the old clone occupies (what RemoveCommand
// removes): a 0.1.17 clone is its bare clone only.
func (o OldClone) Paths() []string {
	if o.V017 {
		return []string{o.Bare}
	}
	return []string{o.Base, o.Bare}
}

// shellPath quotes a path for a shell command line when it needs it.
func shellPath(p string) string {
	if !strings.ContainsAny(p, " \t'\"$`\\!*?[]{}()&;|<>~#") {
		return p
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

// oldClone is the OldClone whose stem is base.
func oldClone(root, base string) OldClone {
	rel, _ := filepath.Rel(root, base)
	rel = filepath.ToSlash(rel)
	o := OldClone{Rel: rel, Base: base, Bare: base + oldBareSuffix, Work: filepath.Join(base, oldWorkDir), Lock: filepath.Join(base, oldLockName)}
	if i := strings.LastIndex(rel, branchCloneSep); i >= 0 && !strings.Contains(rel[i:], "/") {
		o.Pinned = rel[i+len(branchCloneSep):]
	}
	bare := isFile(filepath.Join(o.Bare, headFileName))
	o.Whole = bare && isFile(filepath.Join(o.Work, gitDirName))
	if _, err := os.Lstat(base); bare && errors.Is(err, fs.ErrNotExist) {
		// lore 0.1.17 kept only the bare clone (no stem folder at all): its
		// commits are all there, so it is whole and can be moved
		o.V017, o.Whole = true, true
	}
	return o
}

// ScanOldClones lists every clone stem under <home>/clones, in walk order.
// It only reads; a missing folder is no clones.
func ScanOldClones(home string) ([]OldClone, error) {
	root := filepath.Join(home, OldClonesDir)
	if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	seen := map[string]bool{}
	var out []OldClone
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || p == root {
			return nil
		}
		var base string
		switch {
		case strings.HasSuffix(p, oldBareSuffix) && isFile(filepath.Join(p, headFileName)):
			base = strings.TrimSuffix(p, oldBareSuffix)
		case isDir(filepath.Join(p, oldWorkDir)) || isFile(filepath.Join(p, oldLockName)):
			base = p
		default:
			return nil // a host or owner folder: look inside
		}
		if !seen[base] {
			seen[base] = true
			out = append(out, oldClone(root, base))
		}
		return filepath.SkipDir
	})
	return out, err
}

// ScanOldOrgClones lists the bare clones under <home>/clones-v017 (the
// org link code's second clones of an earlier build): reported, never
// moved or used.
func ScanOldOrgClones(home string) ([]string, error) {
	root := filepath.Join(home, OldOrgClonesDir)
	if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != root && strings.HasSuffix(p, oldBareSuffix) && isFile(filepath.Join(p, headFileName)) {
			out = append(out, p)
			return filepath.SkipDir
		}
		return nil
	})
	return out, err
}

// MoveRecord is <home>/state/layout-move.json: every old clone moved, by
// its stem folder. Machine-local.
type MoveRecord struct {
	Moves map[string]MoveEntry `json:"moves"`
}

// MoveEntry is one moved clone.
type MoveEntry struct {
	// To is the new clone folder ("" for an old clone with no commit,
	// which had nothing to carry).
	To string `json:"to"`
	// Origin is the old clone's origin url as it was spelled.
	Origin string `json:"origin"`
	// Branch is the branch the old worktree had checked out.
	Branch string `json:"branch,omitempty"`
	// Tip is the old worktree's commit when it was moved.
	Tip string `json:"tip,omitempty"`
	// Changes is a digest of the uncommitted changes carried ("" none).
	Changes string `json:"changes,omitempty"`
	// Carried counts the commits the remote did not have at the move.
	Carried int `json:"carried"`
	// Files counts the uncommitted files carried.
	Files int `json:"files"`
	// At is when (RFC 3339 UTC).
	At string `json:"at"`
}

// MoveRecordPath is <home>/state/layout-move.json.
func MoveRecordPath(home string) string {
	return filepath.Join(home, homeStateDir, MoveRecordFile)
}

// ReadMoveRecord reads the record; missing or unreadable is empty.
func ReadMoveRecord(home string) MoveRecord {
	r := MoveRecord{Moves: map[string]MoveEntry{}}
	b, err := os.ReadFile(filepath.Clean(MoveRecordPath(home)))
	if err != nil || json.Unmarshal(b, &r) != nil || r.Moves == nil {
		return MoveRecord{Moves: map[string]MoveEntry{}}
	}
	return r
}

// writeMoveEntry adds one entry to the record, atomically. The caller
// holds the move lock.
func writeMoveEntry(home, base string, e MoveEntry) error {
	r := ReadMoveRecord(home)
	r.Moves[base] = e
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return errs.Wrap(errs.Internal, "git: encode the move record", err)
	}
	p := MoveRecordPath(home)
	if err = os.MkdirAll(filepath.Dir(p), cloneDirMode); err != nil {
		return errs.FileIO("create", filepath.Dir(p), err)
	}
	tmp := p + ".tmp"
	if err = os.WriteFile(tmp, append(b, '\n'), 0o600); err == nil {
		err = osrename.Rename(tmp, p)
	}
	if err != nil {
		_ = os.Remove(tmp) // best effort cleanup of a failed write
		return errs.FileIO("write", p, err)
	}
	return nil
}

// MoveResult is what MoveOldClone did with one old clone.
type MoveResult struct {
	Old OldClone
	// To is the new clone folder ("" when there was nothing to carry).
	To string
	// Origin is the clone's origin url (the person's spelling).
	Origin string
	// Branch is the branch carried.
	Branch string
	// Carried counts the commits the remote does not have yet: they wait
	// in the new clone for lore's next push.
	Carried int
	// Files lists the uncommitted files carried over (root-relative).
	Files []string
	// Already is true when an earlier run moved this clone as it is now:
	// nothing was done.
	Already bool
	// Into is true when the new clone existed already and the old
	// clone's commits were merged into it (fast-forward).
	Into bool
}

// MoveLock takes the lock that serialises moving old clones on this
// machine (<home>/state/layout-move.lock).
func MoveLock(ctx context.Context, home string) (*rowfs.Lock, error) {
	dir := filepath.Join(home, homeStateDir)
	if err := os.MkdirAll(dir, cloneDirMode); err != nil {
		return nil, errs.FileIO("create", dir, err)
	}
	root, err := rowfs.Open(dir, rowfs.WithLockPath(filepath.Join(dir, MoveLockFile)), rowfs.WithAllowNetworkFS(true))
	if err != nil {
		return nil, err
	}
	return root.Lock(ctx)
}

// MoveNeeded reports whether old clones may need moving: a cheap check
// (no git process) the first-run step makes before taking any lock. It is
// true when some whole old clone has no move entry, or its worktree's
// commit is not the one moved.
func MoveNeeded(home string) bool {
	found, err := ScanOldClones(home)
	if err != nil || len(found) == 0 {
		return false
	}
	rec := ReadMoveRecord(home)
	for _, o := range found {
		if !o.Whole {
			continue
		}
		e, ok := rec.Moves[o.Base]
		if !ok || worktreeTip(o) != e.Tip {
			return true
		}
	}
	return false
}

// worktreeTip reads the old worktree's commit from the files (no git
// process): HEAD of its git dir, then the branch ref loose or packed in
// the bare clone. "" when it cannot be read or the branch has no commit.
func worktreeTip(o OldClone) string {
	gitdir := o.Bare // a 0.1.17 bare clone: its HEAD names the branch
	if !o.V017 {
		gitdir = worktreeGitDir(o.Work)
	}
	if gitdir == "" {
		return ""
	}
	head, err := os.ReadFile(filepath.Join(gitdir, headFileName))
	if err != nil {
		return ""
	}
	h := strings.TrimSpace(string(head))
	ref, ok := strings.CutPrefix(h, "ref: ")
	if !ok {
		return h // detached
	}
	if b, err := os.ReadFile(filepath.Join(o.Bare, filepath.FromSlash(ref))); err == nil {
		return strings.TrimSpace(string(b))
	}
	packed, err := os.ReadFile(filepath.Join(o.Bare, "packed-refs"))
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(packed), "\n") {
		if sha, name, ok := strings.Cut(strings.TrimSpace(line), " "); ok && name == ref {
			return sha
		}
	}
	return ""
}

// worktreeGitDir is the git dir a linked worktree's .git file names.
func worktreeGitDir(work string) string {
	b, err := os.ReadFile(filepath.Join(work, gitDirName))
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(b), "\n")
	dir, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
	if !ok {
		return ""
	}
	dir = strings.TrimSpace(dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(work, dir)
	}
	return dir
}

// MoveOldClone moves one whole old clone into the plain layout (see the
// file comment). The caller holds MoveLock. It takes the old clone's own
// lock, so an older lore still writing there finishes first.
func MoveOldClone(ctx context.Context, home string, o OldClone, now time.Time) (MoveResult, error) {
	res := MoveResult{Old: o}
	if !o.Whole {
		return res, errs.New(errs.NotFound, "git: the old store clone "+o.Base+" is missing its bare clone or its worktree, so there is nothing lore can move").
			WithHint("check it holds nothing you need, then remove it: " + o.RemoveCommand())
	}
	bin, err := lookGit("git")
	if err != nil {
		return res, err
	}
	g := runner{bin: bin, owned: true, noHooks: filepath.Join(o.Base, noHooksName)}
	if o.V017 {
		// lore 0.1.17 locked nothing lore can take here (its lock was in
		// the project); the move only reads the bare clone
		return moveV017(ctx, home, g, o, now)
	}
	lockRoot, err := rowfs.Open(o.Base, rowfs.WithLockPath(o.Lock), rowfs.WithLockTimeout(moveLockWait), rowfs.WithAllowNetworkFS(true))
	if err != nil {
		return res, err
	}
	lock, err := lockRoot.Lock(ctx)
	if err != nil {
		return res, errs.Wrap(errs.LockHeld, "git: the old store clone "+o.Base+" is in use by another lore process", err).
			WithHint("let that command finish (an older lore build may still run there), then run the command again")
	}
	defer func() { _ = lock.Release() }() // released when the move ends; nothing to report after it
	out, err := g.run(ctx, call{dir: o.Bare, args: []string{"config", "--get", "remote." + RemoteName + ".url"}})
	if err != nil {
		return res, errs.Wrap(errs.NotFound, "git: the old store clone "+o.Bare+" has no origin remote, so lore cannot tell which store it is", err).
			WithHint("set it (git -C " + o.Bare + " remote add origin <store url>), then run the command again")
	}
	res.Origin = trimmed(out)
	if err = refuseBusyWorktree(ctx, g, o.Work); err != nil {
		return res, err
	}
	if out, err = g.run(ctx, call{dir: o.Work, args: []string{"symbolic-ref", "--short", "HEAD"}}); err != nil {
		return res, errs.Wrap(errs.InvalidInput, "git: the old store worktree "+o.Work+" is not on a branch", err).
			WithHint("check its branch out again (git -C " + o.Work + " switch <branch>), then run the command again")
	}
	res.Branch = trimmed(out)
	tip := worktreeTip(o)
	changes, err := worktreeChanges(ctx, g, o.Work)
	if err != nil {
		return res, err
	}
	digest := changesDigest(o.Work, changes)
	for _, c := range changes {
		res.Files = append(res.Files, c.path)
	}
	return finishMove(ctx, home, g, o, res, tip, digest, changes, now)
}

// moveV017 is MoveOldClone for lore 0.1.17's bare clone: the branch its
// HEAD names is carried (every branch is fetched), with no worktree and
// no uncommitted changes. A commit 0.1.17 made and never pushed is on
// that branch, so it lands in the new clone as a commit the remote lacks.
func moveV017(ctx context.Context, home string, g runner, o OldClone, now time.Time) (MoveResult, error) {
	res := MoveResult{Old: o}
	out, err := g.run(ctx, call{dir: o.Bare, args: []string{"config", "--get", "remote." + RemoteName + ".url"}})
	if err != nil {
		return res, errs.Wrap(errs.NotFound, "git: the lore 0.1.17 store clone "+o.Bare+" has no origin remote, so lore cannot tell which store it is", err).
			WithHint("set it (git -C " + o.Bare + " remote add origin <store url>), then run the command again")
	}
	res.Origin = trimmed(out)
	if out, err = g.run(ctx, call{dir: o.Bare, args: []string{"symbolic-ref", "--short", "HEAD"}}); err != nil {
		return res, errs.Wrap(errs.InvalidInput, "git: the lore 0.1.17 store clone "+o.Bare+" has no branch at HEAD", err).
			WithHint("point it at the store's branch (git -C " + o.Bare + " symbolic-ref HEAD refs/heads/<branch>), then run the command again")
	}
	res.Branch = trimmed(out)
	return finishMove(ctx, home, g, o, res, worktreeTip(o), "", nil, now)
}

// finishMove is the part of a move both kinds of old clone share: a move
// recorded already with the same tip and changes does nothing; otherwise
// the new clone is made (or the old branch merged into the one there) and
// the move recorded.
func finishMove(ctx context.Context, home string, g runner, o OldClone, res MoveResult, tip, digest string, changes []change, now time.Time) (MoveResult, error) {
	if e, ok := ReadMoveRecord(home).Moves[o.Base]; ok && e.Tip == tip && e.Changes == digest && (e.To == "" || isDir(filepath.Join(e.To, gitDirName))) {
		res.To, res.Carried, res.Already = e.To, e.Carried, true
		return res, nil
	}
	if tip == "" && len(changes) == 0 {
		// a store that never got a commit here: the new clone is made by
		// the next open, as for any new store
		return res, writeMoveEntry(home, o.Base, MoveEntry{Origin: res.Origin, Branch: res.Branch, At: now.UTC().Format(time.RFC3339)})
	}
	dest, err := newCloneDir(home, o, res.Origin)
	if err != nil {
		return res, err
	}
	res.To = dest
	if isFile(filepath.Join(dest, gitDirName, headFileName)) {
		res.Into = true
		err = moveInto(ctx, g, o, res.Branch, tip, dest, changes)
	} else {
		err = moveFresh(ctx, g, o, res.Origin, res.Branch, dest, changes)
	}
	if err != nil {
		return res, err
	}
	res.Carried = countAhead(ctx, g, dest, res.Branch)
	return res, writeMoveEntry(home, o.Base, MoveEntry{To: dest, Origin: res.Origin, Branch: res.Branch, Tip: tip, Changes: digest,
		Carried: res.Carried, Files: len(changes), At: now.UTC().Format(time.RFC3339)})
}

// newCloneDir is where the old clone o lands: the clone folder lore uses
// for its origin (CloneDirFor: <home>/stores/<owner>/<repo>[@branch], the
// host left out, an ssh alias included), or the old stem without its host
// folder when the origin is not a url lore parses (a local path).
func newCloneDir(home string, o OldClone, origin string) (string, error) {
	if r, err := RemoteOfRaw(origin); err == nil {
		dir, _, err := CloneDirFor(home, r, o.Pinned)
		return dir, err
	}
	_, rest, ok := strings.Cut(o.Rel, "/")
	if !ok || rest == "" {
		return "", errs.New(errs.BadPath, "git: the old store clone "+o.Base+" is not at <host>/<owner>/<repo> under "+filepath.Join(home, OldClonesDir)).
			WithHint("attach the store again (lore store add); the old clone can then be removed: " + o.RemoveCommand())
	}
	return filepath.Join(home, StoresDir, filepath.FromSlash(rest)), nil
}

// refuseBusyWorktree refuses a worktree in the middle of a merge, rebase,
// cherry-pick or revert: its state cannot be carried as plain changes.
func refuseBusyWorktree(ctx context.Context, g runner, work string) error {
	out, err := g.run(ctx, call{dir: work, args: []string{"rev-parse", "--absolute-git-dir"}})
	if err != nil {
		return errs.Wrap(errs.Internal, "git: read the old store worktree "+work, err).
			WithHint("check it with git -C " + work + " status, then run the command again")
	}
	gitdir := trimmed(out)
	for _, f := range []struct{ name, what, abort string }{
		{"MERGE_HEAD", "a merge", "merge --abort"},
		{"rebase-merge", "a rebase", "rebase --abort"},
		{"rebase-apply", "a rebase", "rebase --abort"},
		{"CHERRY_PICK_HEAD", "a cherry-pick", "cherry-pick --abort"},
		{"REVERT_HEAD", "a revert", "revert --abort"},
	} {
		if _, serr := os.Lstat(filepath.Join(gitdir, f.name)); serr == nil {
			return errs.New(errs.InvalidInput, "git: the old store worktree "+work+" is in the middle of "+f.what+", so lore cannot move it; nothing was moved").
				WithHint("finish it, or undo it with git -C " + work + " " + f.abort + ", then run the command again")
		}
	}
	return nil
}

// change is one uncommitted path of the old worktree.
type change struct {
	path string // root-relative, slash separated
	gone bool   // deleted in the worktree
}

// worktreeChanges lists the uncommitted paths of the old worktree (every
// untracked file too), lore's own bookkeeping left out. A conflicted path
// is refused.
func worktreeChanges(ctx context.Context, g runner, work string) ([]change, error) {
	out, err := g.run(ctx, call{dir: work, args: []string{"status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames"}})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: read the changes of the old store worktree "+work, err)
	}
	var list []change
	for _, entry := range splitZ(out) {
		if len(entry) < 4 {
			continue
		}
		xy, p := entry[:2], entry[3:]
		if strings.Contains(xy, "U") || xy == "AA" || xy == "DD" {
			return nil, errs.New(errs.InvalidInput, "git: "+p+" in the old store worktree "+work+" has an unresolved conflict, so lore cannot move it; nothing was moved").
				WithHint("resolve it (edit the file, then git -C " + work + " add " + p + ") or drop it (git -C " + work + " checkout -- " + p + "), then run the command again")
		}
		if loreBookkeeping(p) {
			continue
		}
		_, serr := os.Lstat(filepath.Join(work, filepath.FromSlash(p)))
		list = append(list, change{path: p, gone: errors.Is(serr, fs.ErrNotExist)})
	}
	slices.SortFunc(list, func(a, b change) int { return strings.Compare(a.path, b.path) })
	return list, nil
}

// loreBookkeeping reports a path that is lore's own state in an old
// worktree (a .lore folder's lock or journal; .lore/data is rows).
func loreBookkeeping(p string) bool {
	segs := strings.Split(p, "/")
	if segs[0] == rowfs.StoreStateDir {
		return true
	}
	for i, s := range segs {
		if s != rowfs.LoreDir {
			continue
		}
		rest := segs[i+1:]
		return !(len(rest) > 0 && rest[0] == rowfs.DataDir) && !isOrgFileAt(rest)
	}
	return false
}

// isOrgFileAt reports the rest of a path after a .lore segment naming the
// org config (the old org/.lore/lore-org.yaml).
func isOrgFileAt(rest []string) bool { return len(rest) == 1 && rest[0] == orgConfigName }

// orgConfigName is the org config file of an org folder.
const orgConfigName = "lore-org.yaml"

// changesDigest is a digest of the carried changes: each path and its
// bytes (or that it is gone). "" when there are none.
func changesDigest(work string, list []change) string {
	if len(list) == 0 {
		return ""
	}
	h := sha256.New()
	for _, c := range list {
		h.Write([]byte(c.path + "\x00"))
		if c.gone {
			h.Write([]byte("-\x00"))
			continue
		}
		b, err := os.ReadFile(filepath.Join(work, filepath.FromSlash(c.path)))
		if err != nil {
			h.Write([]byte("?\x00"))
			continue
		}
		h.Write([]byte(strconv.Itoa(len(b)) + "\x00"))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ChangesDigestOf recomputes the digest of the old worktree's uncommitted
// changes now (lore doctor compares it with the record); ok is false when
// git cannot read the worktree or a path is conflicted.
func ChangesDigestOf(ctx context.Context, o OldClone) (string, bool) {
	if o.V017 {
		return "", true // a bare clone has no uncommitted changes
	}
	bin, err := lookGit("git")
	if err != nil {
		return "", false
	}
	g := runner{bin: bin, owned: true, noHooks: filepath.Join(o.Base, noHooksName)}
	list, err := worktreeChanges(ctx, g, o.Work)
	if err != nil {
		return "", false
	}
	return changesDigest(o.Work, list), true
}

// WorktreeTip is the old worktree's commit, read from the files.
func WorktreeTip(o OldClone) string { return worktreeTip(o) }

// moveFresh builds the new clone in dest+".moving" from the old bare
// clone and renames it into place.
func moveFresh(ctx context.Context, g runner, o OldClone, origin, branch, dest string, changes []change) error {
	tmp := dest + movingSuffix
	if err := os.RemoveAll(tmp); err != nil { // a run cut short: start over
		return errs.FileIO("remove", tmp, err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), cloneDirMode); err != nil {
		return errs.FileIO("create", filepath.Dir(dest), err)
	}
	steps := [][]string{
		{"init", "--quiet", "--", tmp},
		{"symbolic-ref", "HEAD", headsPrefix + pendingBranch},
		{"remote", "add", "--", RemoteName, origin},
		{"fetch", "--quiet", "--no-tags", "--", o.Bare, "+" + headsPrefix + "*:" + headsPrefix + "*", "+" + trackingRefNS + "*:" + trackingRefNS + "*", "+" + backupRefNS + "*:" + backupRefNS + "*"},
	}
	for i, args := range steps {
		dir := tmp
		if i == 0 {
			dir = filepath.Dir(dest)
		}
		if _, err := g.run(ctx, call{dir: dir, args: args}); err != nil {
			return errs.Wrap(errs.Internal, "git: copy the old store clone "+o.Bare+" into "+tmp, err).
				WithHint("check the old clone with git -C " + o.GitDir() + " status, then run the command again")
		}
	}
	has, err := g.run(ctx, call{dir: tmp, args: []string{"rev-parse", "--verify", "--quiet", headsPrefix + branch}})
	args := []string{"symbolic-ref", "HEAD", headsPrefix + branch}
	if err == nil && trimmed(has) != "" {
		args = []string{"checkout", "--quiet", branch}
	}
	if _, err := g.run(ctx, call{dir: tmp, args: args}); err != nil {
		return errs.Wrap(errs.Internal, "git: check out the store branch "+branch+" in "+tmp, err)
	}
	for _, kv := range [][2]string{{"remote", RemoteName}, {"merge", headsPrefix + branch}} {
		if _, err := g.run(ctx, call{dir: tmp, args: []string{"config", "branch." + branch + "." + kv[0], kv[1]}}); err != nil {
			return errs.Wrap(errs.Internal, "git: set the store branch's upstream in "+tmp, err)
		}
	}
	if err := os.MkdirAll(StateDirOf(tmp), cloneDirMode); err != nil {
		return errs.FileIO("create", StateDirOf(tmp), err)
	}
	info := filepath.Join(tmp, gitDirName, "info")
	if err := os.MkdirAll(info, cloneDirMode); err != nil {
		return errs.FileIO("create", info, err)
	}
	if err := os.WriteFile(filepath.Join(info, "attributes"), []byte(rowAttributesNoDriver+"\n"), 0o600); err != nil {
		return errs.FileIO("write", filepath.Join(info, "attributes"), err)
	}
	if err := copyChanges(o.Work, tmp, changes); err != nil {
		return err
	}
	if err := osrename.Rename(tmp, dest); err != nil {
		return errs.FileIO("rename", tmp, err)
	}
	return nil
}

// moveInto carries the old clone into a new clone that exists already (a
// newer lore cloned the store before the move ran): the old branch is
// fetched, and the new clone fast-forwards to it when it is behind and
// clean. A clone that went its own way is refused with the exact steps.
func moveInto(ctx context.Context, g runner, o OldClone, branch, tip, dest string, changes []change) error {
	moved := movedRefNS + branch
	if tip != "" {
		if _, err := g.run(ctx, call{dir: dest, args: []string{"fetch", "--quiet", "--no-tags", "--", o.Bare, "+" + headsPrefix + branch + ":" + moved}}); err != nil {
			return errs.Wrap(errs.Internal, "git: copy the old store clone "+o.Bare+" into "+dest, err)
		}
	}
	todo, err := changesToCarry(ctx, g, o.Work, dest, changes) // refused before anything changes
	if err != nil {
		return err
	}
	if tip != "" && !reachable(ctx, g, dest, tip) {
		cur, _ := g.run(ctx, call{dir: dest, args: []string{"symbolic-ref", "--short", "HEAD"}})
		head := ""
		if h, herr := g.run(ctx, call{dir: dest, args: []string{"rev-parse", "--verify", "--quiet", "HEAD"}}); herr == nil {
			head = trimmed(h)
		}
		behind := head == "" || ancestor(ctx, g, dest, head, tip)
		switch {
		case trimmed(cur) != branch && trimmed(cur) != pendingBranch:
			return errs.New(errs.InvalidInput, "git: the store clone "+dest+" has branch "+trimmed(cur)+" checked out, the old clone "+o.GitDir()+" has "+branch+"; nothing was moved").
				WithHint("switch it (git -C " + dest + " switch " + branch + "), then run the command again")
		case !behind:
			return errs.New(errs.Stale, "git: the old store clone "+o.GitDir()+" and the new clone "+dest+" both have commits the other lacks; nothing was moved").
				WithHint("push the old clone's commits (git -C " + o.GitDir() + " push origin " + branch + "), then run lore sync; or remove the new clone (it is lore's) and run the command again")
		}
		args := []string{"merge", "--quiet", "--ff-only", moved}
		if head == "" {
			args = []string{"checkout", "--quiet", "-B", branch, moved}
		}
		if _, err := g.run(ctx, call{dir: dest, args: args}); err != nil {
			return errs.Wrap(errs.InvalidInput, "git: bring the old clone's commits into "+dest+" (its uncommitted changes are in the way); nothing was moved", err).
				WithHint("run a lore command in " + dest + " so lore commits them (or git -C " + dest + " stash), then run the command again")
		}
	}
	if tip != "" {
		_, _ = g.run(ctx, call{dir: dest, args: []string{"update-ref", "-d", moved}}) // a kept ref only costs a name
	}
	return copyChanges(o.Work, dest, todo)
}

// changesToCarry is the part of changes the clone at dest does not hold
// yet: a path dest changed the same way already (a move cut short after
// the copy) is skipped; a path dest changed another way is refused, since
// lore never picks between two people's edits of one file.
func changesToCarry(ctx context.Context, g runner, from, dest string, changes []change) ([]change, error) {
	if len(changes) == 0 {
		return nil, nil
	}
	have, err := worktreeChanges(ctx, g, dest)
	if err != nil {
		return nil, err
	}
	dirty := map[string]bool{}
	for _, c := range have {
		dirty[c.path] = true
	}
	var out []change
	for _, c := range changes {
		if !dirty[c.path] {
			out = append(out, c)
			continue
		}
		if sameFile(filepath.Join(from, filepath.FromSlash(c.path)), filepath.Join(dest, filepath.FromSlash(c.path))) {
			continue
		}
		return nil, errs.New(errs.InvalidInput, "git: "+c.path+" has uncommitted changes in both the old clone "+from+" and the store clone "+dest+"; lore will not pick one; nothing was moved").
			WithHint("keep one: copy the file you want into " + dest + " (or undo it there with git -C " + dest + " checkout -- " + c.path + "), then run the command again")
	}
	return out, nil
}

// sameFile reports two paths with the same bytes, or both missing.
func sameFile(a, b string) bool {
	ab, aerr := os.ReadFile(a)
	bb, berr := os.ReadFile(b)
	if aerr != nil || berr != nil {
		return errors.Is(aerr, fs.ErrNotExist) && errors.Is(berr, fs.ErrNotExist)
	}
	return string(ab) == string(bb)
}

// reachable reports whether commit is reachable from any branch or
// tracking branch of the clone at dir.
func reachable(ctx context.Context, g runner, dir, commit string) bool {
	out, err := g.run(ctx, call{dir: dir, args: []string{"for-each-ref", "--contains", commit, "--format=%(refname)", headsPrefix, trackingRefNS}})
	return err == nil && trimmed(out) != ""
}

// ancestor reports whether a is an ancestor of b in the clone at dir.
func ancestor(ctx context.Context, g runner, dir, a, b string) bool {
	_, err := g.run(ctx, call{dir: dir, args: []string{"merge-base", "--is-ancestor", a, b}})
	return err == nil
}

// countAhead counts the commits of branch the remote's tracking branch
// lacks (all of them when there is none).
func countAhead(ctx context.Context, g runner, dir, branch string) int {
	spec := headsPrefix + branch
	if _, err := g.run(ctx, call{dir: dir, args: []string{"rev-parse", "--verify", "--quiet", trackingRefNS + branch}}); err == nil {
		spec = trackingRefNS + branch + ".." + spec
	}
	out, err := g.run(ctx, call{dir: dir, args: []string{"rev-list", "--count", spec}})
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(trimmed(out))
	return n
}

// copyChanges copies the old worktree's uncommitted paths into the new
// clone: a changed or new file's bytes and mode (a link as a link), a
// deleted file deleted.
func copyChanges(from, to string, list []change) error {
	for _, c := range list {
		dst := filepath.Join(to, filepath.FromSlash(c.path))
		if c.gone {
			if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return errs.FileIO("remove", dst, err)
			}
			continue
		}
		src := filepath.Join(from, filepath.FromSlash(c.path))
		info, err := os.Lstat(src)
		if err != nil {
			return errs.FileIO("read", src, err)
		}
		if err = os.MkdirAll(filepath.Dir(dst), cloneDirMode); err != nil {
			return errs.FileIO("create", filepath.Dir(dst), err)
		}
		_ = os.Remove(dst) // replaced below; a missing file is fine
		if info.Mode()&fs.ModeSymlink != 0 {
			target, lerr := os.Readlink(src)
			if lerr == nil {
				lerr = os.Symlink(target, dst)
			}
			if lerr != nil {
				return errs.FileIO("copy", src, lerr)
			}
			continue
		}
		b, err := os.ReadFile(src)
		if err != nil {
			return errs.FileIO("read", src, err)
		}
		if err = os.WriteFile(dst, b, info.Mode().Perm()); err != nil {
			return errs.FileIO("write", dst, err)
		}
	}
	return nil
}

// MovedSafely reports whether everything the old clone o holds is in the
// clone it was moved to, so the old folders may be removed: the record
// has an entry, the old worktree's commit and uncommitted changes are the
// ones moved, and that commit is in the new clone (on a branch or a
// tracking branch: carried or pushed). why says what is missing when ok
// is false.
func MovedSafely(ctx context.Context, home string, o OldClone) (e MoveEntry, ok bool, why string) {
	e, found := ReadMoveRecord(home).Moves[o.Base]
	if !found {
		return e, false, "it was not moved yet"
	}
	if tip := worktreeTip(o); tip != e.Tip {
		return e, false, "it got commits after the move"
	}
	if digest, read := ChangesDigestOf(ctx, o); !read || digest != e.Changes {
		return e, false, "its uncommitted changes differ from the ones moved"
	}
	if e.To == "" || e.Tip == "" {
		return e, true, ""
	}
	bin, err := lookGit("git")
	if err != nil {
		return e, false, "git is not available to check the new clone"
	}
	g := runner{bin: bin, owned: true, noHooks: filepath.Join(o.Base, noHooksName)}
	// carried, not necessarily on a branch: a batch push folds carried
	// commits into one batch commit, so the old tip may hang off no branch
	if !isDir(filepath.Join(e.To, gitDirName)) || !hasCommit(ctx, g, e.To, e.Tip) {
		return e, false, "its commits are not in the new clone " + e.To
	}
	return e, true, ""
}

// hasCommit reports whether the clone at dir holds commit.
func hasCommit(ctx context.Context, g runner, dir, commit string) bool {
	_, err := g.run(ctx, call{dir: dir, args: []string{"cat-file", "-e", commit + "^{commit}"}})
	return err == nil
}
