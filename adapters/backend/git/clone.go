package git

import (
	"context"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// RemoteName is the remote every store clone uses.
const RemoteName = "origin"

// Ref namespaces.
const (
	headsPrefix   = "refs/heads/"
	trackingRefNS = "refs/remotes/" + RemoteName + "/"
)

// noHooksName is a folder inside the bare clone that never exists; it is
// the owned clone's core.hooksPath so no hook runs on lore's commits.
const noHooksName = "lore-no-hooks"

// rowAttributes goes into the bare clone's info/attributes, which wins
// over any .gitattributes in the store: git never merges a row file as
// text (a text merge of JSON can silently produce a wrong row); it keeps
// ours and marks the file conflicted, and Sync hands it to the Merger.
const rowAttributes = "*.json -merge\n"

// cloneDirMode is the mode of clone folders: owner only, since a clone of
// an internal store is internal.
const cloneDirMode = 0o700

// branchPattern is a conservative git branch name.
var branchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// lockSuffix ends a name git refuses as a ref component.
const lockSuffix = ".lock"

// validBranch reports whether name is a branch lore accepts: branchPattern
// plus the git check-ref-format rules that pattern lets through (no "..",
// no empty or dot-led component, no trailing "/" or ".", no component
// ending ".lock"). The name also becomes part of a clone folder
// (<repo>@<branch>), so a refused name is refused before any folder or git
// command sees it rather than failing half way in `git worktree add`.
func validBranch(name string) bool {
	if !branchPattern.MatchString(name) || strings.Contains(name, "..") || strings.HasSuffix(name, ".") {
		return false
	}
	for part := range strings.SplitSeq(name, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, lockSuffix) {
			return false
		}
	}
	return true
}

// NewOpener returns the store.Opener for ssh, https and http store urls
// (register it under each of Schemes). It needs store.OpenOptions.Home:
// clones live under <home>/clones. Scp-like urls must go through
// CanonicalURL first.
func NewOpener(opts ...Option) store.Opener {
	return func(ctx context.Context, u *url.URL, o store.OpenOptions) (store.Backend, error) {
		r, err := RemoteOf(u)
		if err != nil {
			return nil, err
		}
		branch, rest, err := branchOf(u)
		if err != nil {
			return nil, err
		}
		folder, err := rowfs.FolderOf(rest)
		if err != nil {
			return nil, err
		}
		base, err := CloneBase(o.Home, r)
		if err != nil {
			return nil, err
		}
		// callOpts is per call: appending to the captured opts would pin
		// every later open of this Opener to the first pinned branch.
		callOpts := opts
		if branch != "" {
			// A pinned branch gets its own clone (bare + worktree + lock):
			// a worktree holds one branch, and the default branch's clone
			// may be open in another process.
			base += branchCloneSep + filepath.FromSlash(branch)
			callOpts = append(slices.Clone(opts), WithBranch(branch))
		}
		return OpenClone(ctx, r.URL, base, folder, o, callOpts...)
	}
}

