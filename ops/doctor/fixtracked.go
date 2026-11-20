package doctor

// fixtracked.go — the tracked files a fix run changed (devco:D-744).
//
// Why: each fix step named its own files to commit (the migrate step's
// .lore/data, the git wiring step's .gitattributes / .gitignore), but other
// steps and repairs change tracked files too: the agent hooks step moves
// lore's hooks out of a committed .claude/settings.json, the workflow pin
// repair rewrites .github/workflows/lore-sync-merge.yml, render refreshes a
// committed LORE.md. Those showed up in `git status` with no line saying
// so. Instead of every step predicting its writes, the run measures them:
//
//	fixProject ─▶ TrackedChanges (before) ─▶ every step and repair
//	           ─▶ TrackedChanges (after)  ─▶ paths whose state moved
//	           ─▶ minus the paths a step's Next already names
//	           ─▶ step "tracked files" with one Next line:
//	              commit these: <paths>: git add -- <paths> && git commit -m "lore: fix"
//
// A file the person had already changed before the run is listed only
// when the run changed it again; a file the run left alone never is.

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// TrackedFilesEnv is the optional upgrade of Env that reads which tracked
// files of the repo differ from HEAD (*wire.App implements it).
type TrackedFilesEnv interface {
	// TrackedChanges returns, for every tracked path (slash separated,
	// relative to the work tree top) that differs from HEAD in the index or
	// the work tree, a fingerprint of its state now. ok is false outside a
	// git repo or when git cannot say; the run then names nothing extra.
	TrackedChanges(ctx context.Context) (changed map[string]string, ok bool)
}

// StepTrackedFiles is the step naming the tracked files the run changed
// that no other step's Next names.
const StepTrackedFiles = "tracked files"

// trackedCommitF is the tracked files step's Next: the files and the
// command that commits them (paths quoted for a shell when needed).
const trackedCommitF = "tracked file(s) this run changed wait for your commit; commit these: %s: git add -- %s && git commit -m \"lore: fix\""

// trackedSnapshot is the state before the run (nil env or !ok: off).
type trackedSnapshot struct {
	env    TrackedFilesEnv
	before map[string]string
}

// snapshotTracked reads the state before the run; off when env cannot.
func snapshotTracked(ctx context.Context, env Env) trackedSnapshot {
	te, ok := env.(TrackedFilesEnv)
	if !ok {
		return trackedSnapshot{}
	}
	before, ok := te.TrackedChanges(ctx)
	if !ok {
		return trackedSnapshot{}
	}
	return trackedSnapshot{env: te, before: before}
}

// withTracked appends the tracked files step to steps when the run changed
// tracked files no step's Next names.
func (s trackedSnapshot) withTracked(ctx context.Context, steps []FixStep) []FixStep {
	if s.env == nil {
		return steps
	}
	after, ok := s.env.TrackedChanges(ctx)
	if !ok {
		return steps
	}
	if st, ok := trackedStep(s.before, after, steps); ok {
		return append(steps, st)
	}
	return steps
}

// trackedStep is the step for the paths whose state moved between before
// and after and that no step's Next names; ok is false when none.
func trackedStep(before, after map[string]string, steps []FixStep) (FixStep, bool) {
	named := NextLines(steps)
	var paths []string
	for p, fp := range after {
		if prev, seen := before[p]; seen && prev == fp {
			continue
		}
		if nextNames(named, p) {
			continue
		}
		paths = append(paths, p)
	}
	if len(paths) == 0 {
		return FixStep{}, false
	}
	slices.Sort(paths)
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = shellWord(p)
	}
	return FixStep{
		Step:    StepTrackedFiles,
		Result:  fmt.Sprintf("changed %d tracked file(s): %s", len(paths), strings.Join(paths, ", ")),
		Changed: true,
		Next:    fmt.Sprintf(trackedCommitF, strings.Join(paths, " "), strings.Join(quoted, " ")),
	}, true
}

// nextNames reports whether one of the Next lines already names path, or a
// folder holding it (the migrate step's `git add .lore/data`).
func nextNames(lines []string, path string) bool {
	for _, l := range lines {
		for _, w := range strings.Fields(l) {
			w = strings.Trim(w, `"',;:()`)
			if w == "" {
				continue
			}
			if w == path {
				return true
			}
			// a folder only when the word looks like a path ("git add"
			// never covers a folder named git)
			if (strings.Contains(w, "/") || strings.HasPrefix(w, ".")) && strings.HasPrefix(path, strings.TrimSuffix(w, "/")+"/") {
				return true
			}
		}
	}
	return false
}

// shellWord is p as one shell word: as it is when it holds only safe
// characters, single-quoted otherwise.
func shellWord(p string) string {
	safe := p != "" && strings.IndexFunc(p, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-/+@=", r))
	}) < 0
	if safe {
		return p
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}
