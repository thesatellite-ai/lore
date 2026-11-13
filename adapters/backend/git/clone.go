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
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/gitenv"
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

// noHooksName is a folder inside the clone's .git/lore that never exists;
// it is the owned clone's core.hooksPath for lore's own git calls (passed
// with -c on each call, never written to the clone's config), so no hook
// runs on lore's commits: lore validates before it writes (D-g).
const noHooksName = "no-hooks"

// pendingBranch is the branch HEAD names between `git init` of a new clone
// and the checkout of the store's branch (the store's default branch is
// known only after the first fetch). A clone whose HEAD still names it was
// cut short and is finished on the next open; it is never committed to.
const pendingBranch = PendingBranch

// PendingBranch is pendingBranch for readers outside this package (a
// clone whose HEAD names it has no branch checked out yet).
const PendingBranch = "lore-clone-pending"

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
// clones live under <home>/stores (CloneDirFor). Scp-like urls must go
// through CanonicalURL first.
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
		// A pinned branch gets its own clone (<repo>@<branch>): a checkout
		// holds one branch, and the default branch's clone may be open in
		// another process.
		dir, own, err := CloneDirFor(o.Home, r, branch)
		if err != nil {
			return nil, err
		}
		// callOpts is per call: appending to the captured opts would pin
		// every later open of this Opener to the first pinned branch.
		callOpts := opts
		if branch != "" || own {
			callOpts = slices.Clone(opts)
		}
		if branch != "" {
			callOpts = append(callOpts, WithBranch(branch))
		}
		if own {
			callOpts = append(callOpts, withOwnClone())
		}
		if !own && r.isLocal() {
			// a clone made before B3-21 sits at the whole path: move it once
			if err = moveLegacyLocalClone(ctx, o.Home, r, branch, dir); err != nil {
				return nil, err
			}
		}
		remote := gitAddress(r.URL)
		if spelled, ok := build(opts).origins[r.URL]; ok && spelled != "" {
			remote = spelled
		}
		return OpenClone(ctx, remote, dir, folder, o, callOpts...)
	}
}

// OpenClone opens (creating on first use) the store clone of remote at
// dir: a normal git clone with its files checked out, the store lock in
// dir/.git/lore/lock (PathsOf). remote is any url or path git can fetch
// (tests use a local bare repository); NewOpener passes a credential-free
// url (the WithOrigins spelling, else Remote.URL). The first open fetches
// the store: if it cannot be reached then, E_STORE_UNREACHABLE. Later
// opens never touch the network (Sync does).
func OpenClone(ctx context.Context, remote, dir, folder string, o store.OpenOptions, opts ...Option) (*Backend, error) {
	cfg := build(opts)
	bin, err := lookGit(cfg.bin)
	if err != nil {
		return nil, err
	}
	if cfg.branch != "" && !validBranch(cfg.branch) {
		return nil, errs.New(errs.InvalidInput, "git: the branch name is not valid")
	}
	if !filepath.IsAbs(dir) {
		return nil, errs.New(errs.BadPath, "git: the store clone folder must be an absolute path")
	}
	paths := PathsOf(dir)
	if err = checkCloneFolder(paths, cfg.own); err != nil {
		return nil, err
	}
	if NoClone(ctx) && !isDir(filepath.Join(dir, gitDirName)) {
		return nil, errs.New(errs.StoreUnreachable, "git: the store "+remote+" has no clone on this machine yet, and this run makes none (dry run)").
			WithHint("a run that is not a dry run clones it to " + dir)
	}
	if err = os.MkdirAll(paths.StateDir, cloneDirMode); err != nil {
		return nil, errs.FileIO("create the clone folder", paths.StateDir, err)
	}
	lockOpts := []rowfs.Option{rowfs.WithLockPath(paths.Lock), rowfs.WithLockTimeout(cfg.lockTimeout), rowfs.WithLockTimeout(o.LockTimeout), rowfs.WithAllowNetworkFS(o.AllowNetworkFS)}
	setupRoot, err := rowfs.Open(dir, lockOpts...)
	if err != nil {
		return nil, err
	}
	b := &Backend{cfg: cfg, git: runner{bin: bin, owned: true, noHooks: filepath.Join(paths.StateDir, noHooksName)}, mode: modeClone, readOnly: o.ReadOnly, work: dir}
	b.git.remote = remote // named in --trace lines (run.go)
	if cfg.route != nil { // ONBOARDING 3.4 / Q5: the active setup's ssh route
		b.git.routeArgs, b.git.routeEnv = cfg.route(ctx, remote)
	}
	if cfg.accessNote != nil {
		b.git.accessNote = cfg.accessNote(remote)
	}
	lock, err := setupRoot.Lock(ctx)
	if err != nil {
		return nil, err
	}
	err = b.setup(ctx, remote)
	if err = releaseKeep(err, lock); err != nil {
		return nil, err
	}
	if b.root, err = rowfs.Open(dir, append(lockOpts, rowfs.WithClock(cfg.now))...); err != nil {
		return nil, err
	}
	if b.folder, err = b.root.Folder(folder); err != nil {
		return nil, err
	}
	b.folder.SetMetaSource(b.sharedFile)
	if err := b.checkMarker(ctx); err != nil {
		return nil, err
	}
	if err := b.dropLiveRefs(ctx); err != nil {
		return nil, err
	}
	return b, nil
}

