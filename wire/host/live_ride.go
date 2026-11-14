package host

// live_ride.go — the ride-along commit (LORE_TEAM_SPEC F6) for a binary
// built from the root module; ported from saas/cmd/cli live_ride.go.
//
// When the default branch refuses a direct landing (protected), the rows
// waiting to land ride on the next code commit of a feature branch as a
// separate "lore: land N rows" commit:
//
//	landing pass ─▶ LandProtected ─▶ note land-protected + install the
//	                 post-commit / pre-push blocks (ensureRideHooks)
//	git commit   ─▶ post-commit ─▶ `lore sync hook post-commit` ─▶ ride commit
//	git push     ─▶ pre-push    ─▶ `lore sync hook pre-push`: the pushed
//	                 branch lacks the current rows ─▶ fresh ride commit, the
//	                 push stops ("run git push again")
//
// Skipped (F6): detached HEAD, the default branch, a rebase / cherry-pick /
// revert / merge / bisect in progress, a landed path with uncommitted
// changes (says why). LORE_RIDE_ACTIVE guards re-entry: lore's own git
// calls run the hooks again, which then do nothing.
//
// The hook blocks are the old binary's, byte for byte, under the same
// marker (lore-sync:live), so the two binaries never rewrite each other's
// block. Not ported (P3-MODEB-6): the pre-push F16 check of the pushed
// commits' .lore/data (the old binary's leak_prepush.go); the ride commit
// itself gets the F16 check of its rows.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/adapters/execfile"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/model/errs"
)

// EnvRideActive is set by lore while it makes a ride-along commit, so its
// own hooks do not run again (§1).
const EnvRideActive = "LORE_RIDE_ACTIVE"

// Ride-along words.
const (
	rideLogPrefix   = "lore: "
	leakWhereRide   = "ride-along commit"
	syncHookUse     = "hook <pre-commit|refresh|post-commit|pre-push>"
	hookPostCommit  = "post-commit"
	hookPrePush     = "pre-push"
	rideHookMarker  = "lore-sync:live"
	rideHookShebang = "#!/bin/sh"
	rideHookMode    = 0o755
	rideHookDirMode = 0o755
	rideExecBits    = 0o111
)

// rideHookLines are the blocks chained into post-commit and pre-push (the
// old binary's hookLines, live part). A lore too old to know the hook
// names must not refuse a push, so both probe `lore land --help` first.
var rideHookLines = map[string][]string{
	hookPostCommit: {"( command -v lore >/dev/null 2>&1 && lore land --help >/dev/null 2>&1 && lore sync hook post-commit ) || true"},
	hookPrePush:    {`if command -v lore >/dev/null 2>&1 && lore land --help >/dev/null 2>&1; then lore sync hook pre-push "$@" || exit $?; fi`},
}

// rideBlockLines are rideHookLines[hook], guarded when the hooks folder
// is shared by every repo (sharedHookLines, D-21).
func rideBlockLines(hook string, shared bool) []string {
	if shared {
		return sharedHookLines("", rideHookLines[hook])
	}
	return rideHookLines[hook]
}

// rideInProgressMarkers are files in the git dir that mean the user's
// history is being rewritten or assembled: no ride commit then.
// The refresh hook and post-commit skip on them too (historyInProgress).
var rideInProgressMarkers = []string{"rebase-merge", "rebase-apply", "CHERRY_PICK_HEAD", "REVERT_HEAD", "MERGE_HEAD", "BISECT_LOG", "sequencer"}

// Live is the live-rows wiring of a binary: the operation hook and the
// host commands that need the same configuration (`lore sync hook`).
type Live struct{ h *liveHost }

// NewLive builds the live wiring for cfg.
func NewLive(cfg LiveConfig) *Live { return &Live{h: &liveHost{cfg: cfg}} }

// hookRefresh is the hook name of the post-checkout, post-merge and
// post-rewrite blocks (gitwire.go).
const hookRefresh = "refresh"

