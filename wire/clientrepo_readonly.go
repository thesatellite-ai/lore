package wire

import (
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// clientRepoWriteRefusal is what a write to the repo store of a client
// repo linked by its remote answers (open.go marks that store read-only:
// lore never writes into a client's repository it was not inited in).
// Why it exists (devco N-8): the bare store.ErrReadOnly said "store:
// attached read-only", naming no store, no cause and no next step, while
// `lore store list` showed no store attached read-only, so the refusal
// read as untrue. remote is the repo's remote as the link names it;
// linked is the store folder the link points at ("" when it is not
// attached here). The error wraps store.ErrReadOnly (E_STORE_READ_ONLY).
func clientRepoWriteRefusal(remote string, linked store.Source) error {
	e := errs.Wrap(errs.StoreReadOnly, "this repository ("+remote+") is a client repo with no lore data of its own: lore writes nothing into it, so a write here (such as --to repo, the default of policy set) is refused", store.ErrReadOnly)
	if linked == "" {
		return e.WithHint("`lore whoami` names the store folder this repo is linked to; write there with --to <store>/<folder>")
	}
	return e.WithHint("it is linked to " + string(linked) + ": write there with --to " + string(linked))
}
