package engine

// natural_edit.go — an edit keeps a natural key unique as a create does.
// A create derives the row's id from its key (naturalKey / NaturalID), so
// a taken name collides on the id and is refused E_NAME_TAKEN
// (recreateNatural). An edit keeps the row's id, so a renamed key never
// collides there: `prompt edit --title <taken>` wrote a second row holding
// the same name (campaign knowledge 2026-11-08). The edit now asks the
// same question with the same wording and hint.

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// refuseTakenKeyOnEdit refuses (E_NAME_TAKEN) an edit of cur whose new
// fields (whole) move its natural key onto a key another live row of the
// same table in src holds. An edit that leaves the key as it was, or a
// table without a natural key, is never checked (no extra read); nor is a
// merge's re-point (withRefRepoint, refrewrite.go).
func (e *Engine) refuseTakenKeyOnEdit(ctx context.Context, src store.Source, def schema.TableDef, cur row.Row, whole map[string]value.Value) error {
	keyFields, _, natural := naturalKey(def, whole)
	if !natural || isRefRepoint(ctx) || sameKey(retype(def, cur.Fields), whole, keyFields) {
		return nil
	}
	rows, err := e.List(ctx, src, def.Name)
	if err != nil {
		return err
	}
	for _, l := range rows {
		if l.ID == cur.ID || l.Deleted || !sameKey(l.Fields, whole, keyFields) {
			continue
		}
		return errs.New(errs.NameTaken, takenKeyMessage(def, keyFields, whole, l.ID)).
			WithHint("use the existing row " + l.ID.String() + ", or pick another " + takenKeyNames(keyFields))
	}
	return nil
}
