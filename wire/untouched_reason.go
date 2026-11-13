package wire

// untouched_reason.go — G-3/G-4, S-2 fallout: a write in a git repo with no
// .lore/ refuses before anything is made there (WithNoSetUp, open's
// noProjectErr). Before S-2 such a write went on to Project, whose
// E_NOT_FOUND said WHY the repo has no project when lore knows: its link's
// store was dropped from the org file (L-7), or another checkout of the
// same repo is a lore project (a worktree of a commit before lore init).
// Those answers are kept: refineNoProject swaps S-2's generic line for the
// specific one. Every check here only reads (git, the lore home's config
// and clones), so the repo still stays untouched.

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// refineNoProject is err, or the specific refusal for the repo when err is
// S-2's refusal of a write in a git repo with no .lore/ and lore knows why
// the repo has no project. Called outside a.mu (staleLinkOrg asks the
// host's Org hook).
func (a *App) refineNoProject(ctx context.Context, err error) error {
	if err == nil || !errs.Has(err, errs.NotAStore) {
		return err
	}
	if r := noCreateOf(ctx); r == nil || !r.write {
		return err // a read keeps its own line (doctor_nocreate.go)
	}
	top := gitTopLevel(ctx, a.projectDir())
	if top == "" || loreFolderAt(a.loreRootFrom(top)) {
		return err // not S-2's refusal: some other E_NOT_A_STORE
	}
	if sl, ok := a.findStaleLink(ctx); ok {
		return staleLinkErr(sl, a.staleLinkOrg(ctx))
	}
	if e := beforeLoreErr(ctx, top, errs.NotFound); e != nil {
		return e // D-8/D-10: the repo is a lore project in another commit
	}
	if loreRanInRepo(ctx, top) {
		return noLoreProjectErr()
	}
	return err
}

// staleLinkErr is L-7's answer: the repo's remote was linked in a store
// that no org entry on this machine lists any more (org names the entry
// that dropped it, "" when none does).
func staleLinkErr(sl staleLink, org string) error {
	who := "no org entry on this machine lists it any more"
	if org != "" {
		who = "org " + org + "'s entry no longer lists it"
	}
	return errs.New(errs.NotFound, "wire: "+noLoreProject+": its remote "+sl.Remote+" was linked in store "+sl.StoreURL+" (folder "+sl.Folder+"), and "+who).
		WithHint("re-add that store to the org's lore-org.yaml and run `lore org refresh`, or run `lore init` for a project of this repo's own")
}

// noLoreProjectErr is Project's answer for a repository with no project.
func noLoreProjectErr() error {
	return errs.New(errs.NotFound, "wire: "+noLoreProject).
		WithHint("run `lore init` in the repository first (or `lore sync`, which writes the project's row file to .lore/data)")
}

// loreRanInRepo reports whether lore already keeps state for the git repo
// top is a checkout of (<git common dir>/lore): another checkout of it,
// such as the main one of a worktree, is a lore project, so this checkout
// is one of a lore repo that has no project in this commit, not a repo
// lore never saw. A refused command removes the state folder it made
// (host/repo_setup.go), so one refusal does not count.
func loreRanInRepo(ctx context.Context, top string) bool {
	out, err := gitRun{dir: top, args: []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}}.output(ctx)
	if err != nil {
		return false
	}
	fi, err := os.Stat(filepath.Join(strings.TrimSpace(string(out)), signalsStateDir))
	return err == nil && fi.IsDir()
}