// SyncHookCommand is the hidden `lore sync hook <name>` the git hook blocks
// call: post-commit and pre-push are the ride-along, pre-commit stages and
// checks .lore/data (precommit.go), refresh re-renders LORE.md after git
// changed the rows (refresh: nil does nothing; it never fails git). Any
// other name is E_INVALID_INPUT: a typo in a hook block must show, not pass
// as a hook that checked nothing. afterCommit (nil: nothing) runs after
// post-commit's ride-along, live rows on or off: the commit notices that
// need the render side, such as a folder scope the commit moved away
// (B3-5). It must be cheap and never fail the commit.
func (l *Live) SyncHookCommand(stdin io.Reader, refresh Refresh, afterCommit func(context.Context), commitRender CommitRender) *cobra.Command {
	return &cobra.Command{
		Use:    syncHookUse,
		Short:  "Entry point for lore's git hook blocks",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1), // pre-push also gets git's remote name and url
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !syncHookNames[name] {
				return errs.New(errs.InvalidInput, "unknown hook "+strconv.Quote(name)).
					WithHint("the hook blocks call one of: " + strings.Join(syncHookList, ", "))
			}
			if os.Getenv(EnvRideActive) != "" {
				return nil
			}
			switch name {
			case hookPostCommit:
				if !liveDisabled() {
					l.h.postCommit(cmd.Context())
				}
				if afterCommit != nil {
					afterCommit(cmd.Context())
				}
			case hookPrePush:
				return l.h.prePush(cmd.Context(), stdin, args[1:])
			case hookPreCommit:
				if err := l.h.preCommit(cmd.Context()); err != nil {
					return err
				}
				l.h.stageCommitRender(cmd.Context(), commitRender)
			case hookRefresh:
				l.h.afterRewrite(cmd.Context(), stdin, args[1:])
				if cwd, err := os.Getwd(); refresh != nil && err == nil && !refreshDeferred(cmd.Context(), l.h.git(), cwd, args[1:]) {
					refresh(cmd.Context())
				}
			}
			return nil
		},
	}
}

// Refresh is the post-checkout, post-merge and post-rewrite re-render.
// cmd/lore passes its refreshHook, which leaves a tracked render that
// matches the index alone after any of them (A-12, G-2): a hook that
// dirties a tracked file makes the person's next `git pull` refuse.
type Refresh func(ctx context.Context)

// syncHookList are the names `lore sync hook` takes, in help order.
var syncHookList = []string{hookPreCommit, hookRefresh, hookPostCommit, hookPrePush}

// syncHookNames is syncHookList as a set.
var syncHookNames = func() map[string]bool {
	m := make(map[string]bool, len(syncHookList))
	for _, n := range syncHookList {
		m[n] = true
	}
	return m
}()

// Prime runs the live session's prepare once for the repo holding the cwd
// (lock, tracking refspec, the fetch when due and its stamp, integrate)
// outside an operation. `lore init` calls it: v0.1.17's init wrote through
// the live wrapper, so the first command after it found a fresh fetch
// stamp and did not fetch again (Leak_WriteCheckNoOpWithoutOrg).
func (l *Live) Prime(ctx context.Context) {
	// Inside an operation (repo.init through the registry) the live hook's
	// own run already prepares and finishes the session: a second one would
	// wait on the same lock.
	if os.Getenv(EnvRideActive) != "" || liveDisabled() || runFrom(ctx) != nil {
		return
	}
	if r, s := l.h.hookSession(ctx); s != nil {
		// init wires the clone at once, as v0.1.17's init did through its
		// first sync pass: the .gitattributes block then exists before the
		// project's first commit and is committed with it. Wired only by a
		// later command instead, it sits untracked in each clone and a
		// merge bringing a teammate's committed copy aborts ("untracked
		// working tree files would be overwritten").
		if root := liveProjectRoot(l.h.cfg.WorkDir); root != "" {
			s.ensureGitWiring(ctx, root, wireInstall)
		}
		r.finish(ctx)
	}
}

