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
	"errors"
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
	return a.withResourceLink(ctx, a.refineNoProjectBase(ctx, err))
}

// refineNoProjectBase is refineNoProject before withResourceLink.
func (a *App) refineNoProjectBase(ctx context.Context, err error) error {
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
	if e := beforeLoreErr(ctx, top); e != nil {
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
		WithHint("write to an org's store folder with --to <org>/<folder> (`lore store folder list --store <org>` lists the folders), link the repo to one with `lore org link --org <org> --project <folder> --own` (both keep the repo free of lore files), or run `lore init` for a project of this repo's own")
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

// folderPlaceholder is the folder placeholder an own repo's no-project
// hint names when lore does not know the folder (withOwnOrgLink,
// noLoreProjectErr).
const folderPlaceholder = "--project <folder>"

// withResourceLink is err with its hint made exact when a resource of the
// repo's own org names this repo (devco:D-653): a write with no --to in an
// unlinked own repo said "--to <org>/<folder>" and "lore org link --org
// <org> --project <folder> --own" although a read there names the folder
// the resource gives (resourceFolderOf, the project field included). The
// hint then names that folder's --to and the same org link the read
// prints. err is returned as it is when its hint has no folder
// placeholder, the repo is no own repo, or no resource names it.
func (a *App) withResourceLink(ctx context.Context, err error) error {
	var e *errs.Error
	if !errors.As(err, &e) || !strings.Contains(e.Hint, folderPlaceholder) {
		return err
	}
	org := ownOrgOf(ctx, a.opts)
	if org == "" {
		return err
	}
	f, found := a.resourceFolderOf(ctx, org)
	if !found {
		return err
	}
	return replaceErr(err, e, e.WithHint(f.writeHint()))
}

// replaceErr is err with its part old swapped for repl: err itself when it
// is old, or the errors.Join open's fail makes (the refusal joined with
// the close of a half-open state), a join inside it included (devco:D-840:
// a join nested in the join kept the placeholder hint). Any other
// wrapping keeps err as it is.
func replaceErr(err error, old *errs.Error, repl error) error {
	if err == error(old) { //nolint:errorlint // identity, not a match
		return repl
	}
	j, ok := err.(interface{ Unwrap() []error }) //nolint:errorlint // only open's own join
	if !ok {
		return err
	}
	var out []error
	for _, p := range j.Unwrap() {
		out = append(out, replaceErr(p, old, repl))
	}
	return joinFlat(out...)
}

// writeHint is the next step of a write refused in an unlinked own repo
// that resource f names: the folder's --to, the org link the read note
// prints (note), or lore init.
func (f resourceFolder) writeHint() string {
	return "its resource " + f.Resource + " names this repo (" + f.Remote + "): write to that folder with --to " + f.target() +
		", or link the repo to it with `lore org link --org " + f.Org + " --project " + f.Folder + " --own` (both keep the repo free of lore files); lore init sets this repo up instead"
}
