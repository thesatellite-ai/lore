// Package machineguard installs lore's client leak guard machine-wide
// (devco H-119, org-setup item 12): an opt-in global core.hooksPath entry
// that points git at a folder of lore's own dispatcher scripts, so a
// clone where lore never ran (a plain `git clone` of a client repo) is
// checked for internal notes on commit and push too. Never on by default:
// only `lore setup guard install` turns it on.
//
//	lore setup guard install
//	   ─▶ <lore home>/git-hooks/<hook> for every hook git runs from a
//	      hooks folder (Hooks): a dispatcher that
//	        1. runs the hook this repo would run without the guard: the
//	           previous global (or system) core.hooksPath's hook when one
//	           was set, else the repo's own .git/hooks/<hook>, with the
//	           same arguments and input, stopping on its failure;
//	        2. commit-msg and pre-push only: lore's leak check, unless the
//	           hook of step 1 already runs lore's block (a lore project or
//	           a linked clone: its blocks carry ChainedMarker)
//	   ─▶ <lore home>/git-hooks/lore-machine-guard.json: the previous
//	      global core.hooksPath (set or not, its exact text)
//	   ─▶ git config --global core.hooksPath <lore home>/git-hooks
//	lore setup guard remove
//	   ─▶ global core.hooksPath back to exactly what it was (set to the
//	      recorded text, or unset when none was set); the folder is removed
//	   ─▶ a global core.hooksPath changed since the install is left alone
//
// Why chaining matters: a global core.hooksPath makes git ignore every
// repo's .git/hooks, so without step 1 the guard would switch off every
// hook a repo (or a hook manager's global folder) already had. lore's own
// wiring knows the folder (Chained): in a repo whose hooks run from it,
// lore writes its blocks where the dispatcher runs them, never into the
// dispatchers.
package machineguard

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/lorehome"
	"github.com/thesatellite-ai/lore/model/errs"
)

const (
	// DirName is the guard's folder under the lore home.
	DirName = "git-hooks"
	// StateName is the file in the folder recording the previous global
	// core.hooksPath, so remove restores it exactly.
	StateName = "lore-machine-guard.json"
	// ScriptMarker is the line every dispatcher carries: a script without
	// it is not lore's (Status reports it as changed).
	ScriptMarker = "# lore-machine-guard"
	// ChainedMarker is the text lore's own hook blocks carry (wire/host's
	// gitBlockMarker; the ride block's marker starts with it): a chained
	// hook holding it already runs lore's check, so the dispatcher does not
	// run it twice.
	ChainedMarker = "lore-sync"
	// HooksPathKey is the git config key the guard sets.
	HooksPathKey = "core.hooksPath"
	// stateVersion is the state file's format version.
	stateVersion = 1
	// scriptMode is a dispatcher's file mode: git runs only executable hooks.
	scriptMode = 0o755
)

// Hook names whose dispatcher adds lore's leak check after the chained hook.
const (
	HookCommitMsg = "commit-msg"
	HookPrePush   = "pre-push"
)

// Hooks are the hooks git runs from a hooks folder that get a dispatcher.
// Left out on purpose: push-to-checkout (its mere presence replaces git's
// own update of a pushed-to work tree), proc-receive (a two-way protocol
// with receive-pack, run only when receive.procReceiveRefs names it) and
// fsmonitor-watchman (git runs it only by the path core.fsmonitor names).
var Hooks = []string{
	"applypatch-msg", "pre-applypatch", "post-applypatch",
	"pre-commit", "pre-merge-commit", "prepare-commit-msg", HookCommitMsg, "post-commit",
	"pre-rebase", "post-checkout", "post-merge", HookPrePush, "pre-auto-gc", "post-rewrite",
	"sendemail-validate", "post-index-change", "reference-transaction",
	"p4-changelist", "p4-prepare-changelist", "p4-post-changelist", "p4-pre-submit",
	"pre-receive", "update", "post-receive", "post-update",
}

