package wire

// store.set_url (PROV 11.9): the composition-root half. Flow:
//
//	SetStoreURL → baseStore (stores.yaml entry or me; an org-brought store
//	            is the org's config, refused here) → clean the new url
//	            → open it: a 0.1.18 marker with an id, else E_NOT_A_STORE
//	            → read the old address's marker when it still opens: a
//	              different sto_ id is another store (E_INVALID_INPUT)
//	            → rewrite the url in place (stores.yaml / personal.store)

import (
	"context"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// SetStoreURL implements opsstores.URLSetter.
func (a *App) SetStoreURL(ctx context.Context, in opsstores.SetURLIn) (opsstores.SetURLOut, error) {
	const verb = "store set-url"
	src, found, fromOrg, err := a.baseStore(ctx, in.Name)
	if err != nil {
		return opsstores.SetURLOut{}, err
	}
	if !found {
		return opsstores.SetURLOut{}, errs.New(errs.NotFound, verb+": no store named "+in.Name+" is attached").
			WithHint("run `lore store list`")
	}
	if fromOrg {
		return opsstores.SetURLOut{}, errs.New(errs.InvalidInput, verb+": store "+in.Name+" comes from the org's config, not this repo's "+attach.FileName).
			WithHint("change the store url in the org entry of ~/.lore/config.yaml")
	}
	raw, err := cleanFileURL(in.URL)
	if err != nil {
		return opsstores.SetURLOut{}, err
	}
	out := opsstores.SetURLOut{Name: in.Name, URL: raw, Previous: src.URL}
	newID, err := a.storeIDAt(ctx, attach.Store{Name: src.Name, URL: raw, Branch: src.Branch})
	if err != nil {
		return opsstores.SetURLOut{}, err
	}
	if newID.IsZero() {
		return opsstores.SetURLOut{}, errs.New(errs.NotAStore, verb+": "+raw+" holds no lore store with an id ("+rowfs.MarkerFile+")").
			WithHint("check the url; a v0.1.17 store needs `lore migrate run` first")
	}
	out.ID = newID.String()
	if raw == src.URL {
		return out, nil
	}
	// PROV 11.9 "same rules as 10.4": the store keeps its permanent id, so
	// an old address that still opens must name the same store (a typo or
	// a different repo is refused, never silently re-pointed).
	oldID, oerr := a.storeIDAt(ctx, src)
	switch {
	case oerr != nil:
		out.Note = "the old address could not be read (" + string(errs.CodeOf(oerr)) + "), so its store id was not compared"
	case !oldID.IsZero() && oldID != newID:
		return opsstores.SetURLOut{}, errs.New(errs.InvalidInput, verb+": "+raw+" is another store ("+newID.String()+"; "+in.Name+" is "+oldID.String()+")").
			WithHint("attach it under its own name: lore store add <name> " + raw)
	}
	if in.Name == opsstores.PersonalName {
		err = setPersonalStore(a.opts.Home, raw)
	} else {
		err = a.setStoresEntryURL(ctx, in.Name, raw)
	}
	if err != nil {
		return opsstores.SetURLOut{}, err
	}
	out.Changed = true
	return out, nil
}

// storeIDAt opens the store src names and returns its marker's sto_ id
// (zero for a folder without a 0.1.18 marker).
func (a *App) storeIDAt(ctx context.Context, src attach.Store) (ids.ID[ids.Store], error) {
	canon, err := canonical(attach.Store{Name: src.Name, URL: src.URL, Branch: src.Branch})
	if err != nil {
		return ids.ID[ids.Store]{}, err
	}
	b, err := a.openStore(ctx, canon, src.URL)
	if err != nil {
		return ids.ID[ids.Store]{}, err
	}
	m, _ := b.(markerOf)
	_ = b.Close() // opened only to read the marker
	if m == nil {
		return ids.ID[ids.Store]{}, nil
	}
	return m.Marker().ID, nil
}

// setStoresEntryURL rewrites the url of the stores.yaml entry name.
func (a *App) setStoresEntryURL(ctx context.Context, name, raw string) error {
	top, err := a.repoTop(ctx)
	if err != nil {
		return err
	}
	return editStoresFile(top, func(list *yaml.Node) error {
		for _, n := range list.Content {
			if v := mapGet(n, keyName); v != nil && v.Value == name {
				mapSet(n, keyURL, scalar(raw))
				return nil
			}
		}
		return errs.New(errs.NotFound, "store set-url: no store named "+name+" in "+attach.FileName)
	})
}

// compile-time check: App offers store.set_url.
var _ opsstores.URLSetter = (*App)(nil)