// OpenClone opens (creating on first use) the store clone of remote whose
// folders stem is base: the bare clone base+".git", the worktree
// base/work, the lock base/lock. remote is any url or path git can fetch
// (tests use a local bare repository); NewOpener passes a credential-free
// url. The first open fetches the store: if it cannot be reached then,
// E_STORE_UNREACHABLE. Later opens never touch the network (Sync does).
func OpenClone(ctx context.Context, remote, base, folder string, o store.OpenOptions, opts ...Option) (*Backend, error) {
	cfg := build(opts)
	bin, err := lookGit(cfg.bin)
	if err != nil {
		return nil, err
	}
	if cfg.branch != "" && !validBranch(cfg.branch) {
		return nil, errs.New(errs.InvalidInput, "git: the branch name is not valid")
	}
	if err = os.MkdirAll(base, cloneDirMode); err != nil {
		return nil, errs.Wrap(errs.Internal, "git: create the clone folder", err)
	}
	lockPath := filepath.Join(base, lockName)
	setupRoot, err := rowfs.Open(base, rowfs.WithLockPath(lockPath), rowfs.WithLockTimeout(cfg.lockTimeout))
	if err != nil {
		return nil, err
	}
	bare := base + bareSuffix
	b := &Backend{cfg: cfg, git: runner{bin: bin, owned: true, noHooks: filepath.Join(bare, noHooksName)}, mode: modeClone, readOnly: o.ReadOnly, work: filepath.Join(base, workDir)}
	lock, err := setupRoot.Lock(ctx)
	if err != nil {
		return nil, err
	}
	err = b.setup(ctx, remote, bare)
	if err = releaseKeep(err, lock); err != nil {
		return nil, err
	}
	if b.root, err = rowfs.Open(b.work, rowfs.WithLockPath(lockPath), rowfs.WithLockTimeout(cfg.lockTimeout), rowfs.WithClock(cfg.now)); err != nil {
		return nil, err
	}
	if b.folder, err = b.root.Folder(folder); err != nil {
		return nil, err
	}
	b.folder.SetMetaSource(b.sharedFile)
	if err := b.checkMarker(ctx); err != nil {
		return nil, err
	}
	return b, nil
}

// setup makes the bare clone and the worktree when missing. The caller
// holds the clone lock, so two processes opening one new store make one
// clone.
func (b *Backend) setup(ctx context.Context, remote, bare string) error {
	if !exists(filepath.Join(bare, "HEAD")) {
		if err := b.initBare(ctx, remote, bare); err != nil {
			return err
		}
	} else if err := b.ensureRemote(ctx, remote, bare); err != nil {
		return err
	}
	if exists(filepath.Join(b.work, ".git")) {
		out, err := b.git.run(ctx, call{dir: b.work, args: []string{"symbolic-ref", "--short", "HEAD"}})
		if err != nil {
			return errs.Wrap(errs.Internal, "git: the clone worktree is not on a branch", err)
		}
		b.branch = trimmed(out)
		return nil
	}
	return b.addWorktree(ctx, bare)
}

// initBare makes an empty bare clone with origin and the row attributes.
func (b *Backend) initBare(ctx context.Context, remote, bare string) error {
	if _, err := b.git.run(ctx, call{dir: filepath.Dir(bare), args: []string{"init", "--quiet", "--bare", "--", bare}}); err != nil {
		return errs.Wrap(errs.Internal, "git: create the store clone", err)
	}
	if _, err := b.git.run(ctx, call{dir: bare, args: []string{"remote", "add", "--", RemoteName, remote}}); err != nil {
		return errs.Wrap(errs.Internal, "git: add the store remote", err)
	}
	info := filepath.Join(bare, "info")
	if err := os.MkdirAll(info, cloneDirMode); err != nil {
		return errs.Wrap(errs.Internal, "git: create info/", err)
	}
	if err := os.WriteFile(filepath.Join(info, "attributes"), []byte(rowAttributes), 0o600); err != nil {
		return errs.Wrap(errs.Internal, "git: write info/attributes", err)
	}
	return nil
}

// ensureRemote points origin at remote when it changed (a moved store, or
// an old config that still held a credential).
func (b *Backend) ensureRemote(ctx context.Context, remote, bare string) error {
	out, err := b.git.run(ctx, call{dir: bare, args: []string{"remote", "get-url", "--", RemoteName}})
	if err == nil && trimmed(out) == remote {
		return nil
	}
	if _, err := b.git.run(ctx, call{dir: bare, args: []string{"remote", "set-url", "--", RemoteName, remote}}); err != nil {
		return errs.Wrap(errs.Internal, "git: update the store remote", err)
	}
	return nil
}