// State is the state file: the global core.hooksPath before the install.
type State struct {
	V int `json:"v"`
	// PrevSet is whether a global core.hooksPath was set before.
	PrevSet bool `json:"previous_set"`
	// Prev is its exact text (restored as is).
	Prev string `json:"previous,omitempty"`
	// Chain is the folder whose hooks the dispatchers run first: the
	// previous global (or else system) core.hooksPath, ~ expanded; "" for
	// each repo's own .git/hooks.
	Chain string `json:"chain,omitempty"`
}

// Status is what is on this machine now.
type Status struct {
	// Dir is the guard's folder (<lore home>/git-hooks).
	Dir string `json:"dir"`
	// On: the global core.hooksPath names Dir.
	On bool `json:"on"`
	// Global is the global core.hooksPath now ("" when unset).
	Global string `json:"global,omitempty"`
	// State is the recorded state (zero when no state file).
	State State `json:"state"`
	// HasState: the state file exists.
	HasState bool `json:"has_state"`
	// Broken are the dispatchers missing, changed or not executable.
	Broken []string `json:"broken,omitempty"`
}

// Dir is the guard's folder under the lore home.
func Dir(home string) string { return filepath.Join(home, DirName) }

// Read reports the guard's state on this machine.
func Read(ctx context.Context, g gitref.Git, home string) (Status, error) {
	st := Status{Dir: Dir(home)}
	s, ok, err := readState(st.Dir)
	if err != nil {
		return st, err
	}
	st.State, st.HasState = s, ok
	glob, set, err := configGet(ctx, g, home, "--global")
	if err != nil {
		return st, err
	}
	if set {
		st.Global = glob
		st.On = SameDir(expandHome(glob), st.Dir)
	}
	if st.On {
		st.Broken = broken(st.Dir, st.State.Chain)
	}
	return st, nil
}

