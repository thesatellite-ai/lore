// live_ride.go — the ride-along commit (LORE_TEAM_SPEC.md F6). When main
// refuses direct pushes (protected), rows waiting to land ride on the next
// code commit as a separate "lore: land N rows" commit, added by the
// post-commit hook; pre-push only checks, and adds a fresh ride commit (and
// stops that push) when the pushed branch lacks the current rows.
package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitref"
	"saas/pkg/aicoder/live"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/style"
)

// envRideActive is set by lore while it makes a ride-along commit, so its
// own hooks do not run again (§1).
const envRideActive = "LORE_RIDE_ACTIVE"

// rideLogPrefix starts every ride-along line (git shows hook output).
const rideLogPrefix = "lore: "

// rideInProgressMarkers are files in the git dir that mean a rebase,
// cherry-pick, revert, merge or bisect is under way: no ride commit then
// (the user's history is being rewritten or assembled).
var rideInProgressMarkers = []string{"rebase-merge", "rebase-apply", "CHERRY_PICK_HEAD", "REVERT_HEAD", "MERGE_HEAD", "BISECT_LOG", "sequencer"}

// liveRepoHere opens the live session of the repo holding the cwd (hooks
// run at the repo top level, which need not be a lore project itself).
func liveRepoHere(ctx context.Context) *liveSession {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	return liveFor(ctx, cwd)
}

// livePostCommitHook is `lore sync hook post-commit`. It never fails the
// user's commit: problems are printed and the rows keep waiting.
func livePostCommitHook(ctx context.Context) error {
	s := liveRepoHere(ctx)
	if s == nil {
		return nil
	}
	branch, ok := s.rideBranch(ctx)
	if !ok {
		return nil
	}
	items, err := s.waitingRows(ctx)
	if err != nil || len(items) == 0 {
		return nil
	}
	head, _, err := s.repo.ResolveCommit(ctx, "HEAD")
	if err != nil {
		return nil
	}
	if _, err := s.rideCommit(ctx, branch, head, items); err != nil {
		fmt.Fprintln(os.Stderr, style.Warn(rideLogPrefix+"ride-along: "+err.Error()))
	}
	return nil
}

// livePrePushHook is `lore sync hook pre-push`: git passes the refs being
// pushed on stdin ("<local ref> <local sha> <remote ref> <remote sha>")
// and the remote's name and URL as args. First the F16 leak check over
// what the push sends (leak_prepush.go; fails closed, refuses the push).
// Then the ride-along: when the current branch is pushed without the
// current waiting rows, a fresh ride commit is added and this push is
// stopped so the next one carries it.
func livePrePushHook(ctx context.Context, stdin io.Reader, args []string) error {
	in, err := io.ReadAll(stdin)
	if err != nil {
		return errcodes.New(errcodes.Internal, "read the pre-push input").WithCause(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return errcodes.New(errcodes.Internal, "getwd").WithCause(err)
	}
	remote := "" // no remote named: every remote-tracking ref counts as "already there"
	if len(args) > 0 {
		remote = args[0]
	}
	if preErr := prePushLeakCheck(ctx, cwd, remote, parsePushUpdates(in)); preErr != nil {
		return preErr
	}
	s := liveRepoHere(ctx)
	if s == nil {
		return nil
	}
	branch, ok := s.rideBranch(ctx)
	if !ok {
		return nil
	}
	items, err := s.waitingRows(ctx)
	if err != nil || len(items) == 0 {
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
			fmt.Fprintln(os.Stderr, style.Warn(rideLogPrefix+"ride-along: "+err.Error()))
			return nil
		}
		if added != "" {
			return errcodes.New(errcodes.InvalidInput, "lore added '"+added+"' to "+branch+" (rows waiting for a protected main); run git push again").
				WithHint("the commit only adds .lore/data files of rows that finished in live")
		}
	}
	return nil
}

// rideBranch returns the branch a ride commit may go on, ok=false when F6
// says skip: detached HEAD, the default branch, or a rebase / cherry-pick /
// revert / merge / bisect in progress.
func (s *liveSession) rideBranch(ctx context.Context) (string, bool) {
	out, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}})
	if err != nil {
		return "", false // detached
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == s.defaultBranch(ctx) {
		return "", false
	}
	gitDir, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--absolute-git-dir"}})
	if err != nil {
		return "", false
	}
	for _, m := range rideInProgressMarkers {
		if _, err := os.Stat(filepath.Join(strings.TrimSpace(string(gitDir)), m)); err == nil {
			return "", false
		}
	}
	return branch, true
}