// addWorktree fetches the store and checks out the worktree on the store
// branch: an existing local branch (unpushed commits survive a deleted
// worktree), else the remote branch, else a new orphan branch for an empty
// store.
func (b *Backend) addWorktree(ctx context.Context, bare string) error {
	fetchErr := b.fetch(ctx, bare)
	if fetchErr != nil && !isOffline(fetchErr) {
		return fetchErr
	}
	if b.branch = b.cfg.branch; b.branch == "" {
		b.branch = DefaultBranch
		if fetchErr == nil {
			b.branch = b.remoteHead(ctx, bare)
		}
	}
	if _, err := b.git.run(ctx, call{dir: bare, args: []string{"worktree", "prune"}}); err != nil {
		return errs.Wrap(errs.Internal, "git: prune worktrees", err)
	}
	local, err := b.resolve(ctx, bare, headsPrefix+b.branch)
	if err != nil {
		return err
	}
	tracking, err := b.resolve(ctx, bare, trackingRefNS+b.branch)
	if err != nil {
		return err
	}
	if fetchErr != nil && local == "" && tracking == "" {
		return errs.Wrap(errs.StoreUnreachable, "git: the store has no local copy yet and cannot be reached", fetchErr).
			WithHint("go online once; afterwards the local copy is used")
	}
	var args []string
	switch {
	case local != "":
		args = []string{"worktree", "add", "--quiet", "--", b.work, b.branch}
	case tracking != "":
		args = []string{"worktree", "add", "--quiet", "-b", b.branch, "--", b.work, trackingRefNS + b.branch}
	default:
		args = []string{"worktree", "add", "--quiet", "--orphan", "-b", b.branch, "--", b.work}
	}
	if _, err := b.git.run(ctx, call{dir: bare, args: args}); err != nil {
		return errs.Wrap(errs.Internal, "git: check out the store worktree", err)
	}
	return nil
}

// remoteHead asks the store which branch its HEAD names; DefaultBranch when
// it cannot say (an empty store).
func (b *Backend) remoteHead(ctx context.Context, dir string) string {
	out, err := b.git.run(ctx, call{dir: dir, args: []string{"ls-remote", "--symref", "--", RemoteName, "HEAD"}})
	if err != nil {
		return DefaultBranch
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		rest, ok := strings.CutPrefix(line, "ref: "+headsPrefix)
		if !ok {
			continue
		}
		if name, _, _ := strings.Cut(rest, "\t"); validBranch(name) {
			return name
		}
	}
	return DefaultBranch
}

// errOffline is the cause of every fetch or push that got no answer.
var errOffline = errors.New("git: the store remote did not answer")

// isOffline reports a remote that could not be reached.
func isOffline(err error) bool { return errors.Is(err, errOffline) }

// msgMissingRemoteRef is git's text for a fetched ref the remote lacks.
const msgMissingRemoteRef = "couldn't find remote ref"

// fetch updates every tracking branch of the clone (dir is the bare clone
// or the worktree). A failed fetch is errOffline: lore treats any fetch it
// could not complete as "remote unreachable", and nothing local changes.
func (b *Backend) fetch(ctx context.Context, dir string) error {
	_, err := b.git.run(ctx, call{dir: dir, args: []string{"fetch", "--quiet", "--no-tags", "--prune", "--no-write-fetch-head", "--", RemoteName, "+" + headsPrefix + "*:" + trackingRefNS + "*"}})
	if err == nil {
		return nil
	}
	if _, ran := exitCode(err); !ran {
		return err // git never ran, or ctx ended
	}
	if strings.Contains(stderrOf(err), msgMissingRemoteRef) {
		return nil
	}
	return errs.Wrap(errs.Offline, "git: fetch the store", errors.Join(errOffline, err))
}

// resolve returns the commit a ref names, "" when it does not exist.
func (b *Backend) resolve(ctx context.Context, dir, ref string) (string, error) {
	out, err := b.git.run(ctx, call{dir: dir, args: []string{"rev-parse", "--verify", "--quiet", ref + "^{commit}"}})
	if err != nil {
		if _, ran := exitCode(err); ran {
			return "", nil
		}
		return "", err
	}
	return trimmed(out), nil
}

// head is the worktree's HEAD commit, "" on an unborn branch.
func (b *Backend) head(ctx context.Context) (string, error) { return b.resolve(ctx, b.work, "HEAD") }

