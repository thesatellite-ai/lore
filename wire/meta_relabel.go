package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
)

// RelabelMeta is the ops Env upgrade for `lore meta rename --rewrite-rows`:
// the engine's label-only write (METADATA 3.12; no hlc or updated_at bump).
func (a *App) RelabelMeta(ctx context.Context, ref engine.Ref, keyID ids.ID[ids.MetaKey], name string) (bool, error) {
	st, err := a.writeState(ctx)
	if err != nil {
		return false, err
	}
	ok, err := st.eng.RelabelMeta(ctx, ref, keyID, name)
	a.wroteTo(ref.Store, err)
	return ok, err
}