// checkCloneFolder refuses a clone folder lore cannot use as it is: a
// folder that holds files but no git clone (lore never clones over a
// person's files), or a .git that is a file (a linked worktree or a
// separate git dir: lore keeps its lock and state in a .git folder). A
// missing or empty folder is fine: the first open makes the clone.
func checkCloneFolder(p ClonePaths, own bool) error {
	st, err := os.Lstat(p.GitDir)
	switch {
	case err == nil && st.IsDir():
		return nil
	case err == nil:
		return errs.New(errs.BadPath, "git: "+p.GitDir+" is a file, not a git folder (a linked worktree or a separate git dir); lore keeps its lock and state in the clone's .git folder").
			WithHint("make a plain clone there (`git clone <url> " + p.Dir + "`), or point lore back at its own clone: lore store use --default <store>")
	case !errors.Is(err, fs.ErrNotExist):
		return errs.FileIO("read", p.GitDir, err)
	}
	if own {
		return errs.New(errs.NotFound, "git: "+p.Dir+" is no longer a git clone (its .git folder is gone)").
			WithHint("clone the store there again, or go back to lore's own clone: lore store use --default <store>")
	}
	entries, err := os.ReadDir(p.Dir)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && len(entries) == 0) {
		return nil
	}
	if err != nil {
		return errs.FileIO("read", p.Dir, err)
	}
	return errs.New(errs.BadPath, "git: "+p.Dir+" holds files but is not a git clone, so lore will not clone the store into it").
		WithHint("move those files away (or remove the folder), then run the command again")
}

// setup makes the clone when it is missing and checks the store's branch
// out when an earlier first open was cut short. The caller holds the
// clone lock, so two processes opening one new store make one clone.
func (b *Backend) setup(ctx context.Context, remote string) error {
	fresh := !exists(filepath.Join(b.work, gitDirName, headFileName))
	if fresh {
		if err := b.initClone(ctx, remote); err != nil {
			return err
		}
	} else if err := b.ensureRemote(ctx, remote); err != nil {
		return err
	}
	if GitSetUp(ctx) {
		if err := b.ensureHandGit(ctx); err != nil {
			return err
		}
	} else {
		b.handPending = true // a read sets nothing up (owner bug 6); the first write does
	}
	if fresh || GitSetUp(ctx) {
		// the clone lore makes now, or an explicit setup or sync
		if err := b.ensureCloneIdentity(ctx, remote); err != nil {
			return err
		}
	} else {
		b.identityRemote = remote // a read writes no identity (owner bug 6); the first write does
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"symbolic-ref", "--short", "HEAD"}})
	if err != nil {
		return errs.Wrap(errs.Internal, "git: the store clone is not on a branch", err).
			WithHint("check a branch out in " + b.work + " (`git -C " + b.work + " switch <branch>`)")
	}
	if b.branch = trimmed(out); b.branch == pendingBranch {
		return b.checkout(ctx)
	}
	if b.cfg.branch != "" && b.branch != b.cfg.branch {
		return errs.New(errs.InvalidInput, "git: the store clone "+b.work+" has branch "+b.branch+" checked out, but the store is pinned to "+b.cfg.branch).
			WithHint("switch it back: git -C " + b.work + " switch " + b.cfg.branch)
	}
	return b.adoptPinnedBranch(ctx)
}

