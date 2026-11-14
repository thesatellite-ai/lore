package host

// leakguard.go — the leak guard of a clone (H-18, H-19): lore's blocks in
// the commit-msg, pre-commit and pre-push hooks of the folder git runs
// hooks from. They go missing without lore knowing: removed by hand, or a
// hook manager (husky's core.hooksPath, the pre-commit framework's script)
// taking the hooks over. Then a commit holding a secret goes through with
// no word, so:
//
//	lore doctor ─▶ blocks missing in a clone lore wired before? ─▶ the
//	   first warning: what is not checked any more and `lore fix`
//	next lore command (read or write) ─▶ same look ─▶ one line, then a
//	   scan of the commits no remote has (the pre-push check's scan):
//	   a secret in one of them is named before it is pushed
//
// A hooks folder inside the work tree (husky's .husky) is committed with
// the repo: a write never creates a new hook file there (a new file
// reaches every teammate); `lore fix`, which asks first, does and says so.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/wire"
)

// guardHooks are the hooks whose lore block checks a commit or a push, and
// what goes unchecked without it, in the order the lines name them.
var guardHooks = []struct{ hook, unchecked string }{
	{hookCommitMsg, "commits are not checked for secrets"},
	{hookPrePush, "pushes are not checked for secrets"},
	{hookPreCommit, "broken lore row files are not refused on commit"},
}

// guardMarkers is the marker each guard hook's block carries: the project
// blocks start with gitBlockMarker (a sub-folder project adds ":<rel>"),
// the pre-push block is the repo-wide ride block.
var guardMarkers = map[string]string{
	hookCommitMsg: gitBlockMarker,
	hookPreCommit: gitBlockMarker,
	hookPrePush:   rideHookMarker,
}

// missingGuardHooks lists the guard hooks of dir that would not run lore's
// block: no file, no block, or a file git cannot run (not executable).
func missingGuardHooks(dir string) []string {
	var out []string
	for _, g := range guardHooks {
		p := filepath.Join(dir, g.hook)
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm()&rideExecBits == 0 {
			out = append(out, g.hook)
			continue
		}
		b, err := os.ReadFile(filepath.Clean(p))
		if err != nil || !strings.Contains(string(b), guardMarkers[g.hook]) {
			out = append(out, g.hook)
		}
	}
	return out
}

// guardOffText is the sentence for missing guard hooks of dir: which
// blocks are gone, where (with core.hooksPath when a setting moved the
// folder) and what goes unchecked.
func (s *liveSession) guardOffText(ctx context.Context, dir string, missing []string) string {
	gone := map[string]bool{}
	for _, m := range missing {
		gone[m] = true
	}
	var what []string
	for _, g := range guardHooks {
		if gone[g.hook] {
			what = append(what, g.unchecked)
		}
	}
	where := dir
	if out, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"config", "--get", "core.hooksPath"}}); err == nil && strings.TrimSpace(string(out)) != "" {
		where += " (core.hooksPath=" + strings.TrimSpace(string(out)) + ")"
	}
	head := "leak guard off in this clone"
	if !gone[hookCommitMsg] && !gone[hookPrePush] {
		head = "lore's commit check is off in this clone"
	}
	return head + ": lore's hook blocks are missing in " + where + " (" + strings.Join(missing, ", ") + "), so " + strings.Join(what, " and ")
}

// hooksInWorkTree reports whether dir is inside the work tree and outside
// the git folder: a hooks folder committed with the repo (husky's .husky).
func (s *liveSession) hooksInWorkTree(dir string) bool {
	rel, err := filepath.Rel(s.top, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	first := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
	return first != ".git"
}

// hookWrite is one lore block a command would put into a hook file.
type hookWrite struct {
	name, marker string
	lines        []string
}

// teamHookWrites splits writes into the hook files of dir that are the
// team's (H-19, stories H-1): dir is a hooks folder in the work tree that
// the repo commits (git tracks a file in it), and writing lore's block
// would create a file there git would not ignore (created) or change the
// bytes of one already there (changed). Either reaches every teammate
// with the next commit, so only an install step that asked first writes
// them. A folder git ignores (husky 9's .husky/_), one holding no tracked
// file (a person's own, not shared), or a file whose block is already
// current lists nothing.
func (s *liveSession) teamHookWrites(ctx context.Context, dir string, writes []hookWrite) (created, changed []string) {
	if !s.hooksInWorkTree(dir) {
		return nil, nil
	}
	if out, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"ls-files", "--", dir}}); err != nil || strings.TrimSpace(string(out)) == "" {
		return nil, nil // nothing of the folder is committed: not the team's
	}
	for _, w := range writes {
		p := filepath.Join(dir, w.name)
		cur, err := os.ReadFile(filepath.Clean(p))
		exists := err == nil
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			continue // unreadable: ensureHookBlock reports it
		}
		if exists && hookBlockNext(string(cur), w.marker, w.lines) == string(cur) {
			continue // lore's block is there as it would write it
		}
		if _, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"check-ignore", "-q", "--no-index", "--", p}}); err == nil {
			continue // ignored: never part of the tree
		}
		if exists {
			changed = append(changed, w.name)
		} else {
			created = append(created, w.name)
		}
	}
	sort.Strings(created)
	sort.Strings(changed)
	return created, changed
}

