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
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
)

// OpSetURL records a store's new address.
const OpSetURL = "store.set_url"

// SetURLIn is the input of store.set_url.
type SetURLIn struct {
	Name string `json:"name" arg:"0" desc:"the store: a stores.yaml name, me, an org entry name, or its sto_ id"`
	URL  string `json:"url" arg:"1" desc:"the store's new git url, local git repository path or file:// folder"`
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
	// Folders are the folder entries (<name>/<folder>) of the same store
	// in stores.yaml whose url was re-pointed too.
	Folders []string `json:"folders,omitempty"`
	// Places are the other places naming the store that now name the new
	// url (SOURCE_OF_TRUTH row 7): ".lore/stores.yaml <name>",
	// "orgs[<org>].stores", "personal.store", "setups[<setup>].personal_store".
	Places []string `json:"places,omitempty"`
	// OrgFiles say what happened to each moved org's lore-org.yaml.
	OrgFiles []string `json:"org_files,omitempty"`
	// Clone says the clone at the old address, which held commits the
	// store had not got, now fetches from and pushes to the new one, so
	// the next sync sends them ("" when there was nothing to carry).
	Clone string `json:"clone,omitempty"`
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
		Confirm:  op.ConfirmRule{MayAsk: true, NoSecretFindings: true}, // CFG7: the url lands in stores.yaml, as for add
		Examples: []op.Example{{Title: "the store repo was renamed", CLI: "lore store set-url --name agency --url git@github.com:agency/lore-notes", Want: "is now at"}},
	}, func(ctx context.Context, in SetURLIn) (SetURLOut, error) {
		if err := checkSetURLName(in.Name); err != nil {
			return SetURLOut{}, err
		}
		if in.URL == "" {
			return SetURLOut{}, errs.New(errs.InvalidInput, "store set-url: the new url is missing; usage: lore store set-url --name <store> --url <url>")
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

// checkSetURLName accepts what store set-url can name: a store name (or
// me), an org entry name, or a sto_ id; the wire side says which it is.
// Empty, a folder target and the other reserved names are refused here.
func checkSetURLName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return errs.New(errs.InvalidInput, "store set-url: name the store; usage: lore store set-url --name <store|org|sto_id> --url <url>")
	case strings.ContainsAny(name, "/ \t"):
		return errs.New(errs.InvalidInput, "store set-url: "+quote(name)+" is not a store name; a folder entry follows its store").
			WithHint("name the store itself: lore store set-url --name <store> --url <url>")
	case name != PersonalName && slices.Contains(attach.Reserved(), name):
		return errs.New(errs.InvalidInput, "store "+quote(name)+" is reserved ("+strings.Join(attach.Reserved(), ", ")+")")
	}
	return nil
}
