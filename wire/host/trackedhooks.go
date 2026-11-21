package host

// trackedhooks.go — lore never leaves an untracked file in a hooks folder
// the repo commits (devco D-732). Seen in a repo whose core.hooksPath is a
// tracked .githooks: a client-repo link created .githooks/commit-msg, and
// every `git status` listed it (one `git add -A` from reaching the team).
//
//	hooks folder tracked (core.hooksPath inside the work tree, a file of
//	it in git)?
//	  ├─ every write, sync, init, doctor --fix, link: writes nothing there
//	  ├─ lore doctor: warns once and prints the exact lines to paste into
//	  │    the repo's own hook files (pasted as printed, lore sees its
//	  │    blocks there and stops warning)
//	  ├─ lore doctor --fix: removes the files there that git does not track
//	  │    and that hold nothing but lore's blocks (an older lore's); a
//	  │    file with any other content stays
//	  └─ lore fix --tracked-hooks: the person's explicit yes (D-501) still
//	       writes lore's blocks there for the team to commit; the files it
//	       creates are noted in the clone's state, so doctor --fix never
//	       removes what the person just asked for

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/model/fspath"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
)

// trackedAskedFile is the state file (in the clone's lore state folder)
// listing the hook files `lore fix --tracked-hooks` created on the
// person's yes, one name per line.
const trackedAskedFile = "tracked-hooks-asked"

// hooksFolderTrackedAt reports whether dir is a hooks folder inside the
// work tree at top that git tracks a file of. The client guard has no
// session, so it asks through g with top as the repo.
func hooksFolderTrackedAt(ctx context.Context, g gitref.Git, top, dir string) bool {
	if !hooksDirInWorkTree(top, dir) {
		return false
	}
	out, err := g.Run(ctx, gitref.Invocation{Dir: top, Args: []string{"ls-files", "--", dir}})
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// loreOnlyHook reports whether a hook script holds nothing but lore's
// blocks: with every `# >>> lore… >>>` … `# <<< lore… <<<` block taken
// out, only a shebang and blank lines are left. A file with any other
// line is the person's (or a hook manager's) and is never removed.
func loreOnlyHook(content string) bool {
	rest := content
	found := false
	for {
		i := strings.Index(rest, "# >>> "+gitBlockMarker)
		if i < 0 {
			break
		}
		nl := strings.IndexByte(rest[i:], '\n')
		if nl < 0 {
			return false
		}
		head := strings.TrimSuffix(strings.TrimPrefix(rest[i:i+nl], "# >>> "), " >>>")
		end := "# <<< " + head + " <<<"
		j := strings.Index(rest[i:], end)
		if j < 0 {
			return false // an open block: not lore's whole file
		}
		rest = rest[:i] + rest[i+j+len(end):]
		found = true
	}
	if !found {
		return false
	}
	for _, line := range strings.Split(rest, "\n") {
		t := strings.TrimSpace(line)
		if t != "" && !strings.HasPrefix(t, "#!") {
			return false
		}
	}
	return true
}

// pasteBlock is the text to paste into the hook file name of dir for
// lore's block w: the block with its markers, so lore finds it there as
// if it had written it.
func pasteBlock(dir string, w hookWrite) string {
	return "paste into " + filepath.Join(dir, w.name) + " (after its first line; a new file starts with " + rideHookShebang + " and needs chmod +x):\n" +
		strings.TrimSuffix(hookBlockNext(rideHookShebang+"\n", w.marker, w.lines), "\n")[len(rideHookShebang)+1:]
}

// noteTrackedAsked records the hook files `lore fix --tracked-hooks`
// created in dir on the person's yes (best effort: a lost note only
// means doctor --fix may offer to remove them before they are committed).
func (s *liveSession) noteTrackedAsked(names []string) {
	if len(names) == 0 || s.stateDir == "" {
		return
	}
	have := s.trackedAsked()
	for _, n := range names {
		have[n] = true
	}
	out := make([]string, 0, len(have))
	for n := range have {
		out = append(out, n)
	}
	sort.Strings(out)
	if err := os.MkdirAll(s.stateDir, rideHookDirMode); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(s.stateDir, trackedAskedFile), []byte(strings.Join(out, "\n")+"\n"), trackedAskedMode) // best effort, see above
}

// trackedAskedMode is the state file's mode.
const trackedAskedMode = 0o600

// trackedAsked reads that note.
func (s *liveSession) trackedAsked() map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(filepath.Clean(filepath.Join(s.stateDir, trackedAskedFile)))
	if err != nil {
		return out
	}
	for _, n := range strings.Fields(string(b)) {
		out[n] = true
	}
	return out
}