// teamHookNames joins what teamHookWrites found for the notices: "create
// a, b" and/or "add its block to c".
func teamHookNames(created, changed []string) string {
	var parts []string
	if len(created) > 0 {
		parts = append(parts, "create "+strings.Join(created, ", "))
	}
	if len(changed) > 0 {
		parts = append(parts, "add its block to "+strings.Join(changed, ", "))
	}
	return strings.Join(parts, " or ")
}

// trackedHooksSkipped is a write's line when it left hook files of a
// tracked hooks folder alone (H-19, stories H-1): "%s" is teamHookNames.
const trackedHooksSkipped = "lore did not %s in %s: that folder is in the work tree and committed with the repo, so a new or changed file there reaches every teammate; `lore fix` writes them after asking (then commit them), or add lore's lines to your own hook files"

// trackedHooksCreated follows the set-up line of `lore fix` when it
// created or changed files in a tracked hooks folder (H-19).
const trackedHooksCreated = "; new or changed files in %s, a folder committed with the repo: %s (commit them to share lore's checks with the team, they run lore only where it is installed; or undo them to keep them out)"

// trackedNoticeSuffix keys the read-notice memory of trackedHooksSkipped.
const trackedNoticeSuffix = ".tracked"

// guardNoticeSuffix keys the read-notice memory of the leak-guard line.
const guardNoticeSuffix = ".guard"

// maxUnguardedCommits caps the commits the guard-off scan reads: past it,
// the line says the pre-push check covers them once restored.
const maxUnguardedCommits = 200

// sayGuardOff prints, once per state of the wired files and HEAD, the
// leak-guard line for missing (fixed: the blocks are back now) and the
// result of a secret scan of the commits no remote has: a commit made
// while the guard was off is named before it is pushed.
func (s *liveSession) sayGuardOff(ctx context.Context, dir string, missing []string, fixed bool, fp, readKey string) {
	if r := runFrom(ctx); r != nil && r.doctor && !fixed {
		return // doctor lists it first in its report (doctorGuard)
	}
	key := fp + "@" + s.headOID(ctx)
	if fixed {
		key += "+restored" // "restored now" is news even right after a read said "off"
	}
	if wire.ReadNoticeSaid(s.h.cfg.Home, readKey+guardNoticeSuffix, key) {
		return
	}
	line := s.guardOffText(ctx, dir, missing)
	if fixed {
		line += "; restored now (commits made meanwhile were not checked)"
	} else {
		line += "; `lore fix` restores them"
	}
	s.cmdNotice(ctx, line)
	if found := s.unguardedLeaks(ctx); found != "" {
		s.cmdNotice(ctx, found)
	}
	wire.MarkReadNotice(s.h.cfg.Home, readKey+guardNoticeSuffix, key)
}

// unguardedLeaks scans the commits of HEAD that no remote-tracking ref
// has, as the pre-push check would, and returns the warning naming what
// it found ("" when clean, nothing to scan, or no check is wired).
func (s *liveSession) unguardedLeaks(ctx context.Context) string {
	if s.h.cfg.LeakCheck == nil {
		return ""
	}
	head := s.headOID(ctx)
	if head == "" {
		return ""
	}
	remote := ""
	if s.ref != nil {
		remote = s.ref.RemoteName
	}
	notRemote := "--remotes"
	if remote != "" {
		notRemote += "=" + remote
	}
	out, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"rev-list", "--count", head, "--not", notRemote}})
	if err != nil {
		return ""
	}
	n := strings.TrimSpace(string(out))
	if n == "0" {
		return ""
	}
	c, aerr := strconv.Atoi(n)
	if aerr != nil {
		return ""
	}
	if c > maxUnguardedCommits {
		return n + " commit(s) here are on no remote and were not scanned now; once `lore fix` restores the pre-push block, `git push` checks them"
	}
	updates := []pushUpdate{{localRef: "HEAD", localSHA: head, remoteRef: "HEAD", remoteSHA: strings.Repeat("0", len(head))}}
	docs, err := pushDocs(ctx, s.repo, remote, updates)
	if err != nil || len(docs) == 0 {
		return ""
	}
	cerr := s.h.cfg.LeakCheck(wire.WithPushBases(ctx, pushBases(ctx, s.repo, remote, updates)), wire.LeakWherePush, docs)
	if cerr == nil {
		return ""
	}
	msg := cerr.Error()
	var e *errs.Error
	if errors.As(cerr, &e) {
		msg = e.Message
	}
	// the refusal's own first line speaks of a push; its finding lines
	// (path:line, rule, the `not a secret?` command) are kept
	if _, rest, ok := strings.Cut(msg, "\n"); ok {
		msg = rest
	}
	return "found what looks like a secret in the " + n + " commit(s) here that no remote has yet; rewrite the commit before you push (git commit --amend, or git rebase --interactive): `git push` refuses it once lore's pre-push block is in place:\n" + msg
}

// headOID is the commit HEAD names ("" before the first commit or on any
// git error). gitref's ResolveRef takes full ref names only.
func (s *liveSession) headOID(ctx context.Context) string {
	out, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--verify", "-q", "HEAD^{commit}"}})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
