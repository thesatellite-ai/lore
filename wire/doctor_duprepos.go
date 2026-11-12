package wire

// doctor_duprepos.go — the doctor's duplicate_repos check (B2-7): two
// repos rows for one repository. Before repo ids were derived (MintRepoID),
// a v0.1.17 repo upgraded on two machines before either pushed got one
// random id per machine; after a normal pull both rows sat in
// .lore/data/repos/ and `lore repo list` showed the repo twice.
//
//	repos rows of the repo store, live, not forks or splits
//	   ├► same project and a shared address (origin_url or any
//	   │    confirmed entry of urls, normalised) ─┐
//	   └► same project and the same mount_name  ─┴► one group
//	group of 2+ ─▶ warning naming the ids; fix: lore fix
//	   --fix ─▶ keep = the id _meta.json names, else the smallest id
//	            each other row: its address entries appended to keep's
//	            urls, then engine.MergeRows (rows naming it re-pointed,
//	            the row soft-deleted "merged into <keep>", one bat_ batch)
//	            _meta.json: repo_id = keep, the folded ids added to
//	            repo_id_aliases (`lore repo merge-ids` rewrites their _o.repo)
//
// Rows' `_o.repo` keep the id they were made under (PROV10: provenance is
// where the row was made, never rewritten).

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// DoctorCheckDuplicateRepos is the check's JSON name.
const DoctorCheckDuplicateRepos DoctorCheck = "duplicate_repos"

// duplicateReposFix is the finding's fix.
const duplicateReposFix = "lore fix"

// Fields of a repos row the check reads.
const (
	repoFieldProject    = "project_id"
	repoFieldMount      = "mount_name"
	repoFieldOrigin     = "origin_url"
	repoFieldURLs       = "urls"
	repoFieldForkedFrom = "forked_from"
	repoFieldSplitFrom  = "split_from"
)

// repoGroup is one set of repos rows that describe one repository: ids
// sorted, and why they were grouped (an address or a mount name).
type repoGroup struct {
	ids []string
	why string
}

// duplicateRepoGroups groups rows that describe one repository. A fork or
// split row is never grouped: it is a repo of its own by design.
func duplicateRepoGroups(rows []row.Row) []repoGroup {
	parent := map[string]string{}
	find := func(x string) string {
		for parent[x] != x {
			x = parent[x]
		}
		return x
	}
	why := map[string]string{}
	owner := map[string]string{} // grouping key -> first row id
	byID := map[string]row.Row{}
	for _, r := range rows {
		if fieldText(r, repoFieldForkedFrom) != "" || fieldText(r, repoFieldSplitFrom) != "" {
			continue
		}
		id := r.ID.String()
		byID[id] = r
		parent[id] = id
		for _, k := range repoKeys(r) {
			first, seen := owner[k]
			if !seen {
				owner[k] = id
				continue
			}
			a, b := find(first), find(id)
			if a != b {
				parent[b] = a
			}
			if _, ok := why[a]; !ok {
				why[a] = k
			}
		}
	}
	members := map[string][]string{}
	for id := range byID {
		root := find(id)
		members[root] = append(members[root], id)
	}
	var out []repoGroup
	for root, m := range members {
		if len(m) < 2 {
			continue
		}
		slices.Sort(m)
		w := why[root]
		if w == "" {
			for _, id := range m {
				if w = why[find(id)]; w != "" {
					break
				}
			}
		}
		out = append(out, repoGroup{ids: m, why: w})
	}
	slices.SortFunc(out, func(a, b repoGroup) int { return strings.Compare(a.ids[0], b.ids[0]) })
	return out
}

// repoKeys are the grouping keys of a repos row: "<project> address
// <key>" for each normalised address it holds, "<project> mount <name>".
func repoKeys(r row.Row) []string {
	pid := fieldText(r, repoFieldProject)
	var keys []string
	addrs := []string{fieldText(r, repoFieldOrigin)}
	addrs = append(addrs, repoHistory(r).Confirmed()...)
	for _, a := range addrs {
		if k, err := repourl.Normalize(a); err == nil {
			keys = append(keys, pid+" address "+k)
		}
	}
	if m := fieldText(r, repoFieldMount); m != "" {
		keys = append(keys, pid+" mount "+m)
	}
	return keys
}

// repoHistory decodes a repos row's urls (empty when absent or unreadable).
func repoHistory(r row.Row) repourl.History {
	v, ok := r.Fields[repoFieldURLs]
	if !ok || v == nil {
		return nil
	}
	raw, ok := value.JSONForm(v)
	if !ok {
		return nil
	}
	h, err := repourl.ParseHistory(raw)
	if err != nil {
		return nil
	}
	return h
}

