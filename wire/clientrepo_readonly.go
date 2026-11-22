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
// attached here). own is true for a repo of the org's own linked to a
// store folder (`lore org link --own`): it is no client repo, and calling
// it one named the wrong cause (devco:V-3). The error wraps
// store.ErrReadOnly (E_STORE_READ_ONLY). target is the --to of the link's
// folder when it is not attached (Options.RepoLinkTarget, "" unknown):
// the hint names it instead of a "<store>/<folder>" placeholder
// (devco:D-670).
func clientRepoWriteRefusal(remote string, linked store.Source, target string, own bool) error {
	why := "is a client repo with no lore data of its own: lore writes nothing into it"
	if own {
		why = "is an own repo whose notes live in its linked store folder, not in the repo"
	}
	e := errs.Wrap(errs.StoreReadOnly, "this repository ("+remote+") "+why+", so a write here (such as --to repo) is refused", store.ErrReadOnly)
	if linked == "" && target != "" {
		return e.WithHint("it is linked to " + target + ", which this repo does not attach (`lore whoami` says why): write there with --to " + target)
	}
	if linked == "" {
		// no link of this repo is in config.yaml's org entries: whoami
		// names the link, and the store has to be set up before --to can
		// name it, so no --to is printed
		return e.WithHint("`lore whoami` names the store folder this repo is linked to")
	}
	return e.WithHint("it is linked to " + string(linked) + ": write there with --to " + string(linked))
}