// adoptPinnedBranch moves a pinned-branch clone that has no commit yet
// (an earlier open made the branch new because the store lacked it, D3)
// onto the remote branch once the store has it, so the clone does not
// stay a second, empty store under that name (G-CORE-5). Only an unborn,
// clean checkout is touched; an offline fetch leaves it as it is.
func (b *Backend) adoptPinnedBranch(ctx context.Context) error {
	if b.cfg.branch == "" {
		return nil
	}
	h, err := b.head(ctx)
	if err != nil || h != "" {
		return err
	}
	st, err := b.git.run(ctx, call{dir: b.work, args: []string{"status", "--porcelain"}})
	if err != nil || trimmed(st) != "" {
		return err
	}
	if ferr := b.fetch(ctx, b.work); ferr != nil {
		if isOffline(ferr) {
			return nil
		}
		return ferr
	}
	tracking, err := b.resolve(ctx, b.work, trackingRefNS+b.branch)
	if err != nil || tracking == "" {
		return err
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"checkout", "--quiet", "-B", b.branch, trackingRefNS + b.branch}}); err != nil {
		return errs.Wrap(errs.Internal, "git: check out the store branch "+b.branch, err)
	}
	return nil
}

// initClone makes an empty, normal (non-bare) git repository at the clone
// folder with origin and the row attributes, its HEAD on pendingBranch
// until checkout knows the store's branch.
func (b *Backend) initClone(ctx context.Context, remote string) error {
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"init", "--quiet", "--", b.work}}); err != nil {
		return errs.Wrap(errs.Internal, "git: create the store clone", err)
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"symbolic-ref", "HEAD", headsPrefix + pendingBranch}}); err != nil {
		return errs.Wrap(errs.Internal, "git: prepare the store clone", err)
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"remote", "add", "--", RemoteName, remote}}); err != nil {
		return errs.Wrap(errs.Internal, "git: add the store remote", err)
	}
	return nil
}

// ensureAttributes makes sure the clone's .git/info/attributes holds
// lore's row line (a person's own clone, `lore store use`, starts without
// it); other lines are kept. A clone whose config names lore's merge
// driver gets `*.json merge=lore` (handgit.go), any other `*.json
// -merge`: git never merges a row file as text (a text merge of JSON can
// silently produce a wrong row).
func (b *Backend) ensureAttributes() error {
	out, err := os.ReadFile(filepath.Join(b.work, gitDirName, "config"))
	if err == nil && strings.Contains(string(out), "[merge \""+MergeDriverName+"\"]") {
		return b.ensureAttributeLine(rowAttributesDriver, rowAttributesNoDriver)
	}
	return b.ensureAttributeLine(rowAttributesNoDriver, rowAttributesDriver)
}

// ensureRemote points origin at remote when it changed (a moved store, or
// an old config that still held a credential), and adds it when a first
// open was cut short before it. A person's own clone (`lore store use`)
// keeps its own spelling of the same store (and one re-pointed at another
// repository is refused); keepSpelling (the org link code, PrepareClone)
// keeps any spelling of the same store too.
func (b *Backend) ensureRemote(ctx context.Context, remote string) error {
	// the url as configured, not `git remote get-url` (which applies the
	// person's url.<base>.insteadOf rules and so never equals remote)
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"config", "--get", "remote." + RemoteName + ".url"}})
	if err == nil && trimmed(out) == remote {
		return nil
	}
	if err != nil {
		if _, aerr := b.git.run(ctx, call{dir: b.work, args: []string{"remote", "add", "--", RemoteName, remote}}); aerr != nil {
			return errs.Wrap(errs.Internal, "git: add the store remote", aerr)
		}
		return nil
	}
	if cur := trimmed(out); sameStore(cur, remote) && (b.cfg.own || b.cfg.keepSpelling) {
		return nil // the same store spelled another way: the clone's own spelling stays
	} else if b.cfg.own {
		return errs.New(errs.InvalidInput, "git: your clone "+b.work+" now points origin at another repository than the store").
			WithHint("point it back (`git -C " + b.work + " remote set-url origin <store url>`), or go back to lore's own clone: lore store use --default <store>")
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"remote", "set-url", "--", RemoteName, remote}}); err != nil {
		return errs.Wrap(errs.Internal, "git: update the store remote", err)
	}
	return nil
}

