package stores

// `lore store set-url <store> <url>`: the store repo moved or was renamed
// (PROV 11.9 store row, CLI_REFERENCE 3.14). Flow:
//
//	store.set_url → CheckName → URLSetter.SetStoreURL (wire)
//	              → open the NEW url: a 0.1.18 store, else E_NOT_A_STORE
//	              → same sto_ id as the old address (when the old one is
//	                still readable), else E_INVALID_INPUT: another store
//	              → rewrite the url: stores.yaml entry, or personal.store
//	                for `me` (rows never change: they carry ids, PROV)

import (
	"context"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// OpSetURL records a store's new address.
const OpSetURL = "store.set_url"

// SetURLIn is the input of store.set_url.
type SetURLIn struct {
	Name string `json:"name" arg:"0" desc:"the store (a stores.yaml name or me)"`
	URL  string `json:"url" arg:"1" desc:"the store's new git url or file:// path"`
}

// SetURLOut is the output of store.set_url.
type SetURLOut struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Previous string `json:"previous"`
	// ID is the store's permanent sto_ id (read at the new url).
	ID string `json:"id"`
	// Changed is false when the url was already recorded.
	Changed bool `json:"changed"`
	// Note says the old address could not be read, so the id check against
	// it was skipped (the new address is still a store).
	Note string `json:"note,omitempty"`
}

// URLSetter is the optional Manager upgrade behind store.set_url.
type URLSetter interface {
	SetStoreURL(ctx context.Context, in SetURLIn) (SetURLOut, error)
}

// setURLOp is store.set_url bound to m; m without URLSetter is E_INTERNAL.
func setURLOp(m Manager) op.Entry {
	return op.Define(op.Spec{
		Name: OpSetURL, Summary: "Record a store's new address (the store repo moved or was renamed); its id and rows stay",
		Permission: op.PermAdmin, Access: op.AccessRead, Errors: storeErrors,
		Confirm:  op.ConfirmRule{MayAsk: true}, // CFG7: the url lands in stores.yaml, as for add
		Examples: []op.Example{{Title: "the store repo was renamed", CLI: "lore store set-url agency git@github.com:agency/lore-notes", Want: "is now at"}},
	}, func(ctx context.Context, in SetURLIn) (SetURLOut, error) {
		if err := CheckName(in.Name, true); err != nil {
			return SetURLOut{}, err
		}
		if in.URL == "" {
			return SetURLOut{}, errs.New(errs.InvalidInput, "store set-url: the new url is missing; usage: lore store set-url <store> <url>")
		}
		if err := CheckScheme(in.URL); err != nil {
			return SetURLOut{}, err
		}
		us, ok := upgrade.As[URLSetter](m)
		if !ok {
			return SetURLOut{}, errs.New(errs.Internal, "store set-url: this build cannot change a store url")
		}
		return us.SetStoreURL(ctx, in)
	})
}
