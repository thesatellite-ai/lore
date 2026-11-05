package task

// Bulk edit (TASK 18.1, TSQ6, XQ1, XQ2):
//
//	editOp ─► no --where ─► edit (one task)
//	       └► --where ─► matchWhere (every task, the task list grammar)
//	            ─► --dry-run: list, write nothing
//	            ─► bulk.Preview (E_NEEDS_CONFIRM above batch.preview_rows)
//	            ─► bulk.Begin(task) ─► edit each, one bat_ id on every write
//	            (history entries are written by each edit, 17.2)

import (
	"context"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/work"
)

// editOp is task.edit: one task by id, or every task --where matches.
func (o ops) editOp(ctx context.Context, in EditIn) (work.WriteOut, error) {
	if len(in.Where) == 0 {
		if in.DryRun {
			return work.WriteOut{}, errs.New(errs.InvalidInput, "task edit: --dry-run needs --where")
		}
		if strings.TrimSpace(in.ID) == "" {
			return work.WriteOut{}, errs.New(errs.InvalidInput, "task edit: give a task id or --where").
				WithHint("lore task edit tsk_<id> --priority high, or lore task edit --where 'status:todo' --priority high")
		}
		return o.edit(ctx, in)
	}
	if in.ID != "" {
		return work.WriteOut{}, errs.New(errs.InvalidInput, "task edit: give a task id or --where, not both")
	}
	hits, err := o.matchWhere(ctx, in.Where)
	if err != nil {
		return work.WriteOut{}, err
	}
	if len(hits) == 0 {
		return work.WriteOut{Human: "no task matches --where; nothing changed\n"}, nil
	}
	items := make([]bulk.Item, len(hits))
	for i, s := range hits {
		items[i] = bulk.Item{ID: s.Row.ID.String(), Label: work.Field(s.Row, work.FieldTitle)}
	}
	if in.DryRun {
		var b strings.Builder
		b.WriteString("would change " + strconv.Itoa(len(hits)) + " tasks (nothing written, --dry-run):\n")
		for _, it := range items {
			b.WriteString("  " + it.String() + "\n")
		}
		return work.WriteOut{Human: b.String()}, nil
	}
	if err = bulk.Preview(ctx, o.env, hits[0].Source, "task edit --where", items); err != nil {
		return work.WriteOut{}, err
	}
	bctx, batch, err := bulk.Begin(ctx, bulk.KindTask)
	if err != nil {
		return work.WriteOut{}, err
	}
	var warn []work.Warning
	for _, s := range hits {
		one := in
		one.Where, one.ID = nil, s.Row.ID.String()
		w, eerr := o.edit(bctx, one)
		if eerr != nil {
			// Partial failure is reported with the batch id, so the rows
			// already changed can be undone as one batch.
			return work.WriteOut{}, errs.Wrap(errs.CodeOf(eerr), "task edit --where: "+s.Row.ID.String()+" failed after earlier tasks of batch "+batch.String()+" changed (lore batch show "+batch.String()+")", eerr)
		}
		warn = append(warn, w.Warnings...)
	}
	return work.WriteOut{ID: batch.String(), Warnings: warn,
		Human: work.Success + " " + strconv.Itoa(len(hits)) + " tasks changed (batch " + batch.String() + "); lore batch show " + batch.String() + "\n"}, nil
}

// matchWhere is every task (any status, every attached store) the --where
// terms match, in the stores' order. Bulk edit matches exactly what the
// filter says: no hidden active-set default (the preview lists the rows).
func (o ops) matchWhere(ctx context.Context, where []string) ([]work.Sourced, error) {
	keys := taskKeys()
	m, whereMeta, err := o.whereMatcher(ctx, where, keys)
	if err != nil {
		return nil, err
	}
	get, _, err := o.withSLA(ctx, rowGetter(keys))
	if err != nil {
		return nil, err
	}
	all, err := work.ListAll(ctx, o.env, table, "")
	if err != nil {
		return nil, err
	}
	out := make([]work.Sourced, 0, len(all))
	for _, s := range all {
		if keepRow(s.Row) && m.Match(get(s.Row)) {
			out = append(out, s)
		}
	}
	// r3-grammar: meta.<key> terms narrow through the metadata filter.
	if len(whereMeta) > 0 {
		if out, _, err = work.Narrow(ctx, o.env, out, work.MetaFlags{Meta: whereMeta}); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// keepRow drops tombstones (a deleted task is never edited in bulk).
func keepRow(r row.Row) bool { return !r.Deleted }
