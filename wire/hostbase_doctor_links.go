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
	"os"
	"path/filepath"
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

// link is one commit link the check reads.
type link struct{ id, entity, sha string }

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
	var off []string // the off-branch commits, each asked about once
	asked := map[string]bool{}
	for _, l := range links {
		if !onBranch[l.sha] && !asked[l.sha] {
			asked[l.sha] = true
			off = append(off, l.sha)
		}
	}
	have := commitsPresent(ctx, dir, off)
	var stale, gone []link
	rewritten := rewriteMap(ctx, dir)
	for _, l := range links {
		switch {
		case onBranch[l.sha]:
		case have[l.sha]:
			stale = append(stale, l)
		case rewritten != nil:
			gone = append(gone, l) // devco Q-9: the rewrite dropped the commit itself
		}
	}
	if len(gone) > 0 {
		warn(goneLinksLine(gone, rewritten))
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

// filterRepoMap is the old→new commit map git filter-repo leaves in the
// git dir ("old new" per line, a header first); bfgMapGlob is BFG's.
const (
	filterRepoMap = "filter-repo/commit-map"
	bfgMapGlob    = ".bfg-report/*/*/object-id-map.old-new.txt"
)

// rewriteMap is the old→new commit map a history rewrite tool left in
// this clone (git filter-repo's commit-map, BFG's object-id map), nil
// when none is there. A rewrite that also dropped the old objects leaves
// a link's commit absent, which a link to a commit this clone never had
// (a teammate's, not fetched yet) looks like too; the map tells them
// apart, and gives the pair to remap. A new sha of all zeros (a commit
// the rewrite removed) maps to "".
func rewriteMap(ctx context.Context, dir string) map[string]string {
	gd, err := gitRun{dir: dir, args: []string{"rev-parse", "--absolute-git-dir"}}.output(ctx)
	if err != nil {
		return nil
	}
	gitDir := strings.TrimSpace(string(gd))
	files := []string{filepath.Join(gitDir, filepath.FromSlash(filterRepoMap))}
	if bfg, gerr := filepath.Glob(filepath.Join(filepath.Dir(gitDir), filepath.FromSlash(bfgMapGlob))); gerr == nil {
		files = append(files, bfg...)
	}
	var m map[string]string
	for _, f := range files {
		b, rerr := os.ReadFile(filepath.Clean(f))
		if rerr != nil {
			continue
		}
		if m == nil {
			m = map[string]string{}
		}
		for line := range strings.SplitSeq(string(b), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 || len(fields[0]) != fullSHALen {
				continue // the "old new" header, a blank line
			}
			n := fields[1]
			if strings.Trim(n, "0") == "" {
				n = ""
			}
			m[strings.ToLower(fields[0])] = strings.ToLower(n)
		}
	}
	return m
}

// goneLinksLine is doctor's warning for links whose commit a history
// rewrite dropped from this clone: each link with its ready remap pair
// when the rewrite map has the new commit.
func goneLinksLine(gone []link, rewritten map[string]string) string {
	slices.SortFunc(gone, func(x, y link) int { return strings.Compare(x.id, y.id) })
	names := make([]string, 0, staleLinksShown)
	var pairs []string
	for i, l := range gone {
		if i == staleLinksShown {
			names = append(names, "and "+strconv.Itoa(len(gone)-staleLinksShown)+" more")
			break
		}
		names = append(names, l.id+" ("+l.entity+" → "+shortCommit(l.sha)+")")
		if n := rewritten[l.sha]; n != "" && !slices.Contains(pairs, l.sha+"="+n) {
			pairs = append(pairs, l.sha+"="+n)
		}
	}
	next := "move one to the rewritten commit: lore link remap --pair <old>=<new>"
	if len(pairs) > 0 {
		next = "move them to the rewritten commits: lore link remap --pair " + strings.Join(pairs, " --pair ")
	}
	return "commit_links: " + strconv.Itoa(len(gone)) + " link(s) name a commit that no longer exists in this repository (a history rewrite such as git filter-repo dropped it): " +
		strings.Join(names, ", ") + "; " + next + ", or remove one: lore link remove --id <cml_…> --yes"
}

// commitsPresent reports which of shas name a commit this repository
// holds, with one `git cat-file --batch-check` (D-110: one `cat-file -e`
// per link was 177 git processes of a doctor run in a 5,500-row repo, and
// a process start is slow on macOS). A failed run reports none present,
// as a failed `cat-file -e` did.
func commitsPresent(ctx context.Context, dir string, shas []string) map[string]bool {
	have := map[string]bool{}
	if len(shas) == 0 {
		return have
	}
	var in strings.Builder
	for _, sha := range shas {
		in.WriteString(sha + "^{commit}\n")
	}
	out, err := gitRun{dir: dir, args: []string{"cat-file", "--batch-check=%(objectname) %(objecttype)"}, stdin: []byte(in.String())}.output(ctx)
	if err != nil {
		return have
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if sha, typ, ok := strings.Cut(strings.TrimSpace(line), " "); ok && typ == "commit" {
			have[sha] = true
		}
	}
	return have
}
