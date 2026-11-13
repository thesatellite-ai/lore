package wire

// beforelore.go — D-8: a checkout at a commit from before lore was added to
// the repo (a bisect step, an old release, `git worktree add --detach`)
// has no .lore/, yet the repo is a lore project: a branch carries .lore/
// and refs/lore/live carries its live rows. "lore isn't set up, run lore
// init" is wrong there (following it makes a second, empty project), so
// the refusals name the branch to switch to instead. Every check only
// reads git refs, so the checkout stays untouched.

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// beforeLoreMaxBranches caps the local branches probed for .lore/ (one
// `git cat-file` each): a repo with hundreds of branches still answers fast.
const beforeLoreMaxBranches = 20

// beforeLore is what the repo at top says about lore in other commits.
type beforeLore struct {
	// Branch is a branch whose tip holds .lore/ ("" when none was found).
	Branch string
	// LiveRef is the live ref present in the repo ("" when none).
	LiveRef string
}

// loreInOtherCommit reports whether the git repo at top is a lore project
// in another commit than the checked-out one: a branch (origin's default
// first, then the local ones) whose tip holds .lore/, or a live ref.
func loreInOtherCommit(ctx context.Context, top string) (beforeLore, bool) {
	var b beforeLore
	for _, ref := range []string{liveRefLocal, liveRefTracking} {
		if _, err := (gitRun{dir: top, args: []string{"rev-parse", "--verify", "-q", ref}}).output(ctx); err == nil {
			b.LiveRef = ref
			break
		}
	}
	var cands []string
	if out, err := (gitRun{dir: top, args: []string{"symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"}}).output(ctx); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			cands = append(cands, s)
		}
	}
	if out, err := (gitRun{dir: top, args: []string{"for-each-ref", "--count=" + strconv.Itoa(beforeLoreMaxBranches), "--sort=-committerdate", "--format=%(refname:short)", "refs/heads"}}).output(ctx); err == nil {
		cands = append(cands, strings.Fields(string(out))...)
	}
	for _, c := range cands {
		if _, err := (gitRun{dir: top, args: []string{"cat-file", "-e", c + ":" + loreFolderName}}).output(ctx); err == nil {
			b.Branch = strings.TrimPrefix(c, "origin/")
			break
		}
	}
	return b, b.Branch != "" || b.LiveRef != ""
}

// beforeLoreErr is the refusal for a checkout of a lore repo at a commit
// without .lore/ (code: the refusal it replaces), nil when the repo at top
// is no lore project in any other commit either.
func beforeLoreErr(ctx context.Context, top string, code errs.Code) error {
	if sparseHidesLore(ctx, top) {
		// D-10: the commit holds .lore/, a sparse cone left it out.
		return errs.New(code, "lore is set up in "+top+", but this sparse checkout leaves .lore/ out of the work tree; lore reads and writes nothing here").
			WithHint("add it to the sparse set: git sparse-checkout add " + loreFolderName + " (`lore init` would make a second project)")
	}
	b, ok := loreInOtherCommit(ctx, top)
	if !ok {
		return nil
	}
	where := "its live rows are in " + b.LiveRef
	hint := "switch to a commit that has .lore/ (for example the default branch) to use lore here; `lore init` would make a second project"
	if b.Branch != "" {
		where = "branch " + b.Branch + " carries its lore project"
		hint = "switch to a commit that has .lore/ (git switch " + b.Branch + ") to use lore here; `lore init` would make a second project"
	}
	return errs.New(code, "this checkout of "+top+" is at a commit from before lore was added to the repo ("+where+"); lore reads and writes nothing here").
		WithHint(hint)
}

// sparseHidesLore reports whether HEAD's tree holds .lore/ while the work
// tree at top does not have it: a sparse checkout whose set excludes it.
func sparseHidesLore(ctx context.Context, top string) bool {
	if loreFolderAt(top) {
		return false
	}
	_, err := (gitRun{dir: top, args: []string{"cat-file", "-e", "HEAD:" + loreFolderName}}).output(ctx)
	return err == nil
}

// orBeforeLore is beforeLoreErr for top when it applies, else err.
func orBeforeLore(ctx context.Context, top string, code errs.Code, err error) error {
	if e := beforeLoreErr(ctx, top, code); e != nil {
		return e
	}
	return err
}

// checkoutWithoutLoreErr is Project's "no lore project" for a checkout of a
// lore repo that lacks .lore/ here (beforeLoreErr), keeping noLoreProject's
// words so callers that skip a project-less repo still match it; nil when
// the command's folder holds .lore/ or the repo has no lore anywhere.
func (a *App) checkoutWithoutLoreErr(ctx context.Context) error {
	top := gitTopLevel(ctx, a.projectDir())
	if top == "" || loreFolderAt(a.loreRootFrom(top)) {
		return nil
	}
	var e *errs.Error
	if be := beforeLoreErr(ctx, top, errs.NotFound); be != nil && errors.As(be, &e) {
		return errs.New(errs.NotFound, "wire: "+noLoreProject+" in this checkout: "+e.Message).WithHint(e.Hint)
	}
	return nil
}