// hookSession opens the live session of the repo holding the cwd (git runs
// hooks at the top level, which need not be a lore project itself). The
// caller finishes the returned run.
func (h *liveHost) hookSession(ctx context.Context) (*liveRun, *liveSession) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil
	}
	top, _ := livePlace(cwd)
	r := &liveRun{h: h, sessions: map[string]*liveSession{}}
	return r, r.session(withRun(ctx, r), top)
}

// withRun is ctx carrying r, the way LiveHook's Around runs an operation.
// A git hook reads rows through the app (the F16 leak check of a ride or a
// push): without the run in its ctx the repo store's live overlay sees no
// session, live-only rows look missing, and the app's open printed the
// v0.1.17 "live rows not visible yet; run `lore migrate run`" note in
// every `git commit` / `git push` of a repo with open live rows.
func withRun(ctx context.Context, r *liveRun) context.Context {
	// Every session caches git reads (liveRun.session), and only the writes
	// that go through a session's own cache drop its answers. The operation
	// also runs git beside it: the signals store's push (whose atomic form
	// moves refs/lore-remote/<remote>/live), reposync's promote and ci-merge,
	// the work-branch push, the pre-commit staging. Any of those that runs
	// through gitref.Exec under this context drops every session's cache
	// before and after it, so no write made beside the cache can leave a
	// stale answer, including one added later that nobody audited.
	ctx = gitref.WithWriteObserver(ctx, func(gitref.Invocation) { r.dropCaches() })
	return context.WithValue(ctx, liveRunKey{}, r)
}

// postCommit is `lore sync hook post-commit`. It never fails the commit:
// problems are printed and the rows keep waiting.
func (h *liveHost) postCommit(ctx context.Context) {
	// A rebase or cherry-pick runs post-commit for every commit it makes:
	// opening the session would fetch and integrate in the middle of it.
	if cwd, err := os.Getwd(); err != nil || historyInProgress(ctx, h.git(), cwd) {
		return
	}
	r, s := h.hookSession(ctx)
	if s == nil {
		return
	}
	ctx = withRun(ctx, r)
	defer r.finish(ctx)
	branch, ok := s.rideBranch(ctx)
	if !ok {
		return
	}
	items, err := s.waitingRows(ctx)
	if err != nil || len(items) == 0 {
		return
	}
	head, _, err := s.repo.ResolveCommit(ctx, "HEAD")
	if err != nil {
		return
	}
	if _, err := s.rideCommit(ctx, branch, head, items); err != nil {
		h.line(rideLogPrefix + "ride-along: " + err.Error())
	}
}

