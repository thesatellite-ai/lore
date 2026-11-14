package wheredel

// Archive is the bulk `<noun> archive --where` (bug hunt G-7): the same
// --where grammar as the bulk delete, over the live rows that are not
// archived yet.
//
//	Archive ─► matcher (Form/keys as Run) ─► live, not-archived matches
//	        ─► --dry-run: count and list, write nothing
//	        ─► bulk.Preview (asks above batch.preview_rows, as resource edit --where)
//	        ─► bulk.Begin(archive) ─► per store: one stage, Update each row,
//	           engine.Commit: ONE store write per store (50 archives were 50
//	           commands and 50 writes, 100 s) ─► `lore batch undo` puts them back

import (
	"context"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/store"
)

// FieldArchivedAt is the field every archivable table stamps.
const FieldArchivedAt = "archived_at"

// ArchiveEnv is what Archive needs of the engine: Run's reads plus Update.
type ArchiveEnv interface {
	Stores() []store.Source
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
	Update(ctx context.Context, ref engine.Ref, patch map[string]value.Value) (engine.Written, error)
}

// ArchiveForm decides which archive the flags ask for, as Form does for
// delete: true for the bulk --where form.
func ArchiveForm(noun, id string, where []string, dryRun bool) (bool, error) {
	hasID := strings.TrimSpace(id) != ""
	switch {
	case len(where) > 0 && hasID:
		return false, errs.New(errs.InvalidInput, noun+" archive: give an id or --where, not both")
	case len(where) > 0:
		return true, nil
	case dryRun:
		return false, errs.New(errs.InvalidInput, noun+" archive: --dry-run needs --where")
	case !hasID:
		return false, errs.New(errs.InvalidInput, noun+" archive: give an id or --where").
			WithHint("lore " + noun + " archive --id <id>, or lore " + noun + " archive --where '<field>=<value>' --dry-run")
	}
	return false, nil
}

// Archive stamps archived_at = now on every live, not yet archived row of
// sp.Table matching where, as one batch (see the file comment).
func Archive(ctx context.Context, env ArchiveEnv, sp Spec, where []string, dryRun bool, now value.Value) (Result, error) {
	m, keys, err := matcherFor(sp, where, "archive")
	if err != nil {
		return Result{}, err
	}
	cols := columns(keys, sp.Aliases)
	var hits []Located
	for _, src := range engine.ReadStoresOf(env) {
		rows, lerr := env.List(ctx, src, sp.Table)
		if lerr != nil {
			return Result{}, lerr
		}
		sortRows(rows)
		for _, r := range rows {
			if !r.Deleted && text(r, FieldArchivedAt) == "" && m.Match(getter(r, cols)) {
				hits = append(hits, Located{Source: src, Row: r})
			}
		}
	}
	out := resultOf(hits)
	if len(hits) == 0 {
		out.Human = "no live " + sp.Noun + " matches --where; nothing archived\n"
		return out, nil
	}
	if dryRun {
		out.Human = "would archive " + describe(sp.Noun, hits, 0, 0, true) + " (nothing written, --dry-run)\n"
		return out, nil
	}
	items := make([]bulk.Item, 0, len(hits))
	for _, h := range hits {
		items = append(items, bulk.Item{ID: h.Row.ID.String()})
	}
	if err = bulk.Preview(ctx, env, hits[0].Source, sp.Noun+" archive --where", items); err != nil {
		return Result{}, err
	}
	bctx, batch, err := bulk.Begin(ctx, bulk.KindArchive)
	if err != nil {
		return Result{}, err
	}
	out.Batch = batch.String()
	done := 0
	for _, group := range byStore(hits) {
		title := sp.Noun + " archive --where: " + strconv.Itoa(len(group)) + " row(s), batch " + out.Batch
		sctx := engine.Staging(store.WithCommitTitle(bctx, title))
		for _, h := range group {
			ref := engine.Ref{Store: h.Source, Table: sp.Table, ID: h.Row.ID}
			if _, uerr := env.Update(sctx, ref, map[string]value.Value{FieldArchivedAt: now}); uerr != nil {
				engine.Discard(sctx)
				return Result{}, archiveFailed(sp.Noun, h.Row.ID.String(), done, len(hits), out.Batch, uerr)
			}
		}
		if _, cerr := engine.Commit(sctx); cerr != nil {
			return Result{}, archiveFailed(sp.Noun, string(group[0].Source), done, len(hits), out.Batch, cerr)
		}
		done += len(group)
	}
	out.Human = "archived " + strconv.Itoa(len(hits)) + " " + sp.Noun + "(s) (--where); batch " + out.Batch +
		" (undo: lore batch undo --id " + out.Batch + ")\n"
	return out, nil
}

// archiveFailed names what was written before a failure (whole stores
// only: a store's stage is all or none).
func archiveFailed(noun, at string, done, total int, batch string, err error) error {
	if done == 0 {
		return errs.Prefix(noun+" archive --where: nothing archived ("+at+")", err)
	}
	return errs.Prefix(noun+" archive --where: "+at+" failed after "+strconv.Itoa(done)+" of "+strconv.Itoa(total)+
		" were archived in batch "+batch+" (undo: lore batch undo --id "+batch+")", err)
}

// byStore splits hits into runs of one store each (hits are in store order).
func byStore(hits []Located) [][]Located {
	var out [][]Located
	for i, h := range hits {
		if i == 0 || h.Source != hits[i-1].Source {
			out = append(out, nil)
		}
		out[len(out)-1] = append(out[len(out)-1], h)
	}
	return out
}

// ArchiveFlags are the bulk flags every `<noun> archive` input embeds
// (bug hunt G-7: archive took only --id, so a quarter's cleanup was a
// scripted loop of one command per row).
type ArchiveFlags struct {
	Where  []string `json:"where,omitempty" flag:"where" desc:"bulk archive: every live row matching this filter (field=value, field~=text, created_at<2026-06-01; repeatable, all must match), one batch"`
	DryRun bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"with --where: count and list what would be archived, write nothing"`
}