// checkMarker reads lore-store.yaml at HEAD. An empty store has no commit
// and passes (its first writer adds the marker); a store with commits but
// no marker is not a lore store (store.ErrNotAStore).
func (b *Backend) checkMarker(ctx context.Context) error {
	h, err := b.head(ctx)
	if err != nil || h == "" {
		return err
	}
	name := h + ":" + rowfs.MarkerFile
	blobs, err := b.readBlobs(ctx, []string{name})
	if err != nil {
		return err
	}
	raw, ok := blobs[name]
	if !ok {
		return errs.Wrap(errs.NotAStore, "git: the repository has no "+rowfs.MarkerFile, store.ErrNotAStore)
	}
	m, err := rowfs.ParseMarker(raw)
	if err != nil {
		return err
	}
	b.marker = m
	return nil
}

// commitBatch stages the applied files and makes one commit; on failure it
// restores the files and the index, so the batch is none.
func (b *Backend) commitBatch(ctx context.Context, batch Batch, changes []store.Change, applied *rowfs.Applied) error {
	author := batch.Author
	if author.IsZero() {
		author = b.cfg.author
	}
	add := append([]string{"add", "--"}, applied.Paths...)
	_, err := b.git.run(ctx, call{dir: b.work, args: add})
	if err == nil {
		_, err = b.git.run(ctx, call{dir: b.work, args: []string{"commit", "--quiet", "--no-verify", "--allow-empty", "-m", subject(batch.Op, changes)}, env: b.commitEnv(author)})
	}
	if err == nil {
		return nil
	}
	undoErr := applied.Undo()
	restage := append([]string{"add", "-A", "--"}, applied.Paths...)
	_, stageErr := b.git.run(ctx, call{dir: b.work, args: restage})
	return errs.Wrap(errs.Internal, "git: commit the batch", errors.Join(err, undoErr, stageErr))
}

// commitEnv signs a commit as author at the configured clock.
func (b *Backend) commitEnv(s Signature) []string {
	date := "@" + strconv.FormatInt(b.cfg.now().Unix(), 10) + " +0000"
	return []string{
		"GIT_AUTHOR_NAME=" + s.Name, "GIT_AUTHOR_EMAIL=" + s.Email, "GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_NAME=" + s.Name, "GIT_COMMITTER_EMAIL=" + s.Email, "GIT_COMMITTER_DATE=" + date,
	}
}

// commitIDPattern is a full sha-1 or sha-256 commit id: the only cursor
// form this backend issues.
var commitIDPattern = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// cursorCommit returns the cursor's commit when it names one in this
// clone, "" otherwise (an unknown cursor reads everything).
func (b *Backend) cursorCommit(ctx context.Context, cursor store.Cursor) (string, error) {
	c := string(cursor)
	if !commitIDPattern.MatchString(c) {
		return "", nil
	}
	return b.resolve(ctx, b.work, c)
}

// readCommits is clone-mode Read: `git diff --name-status <cursor> HEAD`
// over the folder's data path, or every file at HEAD for an unknown
// cursor; blobs are read at HEAD, so the rows and the returned cursor are
// one snapshot. Deleted files are not deletes (only tombstones are).
func (b *Backend) readCommits(ctx context.Context, cursor store.Cursor) ([]store.Row, store.Cursor, error) {
	h, err := b.head(ctx)
	if err != nil || h == "" {
		return nil, "", err
	}
	from, err := b.cursorCommit(ctx, cursor)
	if err != nil {
		return nil, "", err
	}
	paths, err := b.changedPaths(ctx, from, h, b.folder.DataPath())
	if err != nil {
		return nil, "", err
	}
	prefix := b.folder.DataPath() + "/"
	names := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, _, ok := rowfs.ParseRowPath(strings.TrimPrefix(p, prefix)); ok && strings.HasPrefix(p, prefix) {
			names = append(names, h+":"+p)
		}
	}
	blobs, err := b.readBlobs(ctx, names)
	if err != nil {
		return nil, "", err
	}
	rows := make([]store.Row, 0, len(blobs))
	for _, name := range names {
		doc, ok := blobs[name]
		if !ok || len(doc) > rowfs.MaxFileSize {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(name, h+":"), prefix)
		r, derr := rowfs.DecodeRowFile(rel, doc)
		if derr != nil {
			continue // reported by Problems
		}
		rows = append(rows, r)
	}
	return rows, store.Cursor(h), nil
}