// checkout fetches the store and checks its branch out in a clone whose
// HEAD is still pendingBranch: an existing local branch (unpushed commits
// survive a cut-short open), else the remote branch, else a new branch with
// no commit for an empty store. The branch tracks origin, so a plain
// `git pull` in the clone works.
func (b *Backend) checkout(ctx context.Context) error {
	head, fetchErr := b.openFetch(ctx, b.work)
	if fetchErr != nil && !isOffline(fetchErr) {
		return fetchErr
	}
	if b.branch = b.cfg.branch; b.branch == "" {
		b.branch = DefaultBranch
		if fetchErr == nil {
			b.branch = head
		}
	}
	local, err := b.resolve(ctx, b.work, headsPrefix+b.branch)
	if err != nil {
		return err
	}
	tracking, err := b.resolve(ctx, b.work, trackingRefNS+b.branch)
	if err != nil {
		return err
	}
	if fetchErr != nil && local == "" && tracking == "" {
		if accessRefused(fetchErr) {
			return errs.Wrap(errs.StoreUnreachable, msgAccessRefused, fetchErr).WithHint(HintAccessRefused)
		}
		return errs.Wrap(errs.StoreUnreachable, "git: the store has no local copy yet and cannot be reached", fetchErr).
			WithHint("go online once; afterwards the local copy is used")
	}
	return b.switchTo(ctx, local, tracking)
}

// switchTo checks b.branch out from the pending branch: the local branch
// when it has a commit, else a new local branch at the tracking one, else
// the branch with no commit yet. It tracks origin either way, so a plain
// `git pull` in the clone works.
func (b *Backend) switchTo(ctx context.Context, local, tracking string) error {
	var args []string
	switch {
	case local != "":
		args = []string{"checkout", "--quiet", b.branch}
	case tracking != "":
		args = []string{"checkout", "--quiet", "-b", b.branch, trackingRefNS + b.branch}
	default:
		args = []string{"symbolic-ref", "HEAD", headsPrefix + b.branch}
	}
	if _, err := b.git.run(ctx, call{dir: b.work, args: args}); err != nil {
		return errs.Wrap(errs.Internal, "git: check out the store branch "+b.branch, err)
	}
	for _, kv := range [][2]string{{"remote", RemoteName}, {"merge", headsPrefix + b.branch}} {
		if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"config", "branch." + b.branch + "." + kv[0], kv[1]}}); err != nil {
			return errs.Wrap(errs.Internal, "git: set the store branch's upstream", err)
		}
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

// msgAccessRefused is the first-clone refusal when the remote answered but
// would not serve this machine (setupe2e v0117: a v0.1.17 store url
// git@github.com:<login>/<repo> fetched with a key of another account, or
// with no key at all). "Go online once" would be wrong advice there: the
// machine is online and the key is the problem. One line, because the
// setup page's startup warning prints only the message's first line; it
// never quotes ssh's own text (that stays in the wrapped cause).
const msgAccessRefused = "git: the store has no local copy yet, and its remote refused this machine's access (the ssh key or GitHub account in use cannot read it, or the repo does not exist)"

// HintAccessRefused is the next step for msgAccessRefused; wire's write
// guards reuse it for a store that could not be opened this way.
const HintAccessRefused = "check which account the key logs in as (`ssh -T git@github.com`); a setup with that store's account (`lore setup`) reaches it through the account's host alias or key"

// accessRefusedText lists the remote answers that mean "reached, but this
// machine may not read it", matched in git's stderr: ssh's publickey
// denial, GitHub's answer for a repo the key's account cannot see (it says
// "not found" rather than "forbidden", by design), and the https forms.
// Anything else (DNS, timeout, connection refused) stays "cannot be reached".
var accessRefusedText = []string{
	"Permission denied (publickey",
	"ERROR: Repository not found",
	"remote: Repository not found",
	"Authentication failed for",
	"The requested URL returned error: 403",
}

// AccessRefused reports whether err (any wrap of a store open or fetch
// error) is a remote that refused this machine's credentials rather than a
// remote that never answered: the caller's next step is the key or account
// (HintAccessRefused), not "go online".
func AccessRefused(err error) bool { return accessRefused(err) }

// accessRefused reports a failed fetch whose remote refused this machine's
// credentials (accessRefusedText), as opposed to a remote that never answered.
func accessRefused(err error) bool {
	msg := stderrOf(err)
	return slices.ContainsFunc(accessRefusedText, func(t string) bool { return strings.Contains(msg, t) })
}

// msgMissingRemoteRef is git's text for a fetched ref the remote lacks.
const msgMissingRemoteRef = "couldn't find remote ref"