// prePush is `lore sync hook pre-push`: git passes "<local ref> <local
// sha> <remote ref> <remote sha>" lines on stdin. When the current branch
// is pushed without the current waiting rows, a fresh ride commit is added
// and this push stops so the next one carries it.
func (h *liveHost) prePush(ctx context.Context, stdin io.Reader, args []string) error {
	in, err := io.ReadAll(stdin)
	if err != nil {
		return errs.Wrap(errs.Internal, "read the pre-push input", err)
	}
	// The check reads rows through the app: with live rows on, it runs
	// under this hook's run (withRun) so live-only rows are seen. A refused
	// push only drops the lock: nothing of the run is pushed.
	var r *liveRun
	if !liveDisabled() {
		r = &liveRun{h: h, sessions: map[string]*liveSession{}}
		ctx = withRun(ctx, r)
	}
	// First F16 over what the push sends (live_prepush.go; fails closed).
	cwd, cerr := os.Getwd()
	if cerr == nil {
		top, _ := livePlace(cwd)
		if repo, oerr := gitref.Open(ctx, top, h.git()); oerr == nil && !repo.Bare() {
			remote := ""
			if len(args) > 0 {
				remote = args[0]
			}
			updates := parsePushUpdates(in)
			lerr := h.prePushLeakCheck(ctx, repo, remote, updates)
			if lerr == nil {
				lerr = prePushDropCheck(ctx, repo, updates) // H-7: a forced push that takes rows away
			}
			if lerr != nil {
				if r != nil {
					r.release()
				}
				return lerr
			}
		}
	}
	if r == nil || cerr != nil {
		return nil
	}
	defer r.finish(ctx)
	top, _ := livePlace(cwd)
	s := r.session(ctx, top)
	if s == nil {
		return nil
	}
	if len(args) > 0 && args[0] == s.ref.RemoteName {
		// The person's own push to the live remote says "send": the
		// waiting live writes go with it (finish pushes them), whatever
		// live.autopush says (U-2): a push the person asked for (S-1).
		s.kind = live.FetchAlways
		s.asked = true
	}
	branch, items, ok := s.prePushRows(ctx)
	if !ok || len(items) == 0 {
		return nil
	}
	head, _, err := s.repo.ResolveCommit(ctx, "HEAD")
	if err != nil {
		return nil
	}
	sc := bufio.NewScanner(bytes.NewReader(in))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[0] != "refs/heads/"+branch || f[1] != head {
			continue
		}
		added, err := s.rideCommit(ctx, branch, head, items)
		if err != nil {
			var coded *errs.Error
			if errors.As(err, &coded) {
				return err // a leak refusal stops the push
			}
			h.line(rideLogPrefix + "ride-along: " + err.Error())
			return nil
		}
		if added != "" {
			why := "rows waiting for a protected main"
			if branch == s.defaultBranch(ctx) {
				why = "rows that finished in live land with your push"
			}
			// E_PUSH_AGAIN, not an input error (A-13): the push is right,
			// it only has to carry the commit lore just added
			return errs.New(errs.PushAgain, "lore added '"+added+"' to "+branch+" ("+why+"); run git push again").
				WithHint("the commit only adds .lore/data files of rows that finished in live; the same git push now sends it (a script that pushes once can retry on E_PUSH_AGAIN, exit 23)")
		}
	}
	return nil
}

// prePushRows are the branch a pre-push ride commit goes on and the rows
// it carries. On the default branch it is the landing folded into the
// person's own `git push` (U-2: lore never moves the remote default branch
// by itself, so the rows finished in live land with the person's push and
// main never diverges behind their back); on another branch it is the F6
// ride-along of rows a protected main refused. ok=false: no ride commit.
func (s *liveSession) prePushRows(ctx context.Context) (string, []live.LandItem, bool) {
	out, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}})
	if err != nil {
		return "", nil, false // detached
	}
	if branch := strings.TrimSpace(string(out)); branch != "" && branch == s.defaultBranch(ctx) {
		if busy, err := repoHistoryInProgress(ctx, s.repo); err != nil || busy || s.blocked != nil {
			return "", nil, false
		}
		items, err := s.landableRows(ctx)
		return branch, items, err == nil
	}
	branch, ok := s.rideBranch(ctx)
	if !ok {
		return "", nil, false
	}
	items, err := s.waitingRows(ctx)
	return branch, items, ok && err == nil
}

// rideBranch is the branch a ride commit may go on; false when F6 says
// skip (see the file comment).
func (s *liveSession) rideBranch(ctx context.Context) (string, bool) {
	out, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}})
	if err != nil {
		return "", false // detached
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == s.defaultBranch(ctx) {
		return "", false
	}
	if busy, err := repoHistoryInProgress(ctx, s.repo); err != nil || busy {
		return "", false
	}
	return branch, true
}

// refreshDeferred reports whether `lore sync hook refresh <hook> [arg]`
// must leave .lore/LORE.md alone: a rebase, merge, cherry-pick, revert,
// bisect or am is under way in the checkout holding dir, and git
// refuses its next step over a work tree file the hook changed ("cannot
// rebase: You have unstaged changes"). The post-rewrite that ends a
// rebase ("post-rewrite rebase") runs while git's rebase state is still on
// disk, but every pick is done: that one renders, so the rows a pull
// brought show at once. Anything else waits for that post-rewrite or the
// next lore command. Blocks written before the hook name was passed (no
// args) are treated as a checkout.
func refreshDeferred(ctx context.Context, git gitref.Git, dir string, args []string) bool {
	if len(args) >= 2 && args[0] == hookPostRewrite && args[1] == rewriteByRebase {
		return false
	}
	return historyInProgress(ctx, git, dir)
}

