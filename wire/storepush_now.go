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
			WithHint("lore store add --name <name> --url " + raw + " attaches it")
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
	if in.Restore {
		if !gitbackend.RemoteResetAt(dir) {
			// B2-31: the person knows the remote was wiped before any
			// other command found it: check the remote now.
			gb := gitOf(b)
			reset := false
			if gb != nil {
				if reset, err = gb.RemoteReset(ctx); err != nil {
					return opsstores.PushOut{}, err
				}
			}
			if !reset {
				return opsstores.PushOut{}, errs.New(errs.InvalidInput, "store push --restore: the store at "+raw+" was not reset: its remote still shares history with this clone (or could not be reached); nothing to restore").
					WithHint("`lore sync` sends its waiting commits as usual; lore store doctor shows the store's state")
			}
		}
		if err = gitbackend.RestoreRemote(dir); err != nil {
			return opsstores.PushOut{}, err
		}
		out.Restored = true
	}
	sctx := ctx
	if out.Restored {
		sctx = gitbackend.WithFirstPush(ctx) // the person put the old store back (remotereset.go)
	}
	if hold := a.urlPushHold(ctx, raw); hold != nil {
		// a removed person's later commits stay in the clone here too, as
		// in an attached store's sync (armLeftHold; D-85)
		sctx = gitbackend.WithPushHold(sctx, hold)
	}
	rep, err := b.Sync(sctx)
	if err != nil {
		return opsstores.PushOut{}, err
	}
	if rep.Held != "" && rep.Sent == 0 {
		// nothing went out because the person left the org: say so, not "sent 0"
		return opsstores.PushOut{}, errs.New(errs.Refused, "store push: "+rep.Held).
			WithHint("lore doctor --all shows your status in each org")
	}
	if len(rep.Refused) > 0 {
		return opsstores.PushOut{}, errs.New(errs.Refused, "store push: the remote refused the push: "+rep.Refused[0].Reason)
	}
	out.Sent, out.Offline = rep.Sent, rep.Offline
	return out, nil
}
