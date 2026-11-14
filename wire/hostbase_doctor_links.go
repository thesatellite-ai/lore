package wire

// hostbase_doctor_links.go — the stale commit link check of `lore doctor`
// (H-8): a link names a commit by sha, and a rebase or a force-push can
// leave that commit on no branch while the link still answers for it.
//
//	links of this project (every attached store) ─► distinct full shas
//	  ─► git rev-list --branches --remotes (what the branches hold)
//	  ─► a sha no branch holds whose commit this clone has (cat-file -e):
//	     stale (a sha this clone never had, a commit of another clone or a
//	     shallow history, says nothing)
//	  ─► one warning naming the links and both ways out

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/schema"
)

// Commit link fields the check reads.
const (
	linkSHAField     = "sha"
	linkEntityField  = schema.FieldEntityID
	linkProjectField = "project_id"
	// fullSHALen is a sha-1 commit id's hex length; a link's short sha (a
	// commit git could not resolve when the link was made) is skipped.
	fullSHALen = 40
	// staleLinksShown caps the links one warning names.
	staleLinksShown = 5
)

// doctorStaleLinks warns about commit links whose commit is on no branch
// of this clone (H-8). Outside a git checkout, or when git fails, it says
// nothing.
func (a *App) doctorStaleLinks(ctx context.Context, warn func(string)) {
	dir := a.opts.RepoDir
	if dir == "" {
		return
	}
	project, err := a.Project(ctx)
	if err != nil {
		return
	}
	type link struct{ id, entity, sha string }
	var links []link
	for _, src := range engine.ReadStoresOf(a) {
		rows, lerr := a.List(ctx, src, schema.TableCommitLinks)
		if lerr != nil {
			return
		}
		for _, r := range rows {
			sha, ent, proj := r.Fields[linkSHAField], r.Fields[linkEntityField], r.Fields[linkProjectField]
			if r.Deleted || proj.String() != project.String() || len(sha.String()) != fullSHALen {
				continue
			}
			links = append(links, link{id: r.ID.String(), entity: ent.String(), sha: sha.String()})
		}
	}
	if len(links) == 0 {
		return
	}
	out, err := gitRun{dir: dir, args: []string{"rev-list", "--branches", "--remotes"}}.output(ctx)
	if err != nil {
		return
	}
	onBranch := map[string]bool{}
	for l := range strings.SplitSeq(string(out), "\n") {
		onBranch[strings.TrimSpace(l)] = true
	}
	have := map[string]bool{}
	var stale []link
	for _, l := range links {
		if onBranch[l.sha] {
			continue
		}
		known, seen := have[l.sha]
		if !seen {
			_, cerr := gitRun{dir: dir, args: []string{"cat-file", "-e", l.sha + "^{commit}"}}.output(ctx)
			known = cerr == nil
			have[l.sha] = known
		}
		if known {
			stale = append(stale, l)
		}
	}
	if len(stale) == 0 {
		return
	}
	slices.SortFunc(stale, func(x, y link) int { return strings.Compare(x.id, y.id) })
	names := make([]string, 0, staleLinksShown)
	for i, l := range stale {
		if i == staleLinksShown {
			names = append(names, "and "+strconv.Itoa(len(stale)-staleLinksShown)+" more")
			break
		}
		names = append(names, l.id+" ("+l.entity+" → "+shortCommit(l.sha)+")")
	}
	warn("commit_links: " + strconv.Itoa(len(stale)) + " link(s) name a commit that is on no branch (rewritten by a rebase or an amend, or dropped by a force-push): " +
		strings.Join(names, ", ") + "; move one to the rewritten commit: lore link remap --pair <old>=<new>, or remove it: lore link remove --id <cml_…> --yes")
}