// rewriteByRebase is post-rewrite's first argument after a rebase (the
// other is "amend").
const rewriteByRebase = "rebase"

// historyInProgress reports whether the git checkout holding dir is in the
// middle of a rebase, merge, cherry-pick, revert, bisect or am (one of
// rideInProgressMarkers in its git dir). Not a git checkout: false.
func historyInProgress(ctx context.Context, git gitref.Git, dir string) bool {
	repo, err := gitref.Open(ctx, dir, git)
	if err != nil {
		return false
	}
	busy, err := repoHistoryInProgress(ctx, repo)
	return err == nil && busy
}

// repoHistoryInProgress is historyInProgress for an open repo; the markers
// are looked up in the worktree's own git dir, where git keeps them.
func repoHistoryInProgress(ctx context.Context, repo *gitref.Repo) (bool, error) {
	gitDir, err := repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--absolute-git-dir"}})
	if err != nil {
		return false, err
	}
	for _, m := range rideInProgressMarkers {
		if _, err := os.Stat(filepath.Join(strings.TrimSpace(string(gitDir)), m)); err == nil {
			return true, nil
		}
	}
	return false, nil
}

// waitingRows are the rows whose direct landing the default branch refused
// as protected and that it does not hold yet, across every project folder
// of the repo (one ride commit carries them all).
func (s *liveSession) waitingRows(ctx context.Context) ([]live.LandItem, error) {
	if s.noteText(liveNoteProtected) == "" {
		return nil, nil // never refused as protected on this machine
	}
	return s.landableRows(ctx)
}

// landableRows are the live rows a landing pass would land or copy now,
// in every live folder, without companions that would land alone.
func (s *liveSession) landableRows(ctx context.Context) ([]live.LandItem, error) {
	_, tree, err := s.ref.LocalTree(ctx)
	if err != nil {
		return nil, err
	}
	var items []live.LandItem
	for _, folder := range tree.Folders() {
		plan, err := s.plan(ctx, folder, nil)
		if err != nil {
			return nil, err
		}
		for _, p := range plan {
			if p.Decision.Action == live.LandFinal || p.Decision.Action == live.LandCopy {
				items = append(items, live.LandItem{Folder: folder, Key: p.Key, File: p.File, Action: p.Decision.Action})
			}
		}
	}
	return withoutLoneSupporting(items), nil
}