// fetch updates every tracking branch of the clone (dir is the clone). A failed fetch is errOffline: lore treats any fetch it
// could not complete as "remote unreachable", and nothing local changes.
func (b *Backend) fetch(ctx context.Context, dir string) error {
	if b.openFetchFresh(ctx) {
		return nil // the open's own fetch just brought every head (openFetch)
	}
	return b.runFetch(ctx, dir, "fetch", "--quiet", "--no-tags", "--prune", "--no-write-fetch-head", "--", RemoteName, headsRefspec)
}

// runFetch runs one git fetch (args are the whole command line) and maps
// its result the way fetch documents: stamp on success or on a missing
// remote ref, errOffline for any other failure git reported.
func (b *Backend) runFetch(ctx context.Context, dir string, args ...string) error {
	if gitenv.NoStoreFetch(os.Environ()) {
		// LORE_NO_STORE_FETCH (or LORE_NO_FETCH): the parent fetched this
		// clone moments ago;
		// LORE_OFFLINE: this run reads what is on disk. Either way the
		// tracking refs there are what the store reads, and no stamp is
		// written (nothing was fetched by this process).
		return nil
	}
	_, err := b.git.run(ctx, call{dir: dir, args: args})
	if err == nil {
		b.stampFetch(ctx, dir) // RES 3.6: the remote answered (fresh.go)
		return nil
	}
	if _, ran := exitCode(err); !ran {
		return err // git never ran, or ctx ended
	}
	if strings.Contains(stderrOf(err), msgMissingRemoteRef) {
		b.stampFetch(ctx, dir)
		return nil
	}
	return errs.Wrap(errs.Offline, "git: fetch the store", errors.Join(errOffline, err))
}

// headsRefspec maps every remote branch onto its tracking branch.
const headsRefspec = "+" + headsPrefix + "*:" + trackingRefNS + "*"

// LiveTrackingPrefix + <branch> is where a store branch's v0.1.17 live ref
// (refs/lore/live/<branch> on the remote) is copied inside the store clone;
// wire reads it there (liverows.go liveTrackingPrefix is the same text).
const LiveTrackingPrefix = "refs/lore-remote/" + RemoteName + "/live/"

// liveRefspec maps every live ref of the remote onto LiveTrackingPrefix.
const liveRefspec = "+refs/lore/live/*:" + LiveTrackingPrefix + "*"

// remoteHeadRef is where `remote.<name>.followRemoteHEAD` records the
// remote's HEAD branch during openFetch; it is read and removed at once.
const remoteHeadRef = trackingRefNS + "HEAD"

// FetchScope marks one short sequence of store work (the startup schema
// update of `lore setup web`) in which a store's first-open fetch stands
// in for every later fetch of that store: Sync, the shared-file read
// before a first write, and wire's live-ref fetch (OpenFetchFresh). Why:
// the first open of a v0.1.17 store went to the remote four times in a
// row (fetch, ls-remote, the schema update's sync, the live-ref fetch),
// each a new ssh connection; inside a scope one fetch of every ref serves
// them all. Outside a scope nothing is reused, so an explicit sync always
// fetches; a push ends the reuse (pushed), so a lost race fetches again.
type FetchScope struct{ _ byte }

// fetchScopeKey is FetchScope's context key.
type fetchScopeKey struct{}

// WithFetchScope returns ctx carrying a new FetchScope.
func WithFetchScope(ctx context.Context) context.Context {
	return context.WithValue(ctx, fetchScopeKey{}, &FetchScope{})
}

// scopeOf is ctx's FetchScope, nil when it carries none.
func scopeOf(ctx context.Context) *FetchScope {
	s, _ := ctx.Value(fetchScopeKey{}).(*FetchScope)
	return s
}

