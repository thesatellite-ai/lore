package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// ThisHolder implements settings.Holderer: the holder lore records on a
// reference it writes for this machine (SECRETS_AND_REFERENCES 5.2, SQ1,
// BATCH 11: holder = mch_ id plus a label).
//
//	ThisMachine (machine id, ~/.lore/machine.json) ─▶ id = mch_…
//	machines row (deterministic row id) label? ─▶ "<handle>.<label>"
//	    no row or no label ─▶ label "" (the id alone is still exact)
//
// A label is display only (lore secrets relabel rewrites it by id), so a
// missing one never fails the write.
func (a *App) ThisHolder(ctx context.Context) (id, label string, err error) {
	tm, err := a.ThisMachine(ctx)
	if err != nil {
		return "", "", err
	}
	id = tm.ID.String()
	_, r, gerr := a.Get(ctx, engine.Ref{ID: tm.RowID})
	if gerr != nil {
		if errs.Has(gerr, errs.NotFound) {
			return id, "", nil
		}
		return "", "", gerr
	}
	t, ok := r.Fields[schema.FieldMachineLabel].(value.Text)
	if !ok || t.String() == "" {
		return id, "", nil
	}
	label = t.String()
	if who, werr := a.Who(ctx); werr == nil && who.Handle != "" {
		label = who.Handle + "." + label
	}
	return id, label, nil
}
