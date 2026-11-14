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
	if e := loreGoneErr(ctx, top, code); e != nil {
		return e
	}
	b, ok := loreInOtherCommit(ctx, top)
	if !ok {
		return nil
	}
	if e := loreDeletedErr(ctx, top, code); e != nil {
		return e // asked only of a repo with lore elsewhere: it walks the history
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

// loreGoneErr is the refusal for a repo whose .lore/ is gone from the work
// tree although HEAD has it (nil when that is not the case): left out by a
// sparse cone (D-10), or deleted from the work tree (H-1: every command
// blamed a sparse checkout that was not there and hinted git
// sparse-checkout add, the wrong fix).
func loreGoneErr(ctx context.Context, top string, code errs.Code) error {
	if loreFolderAt(top) {
		return nil
	}
	if _, err := (gitRun{dir: top, args: []string{"cat-file", "-e", "HEAD:" + loreFolderName}}).output(ctx); err == nil {
		if sparseCheckoutOn(ctx, top) {
			return errs.New(code, "lore is set up in "+top+", but this sparse checkout leaves .lore/ out of the work tree; lore reads and writes nothing here").
				WithHint("add it to the sparse set: git sparse-checkout add " + loreFolderName + " (`lore init` would make a second project)")
		}
		n := "its files are"
		// against HEAD: a deletion already staged (git add -A) counts too
		if out, lerr := (gitRun{dir: top, args: []string{"diff", "--name-only", "--diff-filter=D", "HEAD", "--", loreFolderName}}).output(ctx); lerr == nil {
			n = strconv.Itoa(len(strings.Fields(string(out)))) + " tracked files are"
		}
		return errs.New(code, "the .lore folder of "+top+" is deleted from the work tree ("+n+" deleted, git status lists them); lore reads and writes nothing here until it is back").
			WithHint("put it back: git -C " + top + " checkout HEAD -- " + loreFolderName + " (`lore init` would make a second project)")
	}
	return nil
}

// loreDeletedErr is the refusal for a branch where a commit deleted the
// project's .lore/ (H-6: lore fix then offered lore init, a second
// project), nil when no commit on HEAD's history did.
func loreDeletedErr(ctx context.Context, top string, code errs.Code) error {
	out, err := (gitRun{dir: top, args: []string{"log", "-1", "--format=%h", "--diff-filter=D", "HEAD", "--", loreFolderName + "/data/_meta.json"}}).output(ctx)
	sha := strings.TrimSpace(string(out))
	if err != nil || sha == "" {
		return nil
	}
	return errs.New(code, "commit "+sha+" on this branch deleted the .lore folder of "+top+" (every rule, memory and task in it); lore reads and writes nothing here").
		WithHint("bring it back in a new commit: git -C " + top + " checkout " + sha + "~1 -- " + loreFolderName + " && git -C " + top + " commit -m \"Restore lore notes\" (`lore init` would make a second project)")
}

// loreDeletedHere reports whether the project's .lore/ at top was deleted
// (from the work tree, or by a commit on this branch), not left out by a
// sparse cone or never there: doctor counts it as an error.
func loreDeletedHere(ctx context.Context, top string) bool {
	if loreFolderAt(top) {
		return false
	}
	if _, err := (gitRun{dir: top, args: []string{"cat-file", "-e", "HEAD:" + loreFolderName}}).output(ctx); err == nil {
		return !sparseCheckoutOn(ctx, top)
	}
	if _, ok := loreInOtherCommit(ctx, top); !ok {
		return false
	}
	return loreDeletedErr(ctx, top, errs.NotAStore) != nil
}

// sparseCheckoutOn reports whether the checkout at top is sparse
// (core.sparseCheckout): only then can a sparse set leave .lore/ out.
func sparseCheckoutOn(ctx context.Context, top string) bool {
	out, err := (gitRun{dir: top, args: []string{"config", "--bool", "core.sparseCheckout"}}).output(ctx)
	return err == nil && strings.TrimSpace(string(out)) == "true"
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
