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
	"strings"

	"github.com/spf13/cobra"

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

// rideInProgressMarkers are files in the git dir that mean the user's
// history is being rewritten or assembled: no ride commit then.
var rideInProgressMarkers = []string{"rebase-merge", "rebase-apply", "CHERRY_PICK_HEAD", "REVERT_HEAD", "MERGE_HEAD", "BISECT_LOG", "sequencer"}

// Live is the live-rows wiring of a binary: the operation hook and the
// host commands that need the same configuration (`lore sync hook`).
type Live struct{ h *liveHost }

// NewLive builds the live wiring for cfg.
func NewLive(cfg LiveConfig) *Live { return &Live{h: &liveHost{cfg: cfg}} }

// SyncHookCommand is the hidden `lore sync hook <name>` the git hook blocks
// call. post-commit and pre-push are the ride-along; the other names the
// old binary's blocks use (pre-commit, refresh) do nothing here: this
// binary validates rows on write, and a hook must never block git over a
// lore it cannot run.
func (l *Live) SyncHookCommand(stdin io.Reader) *cobra.Command {
	return &cobra.Command{
		Use:    syncHookUse,
		Short:  "Entry point for lore's git hook blocks",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv(EnvRideActive) != "" || liveDisabled() {
				return nil
			}
			switch args[0] {
			case hookPostCommit:
				l.h.postCommit(cmd.Context())
				return nil
			case hookPrePush:
				return l.h.prePush(cmd.Context(), stdin)
			}
			return nil
		},
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
	return r, r.session(ctx, top)
}

// postCommit is `lore sync hook post-commit`. It never fails the commit:
// problems are printed and the rows keep waiting.
func (h *liveHost) postCommit(ctx context.Context) {
	r, s := h.hookSession(ctx)
	if s == nil {
		return
	}
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
func (h *liveHost) prePush(ctx context.Context, stdin io.Reader) error {
	in, err := io.ReadAll(stdin)
	if err != nil {
		return errs.Wrap(errs.Internal, "read the pre-push input", err)
	}
	r, s := h.hookSession(ctx)
	if s == nil {
		return nil
	}
	defer r.finish(ctx)
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
			var coded *errs.Error
			if errors.As(err, &coded) {
				return err // a leak refusal stops the push
			}
			h.line(rideLogPrefix + "ride-along: " + err.Error())
			return nil
		}
		if added != "" {
			return errs.New(errs.InvalidInput, "lore added '"+added+"' to "+branch+" (rows waiting for a protected main); run git push again").
				WithHint("the commit only adds .lore/data files of rows that finished in live")
		}
	}
	return nil
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

// waitingRows are the rows whose direct landing the default branch refused
// as protected and that it does not hold yet, across every project folder
// of the repo (one ride commit carries them all).
func (s *liveSession) waitingRows(ctx context.Context) ([]live.LandItem, error) {
	if b, err := os.ReadFile(filepath.Join(s.stateDir, liveNoteProtected)); err != nil || strings.TrimSpace(string(b)) == "" {
		return nil, nil // never refused as protected on this machine
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
	ch, changed, err := live.BuildLanding(ctx, read, items, liveLayout)
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
	out, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--path-format=absolute", "--git-path", "hooks"}})
	if err != nil {
		s.warn("ride-along hooks: " + err.Error())
		return
	}
	dir := strings.TrimSpace(string(out))
	if hp, herr := s.repo.Run(ctx, gitref.Invocation{Args: []string{"config", "--get", "core.hooksPath"}}); herr == nil {
		if p := strings.TrimSpace(string(hp)); p != "" {
			if !filepath.IsAbs(p) {
				p = filepath.Join(s.top, p)
			}
			dir = p
		}
	}
	for _, hook := range []string{hookPostCommit, hookPrePush} {
		if err := ensureHookBlock(dir, hook, rideHookMarker, rideHookLines[hook]); err != nil {
			s.warn("ride-along hooks: " + err.Error())
			return
		}
	}
}

// ensureHookBlock makes the hook script contain exactly one block under
// marker: replaced in place, else put right after the shebang (before an
// `exit 0` a script may end with), and the script executable. Content
// outside the block is never touched.
func ensureHookBlock(dir, hook, marker string, lines []string) error {
	if err := os.MkdirAll(dir, rideHookDirMode); err != nil {
		return err
	}
	path := filepath.Join(dir, hook)
	cur, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	start, end := "# >>> "+marker+" >>>", "# <<< "+marker+" <<<"
	block := start + "\n" + strings.Join(lines, "\n") + "\n" + end + "\n"
	content := string(cur)
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
	if next != content {
		if err := os.WriteFile(path, []byte(next), rideHookMode); err != nil {
			return err
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&rideExecBits == 0 {
		return os.Chmod(path, rideHookMode)
	}
	return nil
}