// Result is what install or remove did (or would do, dry).
type Result struct {
	Dir    string   `json:"dir"`
	Wrote  []string `json:"wrote,omitempty"`
	Prev   string   `json:"previous,omitempty"`
	PrevOn bool     `json:"previous_set"`
	// Chain is the folder the dispatchers run first ("" = each repo's
	// own .git/hooks).
	Chain string `json:"chain,omitempty"`
	// Already: the guard was on before install (scripts refreshed), or
	// off before remove.
	Already bool `json:"already,omitempty"`
	// Left: remove found a global core.hooksPath changed since install
	// and left it (its value).
	Left   string `json:"left,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// Install turns the guard on (see the package comment). Idempotent: on an
// installed machine it rewrites the dispatchers and keeps the recorded
// previous value.
func Install(ctx context.Context, g gitref.Git, home string, dryRun bool) (Result, error) {
	st, err := Read(ctx, g, home)
	if err != nil {
		return Result{}, err
	}
	state := st.State
	if !st.On {
		state = State{V: stateVersion, PrevSet: st.Global != "", Prev: st.Global}
		chain := expandHome(st.Global)
		if chain == "" {
			sys, sset, serr := configGet(ctx, g, home, "--system")
			if serr != nil {
				return Result{}, serr
			}
			if sset {
				chain = expandHome(sys)
			}
		}
		state.Chain = chain
	}
	res := Result{Dir: st.Dir, Prev: state.Prev, PrevOn: state.PrevSet, Chain: state.Chain, Already: st.On, DryRun: dryRun}
	for _, h := range Hooks {
		res.Wrote = append(res.Wrote, filepath.Join(st.Dir, h))
	}
	if dryRun {
		return res, nil
	}
	if err := os.MkdirAll(st.Dir, 0o750); err != nil {
		return Result{}, errs.Wrap(errs.Internal, "machine guard: create "+st.Dir, err)
	}
	for _, h := range Hooks {
		p := filepath.Join(st.Dir, h)
		if err := os.WriteFile(p, []byte(Script(h, state.Chain)), scriptMode); err != nil {
			return Result{}, errs.Wrap(errs.Internal, "machine guard: write "+p, err)
		}
		if err := os.Chmod(p, scriptMode); err != nil { // an existing file keeps its old mode on write
			return Result{}, errs.Wrap(errs.Internal, "machine guard: chmod "+p, err)
		}
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return Result{}, errs.Wrap(errs.Internal, "machine guard: state", err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir, StateName), append(b, '\n'), 0o600); err != nil {
		return Result{}, errs.Wrap(errs.Internal, "machine guard: write state", err)
	}
	if !st.On {
		if _, err := g.Run(ctx, gitref.Invocation{Dir: home, Args: []string{"config", "--global", HooksPathKey, filepath.ToSlash(st.Dir)}}); err != nil {
			return Result{}, errs.Wrap(errs.Internal, "machine guard: set the global "+HooksPathKey, err)
		}
	}
	return res, nil
}

// Remove turns the guard off: the global core.hooksPath goes back to the
// recorded previous value (or is unset), then the folder is removed. A
// global core.hooksPath that no longer names the folder is left as it is
// (Result.Left); the folder is still removed.
func Remove(ctx context.Context, g gitref.Git, home string, dryRun bool) (Result, error) {
	st, err := Read(ctx, g, home)
	if err != nil {
		return Result{}, err
	}
	res := Result{Dir: st.Dir, Prev: st.State.Prev, PrevOn: st.State.PrevSet, Chain: st.State.Chain, DryRun: dryRun}
	_, dirErr := os.Stat(st.Dir)
	folder := dirErr == nil
	switch {
	case !st.On && !folder:
		res.Already = true
		return res, nil
	case !st.On:
		res.Left = st.Global
	}
	if dryRun {
		return res, nil
	}
	if st.On {
		args := []string{"config", "--global", "--unset-all", HooksPathKey}
		if st.State.PrevSet {
			args = []string{"config", "--global", "--replace-all", HooksPathKey, st.State.Prev}
		}
		if _, err := g.Run(ctx, gitref.Invocation{Dir: home, Args: args}); err != nil {
			return Result{}, errs.Wrap(errs.Internal, "machine guard: restore the global "+HooksPathKey, err)
		}
	}
	if folder {
		if err := os.RemoveAll(st.Dir); err != nil {
			return Result{}, errs.Wrap(errs.Internal, "machine guard: remove "+st.Dir, err)
		}
	}
	return res, nil
}

// Chained is where lore's own blocks go in a repo whose hooks folder is
// dir: when dir is the guard's folder under home, the folder the
// dispatchers run first (the recorded chain, shared by every repo, or ""
// for the repo's own .git/hooks) and ok; otherwise ok is false.
func Chained(dir, home string) (chain string, ok bool) {
	gd := Dir(home)
	if !SameDir(dir, gd) {
		return "", false
	}
	s, _, _ := readState(gd)
	return s.Chain, true
}

// Own maps dir, the hooks folder git runs for the clone at top, through
// the guard: when dir is the guard's folder under home, the folder its
// dispatchers run first, where lore's own blocks belong (never in the
// dispatchers): the recorded chain (a hooks folder every repo shares, so
// shared is true, as before the install) or else the clone's own
// <common git dir>/hooks. ok is false for any other folder (or home "").
func Own(ctx context.Context, g gitref.Git, home, dir, top string) (own string, shared, ok bool) {
	if home == "" {
		return "", false, false
	}
	chain, ok := Chained(dir, home)
	if !ok {
		return "", false, false
	}
	if chain != "" {
		return filepath.Clean(filepath.FromSlash(chain)), true, true
	}
	out, err := g.Run(ctx, gitref.Invocation{Dir: top, Args: []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}})
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return "", false, false
	}
	return filepath.Join(filepath.Clean(filepath.FromSlash(strings.TrimSpace(string(out)))), "hooks"), false, true
}

// OwnHere is Own for the process's lore home (lorehome.Resolve), for the
// callers that hold no lore home of their own.
func OwnHere(ctx context.Context, dir, top string) (string, bool) {
	h, err := lorehome.Resolve()
	if err != nil {
		return "", false
	}
	own, _, ok := Own(ctx, gitref.Exec{}, h.Dir, dir, top)
	return own, ok
}

// SameDir reports whether a and b name one folder (cleaned, links
// followed where they exist).
func SameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = filepath.Clean(filepath.FromSlash(a)), filepath.Clean(filepath.FromSlash(b))
	if a == b {
		return true
	}
	ra, ea := filepath.EvalSymlinks(a)
	rb, eb := filepath.EvalSymlinks(b)
	return ea == nil && eb == nil && ra == rb
}

// Script is hook's dispatcher; chain is the folder whose hook runs first
// ("" = the repo's own hooks folder in its common git dir).
func Script(hook, chain string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString(ScriptMarker + " " + hook + ": written by `lore setup guard install`; `lore setup guard remove` restores your git config. Rewritten on install: do not edit.\n")
	b.WriteString("lore_chain=" + shQuote(filepath.ToSlash(chain)) + "\n")
	b.WriteString(`if [ -n "$lore_chain" ]; then lore_own="$lore_chain/` + hook + `"; else lore_own="$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null)/hooks/` + hook + `"; fi` + "\n")
	switch hook {
	case HookCommitMsg:
		b.WriteString(`if [ -f "$lore_own" ] && [ -x "$lore_own" ]; then "$lore_own" "$@" || exit $?; grep -q '` + ChainedMarker + `' "$lore_own" 2>/dev/null && exit 0; fi` + "\n")
		b.WriteString(`if command -v lore >/dev/null 2>&1; then lore check leaks --staged --commit-msg "$1" || exit $?; fi` + "\n")
	case HookPrePush:
		b.WriteString(`lore_in="$(mktemp "${TMPDIR:-/tmp}/lore-pre-push.XXXXXX")" || exit 1` + "\n")
		b.WriteString(`trap 'rm -f "$lore_in"' EXIT` + "\n")
		b.WriteString(`cat > "$lore_in"` + "\n")
		b.WriteString(`if [ -f "$lore_own" ] && [ -x "$lore_own" ]; then "$lore_own" "$@" < "$lore_in" || exit $?; grep -q '` + ChainedMarker + `' "$lore_own" 2>/dev/null && exit 0; fi` + "\n")
		b.WriteString(`if command -v lore >/dev/null 2>&1; then lore sync hook pre-push "$@" < "$lore_in" || exit $?; fi` + "\n")
	default:
		b.WriteString(`if [ -f "$lore_own" ] && [ -x "$lore_own" ]; then exec "$lore_own" "$@"; fi` + "\n")
	}
	b.WriteString("exit 0\n")
	return b.String()
}

// shQuote quotes s for sh as one word.
func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// broken lists the dispatchers in dir that are missing, not lore's
// current script, or not executable.
func broken(dir, chain string) []string {
	var out []string
	for _, h := range Hooks {
		p := filepath.Join(dir, h)
		b, err := os.ReadFile(filepath.Clean(p))
		info, serr := os.Stat(p)
		if err != nil || serr != nil || string(b) != Script(h, chain) || info.Mode().Perm()&0o111 == 0 {
			out = append(out, h)
		}
	}
	return out
}

// readState reads dir's state file; ok is false when there is none.
func readState(dir string) (State, bool, error) {
	b, err := os.ReadFile(filepath.Clean(filepath.Join(dir, StateName)))
	if errors.Is(err, os.ErrNotExist) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, errs.Wrap(errs.Internal, "machine guard: read state", err)
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, false, errs.Wrap(errs.ConfigInvalid, "machine guard: "+filepath.Join(dir, StateName)+" is not valid JSON", err)
	}
	return s, true, nil
}

// configGet reads core.hooksPath in one git config scope ("--global",
// "--system"): its text and whether it is set. git exits 1 for unset.
func configGet(ctx context.Context, g gitref.Git, dir, scope string) (string, bool, error) {
	if _, err := os.Stat(dir); err != nil {
		dir = os.TempDir() // the lore home may not exist yet
	}
	out, err := g.Run(ctx, gitref.Invocation{Dir: dir, Args: []string{"config", scope, "--get", HooksPathKey}})
	if err != nil {
		if code, ok := gitref.ExitCode(err); ok && code == 1 {
			return "", false, nil
		}
		return "", false, errs.Wrap(errs.Internal, "machine guard: read the "+strings.TrimPrefix(scope, "--")+" "+HooksPathKey, err)
	}
	v := strings.TrimSpace(string(out))
	return v, v != "", nil
}

// expandHome expands a leading ~/ the way git does for core.hooksPath.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