// changedPaths lists the files under pathspec added or modified between
// from and to (every file at to when from is ""), root-relative.
func (b *Backend) changedPaths(ctx context.Context, from, to, pathspec string) ([]string, error) {
	if from == "" {
		out, err := b.git.run(ctx, call{dir: b.work, args: []string{"ls-tree", "-r", "-z", "--name-only", to, "--", pathspec}})
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "git: list row files", err)
		}
		return splitZ(out), nil
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"diff", "--name-status", "-z", "--no-renames", from, to, "--", pathspec}})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: diff row files", err)
	}
	fields := splitZ(out)
	var paths []string
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i] != statusDeleted {
			paths = append(paths, fields[i+1])
		}
	}
	return paths, nil
}

// statusDeleted is `git diff --name-status`'s letter for a removed file.
const statusDeleted = "D"

// missingCommits lists the folder's row files present at the cursor
// commit and absent at HEAD.
func (b *Backend) missingCommits(ctx context.Context, cursor store.Cursor) ([]string, error) {
	from, err := b.cursorCommit(ctx, cursor)
	if err != nil || from == "" {
		return nil, err
	}
	h, err := b.head(ctx)
	if err != nil || h == "" {
		return nil, err
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"diff", "--name-only", "-z", "--no-renames", "--diff-filter=D", from, h, "--", b.folder.DataPath()}})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: diff row files", err)
	}
	prefix := b.folder.DataPath() + "/"
	var missing []string
	for _, p := range splitZ(out) {
		missing = append(missing, strings.TrimPrefix(p, prefix))
	}
	return missing, nil
}

// exists reports whether path exists.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, fs.ErrNotExist)
}

// BranchParam is the url query parameter that pins the store branch a
// clone checks out (`ssh://git@host/acme/notes?branch=staging`); absent
// follows the remote's default branch. Wire builds it from the `branch:`
// key of .lore/stores.yaml, as it builds rowfs.FolderParam.
const BranchParam = "branch"

// branchCloneSep joins the clone stem and a pinned branch:
// <home>/clones/<host>/<owner>/<repo>@<branch>.git and …@<branch>/work.
const branchCloneSep = "@"

// branchOf takes BranchParam out of u: the branch (validated like
// WithBranch) and a copy of u without it, for rowfs.FolderOf.
func branchOf(u *url.URL) (string, *url.URL, error) {
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", nil, errs.New(errs.StoreScheme, "git: the store url query does not parse")
	}
	vals, ok := q[BranchParam]
	if !ok {
		return "", u, nil
	}
	if len(vals) != 1 || !validBranch(vals[0]) {
		return "", nil, errs.New(errs.InvalidInput, "git: the branch name is not valid")
	}
	q.Del(BranchParam)
	rest := *u
	rest.RawQuery = q.Encode()
	return vals[0], &rest, nil
}

// sharedFile is the folder's rowfs.MetaSource in clone mode: before a
// folder's first write creates its _meta.json, fetch (best effort: offline
// reads what was fetched last) and return the remote branch's copy of rel,
// so this clone adopts the file `lore org link` or another clone already
// pushed instead of creating a second one. Called from Write, which holds
// the store lock.
func (b *Backend) sharedFile(ctx context.Context, rel string) ([]byte, bool, error) {
	if ferr := b.fetch(ctx, b.work); ferr != nil && !isOffline(ferr) {
		return nil, false, ferr
	}
	name := trackingRefNS + b.branch + ":" + rel
	got, err := b.readBlobs(ctx, []string{name})
	if err != nil {
		return nil, false, err
	}
	doc, ok := got[name]
	return doc, ok, nil
}