// openFetch is the first open's only network call: one fetch, over one
// connection, of every branch, every live ref (refs/lore/live/*, which
// wire reads for v0.1.17 stores; dropped again by dropLiveRefs when the
// store turns out not to be one) and the remote's HEAD branch (git's
// followRemoteHEAD, so no separate ls-remote). The refspecs come from
// config (-c) rather than the command line because git follows the
// remote HEAD only on a configured-refspec fetch. head is the remote's
// default branch: DefaultBranch for an empty store, and an ls-remote only
// when the remote has branches but git did not record its HEAD (a git
// older than followRemoteHEAD, or a remote HEAD naming no branch).
func (b *Backend) openFetch(ctx context.Context, dir string) (head string, err error) {
	remote := "remote." + RemoteName + "."
	err = b.runFetch(ctx, dir, "-c", remote+"followRemoteHEAD=always", "-c", remote+"fetch="+headsRefspec, "-c", remote+"fetch="+liveRefspec,
		"fetch", "--quiet", "--no-tags", "--prune", "--no-write-fetch-head", RemoteName)
	if err != nil {
		return DefaultBranch, err
	}
	b.openScope.Store(scopeOf(ctx))
	b.openLive = true
	out, serr := b.git.run(ctx, call{dir: dir, args: []string{"symbolic-ref", "--quiet", remoteHeadRef}})
	if serr == nil {
		// remove it again: no other code path ever made it, and a clone
		// opened by an older lore has none.
		if _, derr := b.git.run(ctx, call{dir: dir, args: []string{"symbolic-ref", "--delete", remoteHeadRef}}); derr != nil {
			return DefaultBranch, errs.Wrap(errs.Internal, "git: remove the remote HEAD ref", derr)
		}
		if name, ok := strings.CutPrefix(trimmed(out), trackingRefNS); ok && validBranch(name) {
			return name, nil
		}
	}
	heads, lerr := b.git.run(ctx, call{dir: dir, args: []string{"for-each-ref", "--count=1", "--format=%(refname)", trackingRefNS}})
	if lerr != nil {
		return DefaultBranch, errs.Wrap(errs.Internal, "git: list the fetched branches", lerr)
	}
	if trimmed(heads) == "" {
		return DefaultBranch, nil // an empty store: ls-remote would say the same
	}
	return b.remoteHead(ctx, dir), nil
}

// dropLiveRefs removes the live refs openFetch copied when the store is
// not a v0.1.17 one (its marker is current): before openFetch, only
// wire's v0.1.17 path fetched them, so a migrated store keeps reading no
// live ref, as it did.
func (b *Backend) dropLiveRefs(ctx context.Context) error {
	if !b.openLive || b.marker.Version < rowfs.MarkerFirstStores {
		return nil
	}
	b.openLive = false
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"for-each-ref", "--format=%(refname)", LiveTrackingPrefix}})
	if err != nil {
		return errs.Wrap(errs.Internal, "git: list the live refs", err)
	}
	for ref := range strings.SplitSeq(trimmed(out), "\n") {
		if ref == "" {
			continue
		}
		if _, err := b.git.run(ctx, call{dir: b.work, args: []string{"update-ref", "-d", ref}}); err != nil {
			return errs.Wrap(errs.Internal, "git: remove a live ref", err)
		}
	}
	return nil
}

// openFetchFresh reports whether ctx is the FetchScope this Backend's
// first open fetched under, so that fetch stands in for another one.
func (b *Backend) openFetchFresh(ctx context.Context) bool {
	s := scopeOf(ctx)
	return s != nil && b.openScope.Load() == s
}

// OpenFetchFresh reports whether this Backend's open fetched the remote's
// branches and live refs (LiveTrackingPrefix) inside ctx's FetchScope and
// nothing has been pushed since, so a caller in that scope about to fetch
// a live ref of this clone can skip it.
func (b *Backend) OpenFetchFresh(ctx context.Context) bool { return b.openFetchFresh(ctx) }