// strayLoreHooks lists the files of the tracked hooks folder dir that an
// older lore left there: a hook name lore writes, untracked, not ignored,
// holding only lore's blocks, and not created on the person's yes.
func (s *liveSession) strayLoreHooks(ctx context.Context, dir string) []string {
	asked := s.trackedAsked()
	names := map[string]bool{hookPostCommit: true, hookPrePush: true}
	for n := range hookBlocks("") {
		names[n] = true
	}
	var out []string
	for n := range names {
		p := filepath.Join(dir, n)
		b, err := os.ReadFile(filepath.Clean(p))
		if err != nil || asked[n] || !loreOnlyHook(string(b)) {
			continue
		}
		if _, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"ls-files", "--error-unmatch", "--", p}}); err == nil {
			continue // tracked: the team's file, whatever it holds
		}
		if _, err := s.repo.Run(ctx, gitref.Invocation{Args: []string{"check-ignore", "-q", "--no-index", "--", p}}); err == nil {
			continue // ignored: never part of the tree
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// doctorTrackedHooks is doctor's part for a hooks folder the repo commits
// (D-732): lore-only untracked files there are removed on --fix (else one
// warning naming them), and the blocks lore would add there come as
// paste lines, once.
func (s *liveSession) doctorTrackedHooks(ctx context.Context, dir string, shared, fix bool, warn, note func(string)) {
	if !s.trackedHooksFolder(ctx, dir) {
		return
	}
	if stray := s.strayLoreHooks(ctx, dir); len(stray) > 0 {
		if !fix {
			warn("untracked file(s) " + strings.Join(stray, ", ") + " in " + dir + " hold only lore's blocks (an older lore wrote them); that folder is committed with the repo, so lore no longer writes there and `git status` lists them: `lore doctor --fix` removes them")
		} else {
			var gone []string
			for _, n := range stray {
				if err := os.Remove(filepath.Join(dir, n)); err != nil {
					warn("could not remove " + filepath.Join(dir, n) + ": " + err.Error())
					continue
				}
				gone = append(gone, n)
			}
			if len(gone) > 0 {
				warn("removed untracked file(s) " + strings.Join(gone, ", ") + " from " + dir + ": they held only lore's blocks, and that folder is committed with the repo")
			}
		}
	}
	if !s.repoMayHoldLoreFiles(ctx, s.top) {
		return // D-500: a private project's blocks never go there
	}
	if text := s.trackedPasteText(ctx, dir, shared); text != "" {
		note(text)
	}
}

// trackedPasteText is the paste lines for every lore block missing from
// the tracked hooks folder dir ("" when none is missing).
func (s *liveSession) trackedPasteText(ctx context.Context, dir string, shared bool) string {
	rel := ""
	if root := liveProjectRoot(s.h.cfg.WorkDir); root != "" {
		if r, inside := fspath.Rel(s.top, root); inside {
			rel = r
		}
	}
	blocks := hookBlocksFor(rel, shared, hookPinFor(ctx, s.top, dir, shared))
	missing := map[string]bool{}
	for _, n := range missingGuardHooks(dir) {
		missing[n] = true
	}
	for _, n := range missingRideHooks(dir) {
		missing[n] = true
	}
	names := make([]string, 0, len(missing))
	for n := range missing {
		if _, ok := blocks[n]; ok {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	parts := []string{"lore writes nothing into " + dir + " (committed with the repo); to run lore's checks from your own hooks, add these lines (or run `" + trackedHooksCommand + "` to have lore add them, then commit them)"}
	for _, n := range names {
		parts = append(parts, pasteBlock(dir, hookWrite{name: n, marker: blocks[n].marker, lines: blocks[n].lines}))
	}
	return strings.Join(parts, "\n")
}

// clientGuardPasteOnDoctor is doctor's line in a client repo with no
// .lore/ whose hooks folder is committed and whose commit-msg holds no
// lore check (D-732): the client guard cannot be installed there, so
// doctor prints the block to paste, the same text the link printed, and
// whoami's guards-off line points here.
func (h *liveHost) clientGuardPasteOnDoctor(ctx context.Context, opName string) {
	if opName != opsdoctor.OpRun || h.cfg.ClientRepo == nil || liveProjectRoot(h.cfg.WorkDir) != "" {
		return
	}
	top, err := h.git().Run(ctx, gitref.Invocation{Dir: h.cfg.WorkDir, Args: []string{"rev-parse", "--show-toplevel"}})
	dir := strings.TrimSpace(string(top))
	if err != nil || dir == "" || !h.cfg.ClientRepo(ctx) {
		return
	}
	hp, err := h.git().Run(ctx, gitref.Invocation{Dir: dir, Args: []string{"config", "--get", "core.hooksPath"}})
	p := filepath.FromSlash(strings.TrimSpace(string(hp)))
	if err != nil || p == "" {
		return
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	if !hooksFolderTrackedAt(ctx, h.git(), dir, p) {
		return
	}
	if b, rerr := os.ReadFile(filepath.Clean(filepath.Join(p, hookCommitMsg))); rerr == nil && strings.Contains(string(b), "# >>> "+gitBlockMarker) {
		return // the lines are pasted already
	}
	b := hookBlocks("")[hookCommitMsg]
	h.line("lore doctor: commits in this client repo are NOT checked for internal notes: git runs hooks from " + p + ", which is committed with the repo, so lore writes nothing there; " +
		pasteBlock(p, hookWrite{name: hookCommitMsg, marker: b.marker, lines: withHookPin(hookPinFor(ctx, dir, p, false), b.lines)}))
}