// waitingRows are rows whose direct landing main refused for protection
// and that main does not hold yet, across every project folder of the repo
// (one ride commit carries them all).
func (s *liveSession) waitingRows(ctx context.Context) ([]live.LandItem, error) {
	if readLiveNoteText(s.stateDir, liveNoteProtected) == "" {
		return nil, nil
	}
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

// rideCommit adds the ride commit on branch (whose tip is head) and brings
// the index and work tree up to it for exactly those paths. It returns the
// commit subject, or "" when the branch already carries the current rows
// or a landed path has uncommitted changes (then it says why).
func (s *liveSession) rideCommit(ctx context.Context, branch, head string, items []live.LandItem) (string, error) {
	read := func(p string) ([]byte, bool, error) { return s.repo.ReadFile(ctx, head, p) }
	ch, changed, err := live.BuildLanding(ctx, read, items, liveLayout)
	if err != nil {
		return "", err
	}
	if ch.Empty() {
		return "", nil
	}
	paths := append(append([]string{}, keysSorted(ch.Put)...), ch.Delete...)
	dirty, err := s.repo.Run(ctx, gitref.Invocation{Args: append([]string{"status", "--porcelain=v1", "--untracked-files=all", "--"}, paths...)})
	if err != nil {
		return "", err
	}
	if d := strings.TrimSpace(string(dirty)); d != "" {
		fmt.Fprintf(os.Stderr, "%sskipped the ride-along commit: a landed file has uncommitted changes (%s); the rows keep waiting\n", rideLogPrefix, strings.ReplaceAll(d, "\n", "; "))
		return "", nil
	}
	msg := live.LandMessage(changed)
	c, err := s.repo.MakeCommit(ctx, gitref.CommitRequest{Base: head, Changes: ch, Message: msg, Signature: s.ref.Signed()})
	if err != nil {
		return "", err
	}
	env := []string{envRideActive + "=1"}
	if _, err := s.repo.Run(ctx, gitref.Invocation{Env: env, Args: []string{"update-ref", "-m", "lore: ride-along", "refs/heads/" + branch, c.OID, head}}); err != nil {
		return "", fmt.Errorf("move %s: %w", branch, err)
	}
	if len(ch.Put) > 0 {
		args := append([]string{"checkout", c.OID, "--"}, keysSorted(ch.Put)...)
		if _, err := s.repo.Run(ctx, gitref.Invocation{Env: env, Args: args}); err != nil {
			return "", fmt.Errorf("update the work tree: %w", err)
		}
	}
	if len(ch.Delete) > 0 {
		args := append([]string{"rm", "-q", "--ignore-unmatch", "--"}, ch.Delete...)
		if _, err := s.repo.Run(ctx, gitref.Invocation{Env: env, Args: args}); err != nil {
			return "", fmt.Errorf("update the index: %w", err)
		}
	}
	subject, _, _ := strings.Cut(msg, "\n")
	ids := make([]string, 0, len(changed))
	for _, it := range changed {
		ids = append(ids, it.Key.ID)
	}
	fmt.Fprintf(os.Stderr, "%sadded %q to %s (%s)\n", rideLogPrefix, subject, branch, strings.Join(ids, ", "))
	return subject, nil
}

func keysSorted(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// withoutLiveRows drops changed .lore/data paths whose row is live (read
// from lore.db's record of live rows). shadowed reports whether any was
// dropped.
func withoutLiveRows(ctx context.Context, db *sql.DB, changed []string) ([]string, bool) {
	states, err := lsync.LiveStates(ctx, db)
	if err != nil || len(states) == 0 {
		return changed, false
	}
	out := changed[:0:0]
	shadowed := false
	for _, p := range changed {
		table, id := filepath.Base(filepath.Dir(p)), strings.TrimSuffix(filepath.Base(p), ".json")
		if _, isLive := states[lsync.LiveKey{Table: table, ID: id}]; isLive {
			shadowed = true
			continue
		}
		out = append(out, p)
	}
	return out, shadowed
}