// pushed ends the open fetch's reuse: the remote has (or may have, after
// a lost race) moved since.
func (b *Backend) pushed() { b.openScope.Store(nil) }

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
// and passes (its first writer adds the marker), as does one whose commits
// hold only hosting starter files (OV59, adopt.go); a store with commits,
// no marker and other files is not a lore store (store.ErrNotAStore).
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
		// OV59: a repo holding only hosting starter files (README, LICENSE,
		// .gitignore) is adopted like an empty one; anything else is refused
		// naming the files (adopt.go).
		foreign, ferr := b.foreignFiles(ctx, h)
		if ferr != nil {
			return ferr
		}
		if len(foreign) == 0 {
			return nil
		}
		return notAStore(foreign)
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
	add := append([]string{"add", "--"}, applied.Paths...)
	_, err := b.git.run(ctx, call{dir: b.work, args: add})
	var env []string
	if err == nil {
		// FLAG4: the person's git identity signs the commit when git has
		// one; only without it does lore pass its own (commitEnv).
		env, err = b.commitEnv(ctx, batch.Author)
	}
	if err == nil {
		msg := subjectOf(ctx, batch.Op, changes)
		if own, ok := store.CommitMessage(ctx); ok {
			msg = own // a write path that names its commit (the pick-up of hand edits)
		}
		trailers := store.CommitTrailers(ctx)
		if store.PersonAuthored(ctx) {
			// a person's own change: the batch squash keeps it a commit of
			// its own, so its author survives push mode batch (squashOnto)
			trailers = append(trailers, handEditTrailer)
		}
		_, err = b.git.run(ctx, call{dir: b.work, args: []string{"commit", "--quiet", "--no-verify", "--allow-empty", "-m", b.cfg.withTrailers(msg, trailers...)}, env: env})
	}
	if err == nil {
		return nil
	}
	undoErr := applied.Undo()
	restage := append([]string{"add", "-A", "--"}, applied.Paths...)
	_, stageErr := b.git.run(ctx, call{dir: b.work, args: restage})
	return errs.Wrap(errs.Internal, "git: commit the batch", errors.Join(err, undoErr, stageErr))
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
	if from == "" {
		if rows, ok := b.cachedFull(h); ok {
			return rows, store.Cursor(h), nil
		}
	}
	paths, err := b.changedPaths(ctx, from, h, b.folder.Pathspecs())
	if err != nil {
		return nil, "", err
	}
	names := make([]string, 0, len(paths))
	rels := make(map[string]string, len(paths))
	for _, p := range paths {
		if rel, ok := b.folder.FileRel(p); ok {
			names = append(names, h+":"+p)
			rels[h+":"+p] = rel
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
		r, derr := rowfs.DecodeRowFile(rels[name], doc)
		if derr != nil {
			continue // reported by Problems
		}
		rows = append(rows, r)
	}
	if from == "" {
		b.keepFull(h, rows)
	}
	return rows, store.Cursor(h), nil
}

// cachedFull is a copy of the full read at commit h, if the last one was
// read there (Backend.full). Copies, documents too: a caller may change
// what Read returns (storetest: a backend never hands out its own buffer).
func (b *Backend) cachedFull(h string) ([]store.Row, bool) {
	b.fullMu.Lock()
	defer b.fullMu.Unlock()
	if b.fullHead == "" || b.fullHead != h {
		return nil, false
	}
	return copyRows(b.fullRows), true
}

// keepFull records rows as the full read at commit h.
func (b *Backend) keepFull(h string, rows []store.Row) {
	b.fullMu.Lock()
	defer b.fullMu.Unlock()
	b.fullHead, b.fullRows = h, copyRows(rows)
}

// copyRows copies rows and their documents.
func copyRows(rows []store.Row) []store.Row {
	out := make([]store.Row, len(rows))
	for i, r := range rows {
		r.Doc = append([]byte(nil), r.Doc...)
		out[i] = r
	}
	return out
}

// changedPaths lists the files under pathspecs added or modified between
// from and to (every file at to when from is ""), root-relative.
func (b *Backend) changedPaths(ctx context.Context, from, to string, pathspecs []string) ([]string, error) {
	if from == "" {
		out, err := b.git.run(ctx, call{dir: b.work, args: append([]string{"ls-tree", "-r", "-z", "--name-only", to, "--"}, pathspecs...)})
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "git: list row files", err)
		}
		return splitZ(out), nil
	}
	out, err := b.git.run(ctx, call{dir: b.work, args: append([]string{"diff", "--name-status", "-z", "--no-renames", from, to, "--"}, pathspecs...)})
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
	out, err := b.git.run(ctx, call{dir: b.work, args: append([]string{"diff", "--name-only", "-z", "--no-renames", "--diff-filter=D", from, h, "--"}, b.folder.Pathspecs()...)})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: diff row files", err)
	}
	var missing []string
	for _, p := range splitZ(out) {
		if rel, ok := b.folder.FileRel(p); ok {
			missing = append(missing, rel)
		}
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

// branchCloneSep joins the clone folder and a pinned branch:
// <home>/stores/<owner>/<repo>@<branch>.
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

// sameStore reports whether two origin urls name the same store (one
// host/path); a url lore cannot parse (a local path) matches only itself.
func sameStore(a, b string) bool {
	if a == b {
		return true
	}
	ra, ea := RemoteOfRaw(a)
	rb, eb := RemoteOfRaw(b)
	return ea == nil && eb == nil && ra.Norm == rb.Norm
}