// noteText is a machine note of .git/lore ("" when absent or unreadable).
func (s *liveSession) noteText(name string) string {
	b, err := os.ReadFile(filepath.Clean(filepath.Join(s.stateDir, name)))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// rideCommit adds the ride commit on branch (tip head) and brings the index
// and work tree up to it for exactly those paths. It returns the commit
// subject, or "" when the branch already carries the current rows or a
// landed path has uncommitted changes (then it says why).
func (s *liveSession) rideCommit(ctx context.Context, branch, head string, items []live.LandItem) (string, error) {
	byFolder := map[string]map[live.RowKey]live.File{}
	for _, it := range items {
		if byFolder[it.Folder] == nil {
			byFolder[it.Folder] = map[live.RowKey]live.File{}
		}
		byFolder[it.Folder][it.Key] = it.File
	}
	for folder, rows := range byFolder {
		if err := s.h.leakCheck(ctx, leakWhereRide, folder, rows); err != nil {
			return "", err
		}
	}
	read := func(p string) ([]byte, bool, error) { return s.repo.ReadFile(ctx, head, p) }
	ch, changed, err := live.BuildLanding(ctx, read, items, s.layout())
	if err != nil {
		return "", err
	}
	if ch.Empty() {
		return "", nil
	}
	puts := make([]string, 0, len(ch.Put))
	for p := range ch.Put {
		puts = append(puts, p)
	}
	sort.Strings(puts)
	paths := append(append([]string{}, puts...), ch.Delete...)
	dirty, err := s.repo.Run(ctx, gitref.Invocation{Args: append([]string{"status", "--porcelain=v1", "--untracked-files=all", "--"}, paths...)})
	if err != nil {
		return "", err
	}
	if d := strings.TrimSpace(string(dirty)); d != "" {
		s.h.line(rideLogPrefix + "skipped the ride-along commit: a landed file has uncommitted changes (" + strings.ReplaceAll(d, "\n", "; ") + "); the rows keep waiting")
		return "", nil
	}
	msg := live.LandMessage(changed)
	c, err := s.repo.MakeCommit(ctx, gitref.CommitRequest{Base: head, Changes: ch, Message: msg, Signature: s.ref.Signed()})
	if err != nil {
		return "", err
	}
	env := []string{EnvRideActive + "=1"}
	if _, err := s.repo.Run(ctx, gitref.Invocation{Env: env, Args: []string{"update-ref", "-m", "lore: ride-along", "refs/heads/" + branch, c.OID, head}}); err != nil {
		return "", fmt.Errorf("move %s: %w", branch, err)
	}
	if len(puts) > 0 {
		if _, err := s.repo.Run(ctx, gitref.Invocation{Env: env, Args: append([]string{"checkout", c.OID, "--"}, puts...)}); err != nil {
			return "", fmt.Errorf("update the work tree: %w", err)
		}
	}
	if len(ch.Delete) > 0 {
		if _, err := s.repo.Run(ctx, gitref.Invocation{Env: env, Args: append([]string{"rm", "-q", "--ignore-unmatch", "--"}, ch.Delete...)}); err != nil {
			return "", fmt.Errorf("update the index: %w", err)
		}
	}
	subject, _, _ := strings.Cut(msg, "\n")
	ids := make([]string, 0, len(changed))
	for _, it := range changed {
		ids = append(ids, it.Key.ID)
	}
	s.h.line(fmt.Sprintf("%sadded %q to %s (%s)", rideLogPrefix, subject, branch, strings.Join(ids, ", ")))
	return subject, nil
}

// ensureRideHooks chains the post-commit and pre-push blocks into the hook
// folder git uses (core.hooksPath when set: a hook manager's scripts are
// extended, never replaced). Called when a landing is refused as
// protected, the moment a ride-along becomes possible. Best effort: a
// failure is a status line, the rows keep waiting for `lore land`.
func (s *liveSession) ensureRideHooks(ctx context.Context) {
	dir, shared, err := s.hooksFolder(ctx)
	if err != nil {
		s.warn("ride-along hooks: " + err.Error())
		return
	}
	hooks := []string{hookPostCommit, hookPrePush}
	writes := make([]hookWrite, 0, len(hooks))
	for _, hook := range hooks {
		writes = append(writes, hookWrite{name: hook, marker: rideHookMarker, lines: rideBlockLines(hook, shared)})
	}
	// stories H-1: never a new or changed file in a hooks folder the repo
	// commits (husky's .husky) without asking; `lore fix` writes those
	created, changed := s.teamHookWrites(ctx, dir, writes)
	team := map[string]bool{}
	for _, n := range append(append([]string{}, created...), changed...) {
		team[n] = true
	}
	if len(team) > 0 {
		s.cmdNotice(ctx, fmt.Sprintf(trackedHooksSkipped, teamHookNames(created, changed), dir))
	}
	for _, hook := range hooks {
		if team[hook] {
			continue
		}
		if err := ensureHookBlock(dir, hook, rideHookMarker, rideBlockLines(hook, shared)); err != nil {
			s.warn("ride-along hooks: " + err.Error())
			return
		}
	}
}

// ensureHookBlock makes the hook script contain exactly one block under
// marker: replaced where it stands, else put right after the shebang
// (before an `exit 0` a script may end with), and the script executable.
// Content outside the block is never touched. The new script replaces the
// old file (replaceHookFile), so a hook that is running reads on unchanged.
func ensureHookBlock(dir, hook, marker string, lines []string) error {
	if err := os.MkdirAll(dir, rideHookDirMode); err != nil {
		return err
	}
	// Through a root at the hooks folder: hook is one of lore's fixed
	// names, so the file can never be outside it.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }() // read-and-write errors are returned below; a close error adds nothing
	cur, err := root.ReadFile(hook)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	content := string(cur)
	next := hookBlockNext(content, marker, lines)
	// Under execfile.Do: git may run this hook while another goroutine of
	// this process forks, and a child holding the write descriptor would
	// make that run fail with "text file busy".
	return execfile.Do(func() error {
		if next != content {
			if err := replaceHookFile(root, hook, []byte(next)); err != nil {
				return err
			}
		}
		info, err := root.Stat(hook)
		if err != nil {
			return err
		}
		if info.Mode().Perm()&rideExecBits == 0 {
			return root.Chmod(hook, rideHookMode)
		}
		return nil
	})
}

// hookTmpSuffix names the file a new hook script is written to before it
// replaces the old one (one per process, in the hooks folder).
const hookTmpSuffix = ".lore-tmp-"

// replaceHookFile writes body to a new file and renames it over hook, so
// a hook script that is running at that moment keeps reading the old
// bytes. sh reads a script as it goes, from an offset: a hook rewritten in
// place under it (the commit-msg hook runs `lore check leaks`, and that
// lore re-wires a clone whose hooks an older lore wrote) resumes in the
// middle of the new text and fails the commit with `Syntax error: ")"
// unexpected` (the first commit after an upgrade from 0.1.17). The old
// file's permissions are kept. A hook that is a symlink (a hook manager's
// link) is written through in place, as before, so the link stays.
func replaceHookFile(root *os.Root, hook string, body []byte) error {
	mode := fs.FileMode(rideHookMode)
	switch info, err := root.Lstat(hook); {
	case err == nil && info.Mode()&fs.ModeSymlink != 0:
		return root.WriteFile(hook, body, rideHookMode)
	case err == nil:
		mode = info.Mode().Perm()
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	tmp := hook + hookTmpSuffix + strconv.Itoa(os.Getpid())
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, werr := f.Write(body)
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = root.Chmod(tmp, mode) // OpenFile's mode is cut by the umask; the old file's bits are kept as they were
	}
	if werr == nil {
		if rerr := root.Rename(tmp, hook); rerr != nil {
			// A system that refuses to rename over an open file (Windows,
			// while git runs the hook): write in place, as before.
			_ = root.Remove(tmp) // best effort: the write below decides
			return root.WriteFile(hook, body, mode)
		}
		return nil
	}
	_ = root.Remove(tmp) // best effort: the error that matters is werr
	return werr
}

// hookBlockNext is a hook file's content with lore's block for marker
// holding exactly lines: replaced in place, else put after the shebang
// (a new file gets one). Equal to content when nothing changes.
func hookBlockNext(content, marker string, lines []string) string {
	start, end := "# >>> "+marker+" >>>", "# <<< "+marker+" <<<"
	block := start + "\n" + strings.Join(lines, "\n") + "\n" + end + "\n"
	next := content
	if i := strings.Index(content, start); i >= 0 {
		if j := strings.Index(content[i:], end); j >= 0 {
			k := i + j + len(end)
			if k < len(content) && content[k] == '\n' {
				k++
			}
			next = content[:i] + block + content[k:]
		}
	}
	if next == content && !strings.Contains(content, start) {
		switch {
		case content == "":
			next = rideHookShebang + "\n" + block
		case strings.HasPrefix(content, "#!"):
			if nl := strings.IndexByte(content, '\n'); nl >= 0 {
				next = content[:nl+1] + block + content[nl+1:]
			} else {
				next = content + "\n" + block
			}
		default:
			next = block + content
		}
	}
	return next
}
