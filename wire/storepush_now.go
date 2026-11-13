package wire

// storepush_now.go — `lore store push [--restore]` (B3-29): push one
// store's clone now. With --restore (asked first, op.ConfirmRule.When) the
// mark of a remote lore found reset (adapters/backend/git remotereset.go)
// is cleared before the sync, so this machine's copy of the store goes
// back into the remote that was deleted and made again.

import (
	"context"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

var _ opsstores.StorePusherNow = (*App)(nil)

// PushStoreNow implements opsstores.StorePusherNow. in.Store is the
// store's url (the op resolved a name to its entry).
func (a *App) PushStoreNow(ctx context.Context, in opsstores.PushIn) (opsstores.PushOut, error) {
	raw, err := cleanFileURL(in.Store)
	if err != nil {
		return opsstores.PushOut{}, err
	}
	out := opsstores.PushOut{URL: raw}
	dir, _, err := gitbackend.CloneDirOf(a.opts.Home, raw, in.Branch)
	if err != nil {
		return opsstores.PushOut{}, errs.New(errs.InvalidInput, "store push: "+raw+" is not a git store; a folder store has nothing to push").
			WithHint("lore store list names each store's url")
	}
	if !exists(dir) {
		return opsstores.PushOut{}, errs.New(errs.NotFound, "store push: this machine has no clone of "+raw).
			WithHint("lore store add <name> " + raw + " attaches it")
	}
	if in.Restore {
		if !gitbackend.RemoteResetAt(dir) {
			return opsstores.PushOut{}, errs.New(errs.InvalidInput, "store push --restore: lore has not found the store at "+raw+" reset; nothing to restore").
				WithHint("lore store push " + in.Store + " pushes it as usual")
		}
		if err = gitbackend.RestoreRemote(dir); err != nil {
			return opsstores.PushOut{}, err
		}
		out.Restored = true
	}
	canon, err := canonical(attach.Store{URL: raw, Branch: in.Branch})
	if err != nil {
		return opsstores.PushOut{}, err
	}
	b, err := a.openStore(ctx, canon, raw)
	if err != nil {
		return opsstores.PushOut{}, err
	}
	defer func() { _ = b.Close() }() // the sync below is the only work
	rep, err := b.Sync(ctx)
	if err != nil {
		return opsstores.PushOut{}, err
	}
	if len(rep.Refused) > 0 {
		return opsstores.PushOut{}, errs.New(errs.Refused, "store push: the remote refused the push: "+rep.Refused[0].Reason)
	}
	out.Sent, out.Offline = rep.Sent, rep.Offline
	return out, nil
}