// groupReason is the human form of a grouping key.
func groupReason(k string) string {
	_, rest, _ := strings.Cut(k, " ")
	kind, val, _ := strings.Cut(rest, " ")
	if kind == "mount" {
		return "same mount name " + val
	}
	return "same address " + val
}

// DoctorDuplicateRepos reports (and with fix folds) repos rows of the repo
// store that describe one repository.
func (a *App) DoctorDuplicateRepos(ctx context.Context, fix bool) ([]DoctorFinding, error) {
	eng, err := a.Engine(ctx)
	if err != nil {
		return nil, nil
	}
	rows, err := eng.List(ctx, store.SourceRepo, schema.TableRepos)
	if err != nil || len(rows) < 2 {
		return nil, nil
	}
	groups := duplicateRepoGroups(rows)
	if len(groups) == 0 {
		return nil, nil
	}
	var meta rowfs.RepoMeta
	top, terr := a.knownRepoTop(ctx)
	if terr == nil {
		meta, _, _ = rowfs.ReadRepoMeta(top)
	}
	byID := map[string]row.Row{}
	for _, r := range rows {
		byID[r.ID.String()] = r
	}
	var out []DoctorFinding
	for _, g := range groups {
		keep := g.ids[0]
		if slices.Contains(g.ids, meta.RepoID) {
			keep = meta.RepoID
		}
		f := DoctorFinding{Check: DoctorCheckDuplicateRepos, Level: DoctorWarning, Store: store.SourceRepo, Fix: duplicateReposFix,
			Message: plural(len(g.ids), "repos row") + " describe one repository (" + strings.Join(g.ids, ", ") + "; " + groupReason(g.why) +
				"; two machines upgraded it before either pushed?): " + duplicateReposFix + " keeps " + keep + " and re-points the rows"}
		if fix && !eng.ReadOnly(store.SourceRepo) {
			if ferr := a.foldRepos(ctx, eng, byID, keep, g.ids, meta); ferr != nil {
				f.Message += " (not fixed: " + ferr.Error() + ")"
			} else {
				f.Fixed = true
				f.Message = "folded " + plural(len(g.ids)-1, "repos row") + " into " + keep + " (" + groupReason(g.why) + "); commit .lore/data with your next push"
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// foldRepos merges every row of group into keep: their address entries go
// into keep's urls first (so no confirmed address is lost), then each is
// folded by engine.MergeRows in one batch, and _meta.json follows keep.
func (a *App) foldRepos(ctx context.Context, eng *engine.Engine, byID map[string]row.Row, keep string, group []string, meta rowfs.RepoMeta) error {
	keepID, err := ids.ParseAny(keep)
	if err != nil {
		return err
	}
	h := repoHistory(byID[keep])
	grown := false
	for _, s := range group {
		if s == keep {
			continue
		}
		for _, e := range repoHistory(byID[s]) {
			if h.Find(e.Key()) < 0 {
				h = append(h, e)
				grown = true
			}
		}
	}
	if grown {
		raw, jerr := h.JSON()
		if jerr != nil {
			return jerr
		}
		v, verr := value.Infer(raw)
		if verr != nil {
			return verr
		}
		if _, uerr := a.Update(ctx, engine.Ref{Store: store.SourceRepo, Table: schema.TableRepos, ID: keepID}, map[string]value.Value{repoFieldURLs: v}); uerr != nil {
			return uerr
		}
	}
	for _, s := range group {
		if s == keep {
			continue
		}
		drop, perr := ids.ParseAny(s)
		if perr != nil {
			return perr
		}
		res, merr := eng.MergeRows(ctx, a, schema.TableRepos, keepID, drop, false)
		if merr != nil {
			return merr
		}
		if !res.Batch.IsZero() {
			ctx = engine.WithBatch(ctx, engine.Batch{ID: res.Batch, Kind: engine.BatchKindMerge})
		}
	}
	if meta.RepoID == "" {
		return nil // no _meta.json here: nothing names the repo
	}
	// The folded ids become aliases (Q5): rows stamped `_o.repo` with one
	// stay correct, and `lore repo merge-ids` can rewrite them later.
	before := len(meta.Aliases)
	for _, s := range group {
		if s != keep && !slices.Contains(meta.Aliases, s) {
			meta.Aliases = append(meta.Aliases, s)
		}
	}
	if meta.RepoID == keep && len(meta.Aliases) == before {
		return nil
	}
	meta.RepoID = keep
	return a.WriteRepoMeta(ctx, meta)
}
