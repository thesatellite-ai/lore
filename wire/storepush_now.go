package wire

// storepush_now.go — `lore store push [--restore]` (B3-29): push one
// store's clone now. With --restore (asked first, op.ConfirmRule.When) the
// mark of a remote lore found reset (adapters/backend/git remotereset.go)
// is cleared before the sync, so this machine's copy of the store goes
// back into the remote that was deleted and made again.

import (
	"context"
	"io"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/storepush"
	"github.com/thesatellite-ai/lore/model/errs"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/store"
)

var _ opsstores.StorePusherNow = (*App)(nil)

// PushStoreNow implements opsstores.StorePusherNow. in.Store is the
// store's url (the op resolved a name to its entry).
func (a *App) PushStoreNow(ctx context.Context, in opsstores.PushIn) (opsstores.PushOut, error) {
	out, _, err := a.pushStoreNow(ctx, in, nil)
	if err == nil && !out.Offline {
		a.forwardStoreMoves(ctx, a.pushNotes()) // D-699: a set-url move owed to an old address
	}
	return out, err
}

// storeNowPush is what a push of a whole store clone moved, as git
// counted it: the commits that arrived from the remote (a teammate's
// push merged first) and the commits that went out. A sync's line for a
// waiting org store clone counts these, never the rows (devco D-684: a
// client repo's sync said "sent 4, received 0" for 7 commits sent on top
// of 2 received).
type storeNowPush struct {
	sentCommits, receivedCommits, received int
}

// pushStoreNow is PushStoreNow that also returns the commits git counted
// and prints each push to notes (nil: not printed; `lore store push`
// prints its own "✓ store …" line).
func (a *App) pushStoreNow(ctx context.Context, in opsstores.PushIn, notes io.Writer) (opsstores.PushOut, storeNowPush, error) {
	raw, err := cleanFileURL(in.Store)
	if err != nil {
		return opsstores.PushOut{}, storeNowPush{}, err
	}
	out := opsstores.PushOut{URL: raw}
	dir, _, err := gitbackend.CloneDirOf(a.opts.Home, raw, in.Branch)
	if err != nil {
		return opsstores.PushOut{}, storeNowPush{}, errs.New(errs.InvalidInput, "store push: "+raw+" is not a git store; a folder store has nothing to push").
			WithHint("lore store list names each store's url")
	}
	if !exists(dir) {
		return opsstores.PushOut{}, storeNowPush{}, errs.New(errs.NotFound, "store push: this machine has no clone of "+raw).
			WithHint("lore store add --name <name> --url " + raw + " attaches it")
	}
	canon, err := canonical(attach.Store{URL: raw, Branch: in.Branch})
	if err != nil {
		return opsstores.PushOut{}, storeNowPush{}, err
	}
	b, err := a.openStore(ctx, canon, raw)
	if err != nil {
		return opsstores.PushOut{}, storeNowPush{}, err
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
					return opsstores.PushOut{}, storeNowPush{}, err
				}
			}
			if !reset {
				return opsstores.PushOut{}, storeNowPush{}, errs.New(errs.InvalidInput, "store push --restore: the store at "+raw+" was not reset: its remote still shares history with this clone (or could not be reached); nothing to restore").
					WithHint("`lore sync` sends its waiting commits as usual; lore store doctor shows the store's state")
			}
		}
		if err = gitbackend.RestoreRemote(dir); err != nil {
			return opsstores.PushOut{}, storeNowPush{}, err
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
	var rep store.SyncReport
	if gb := gitOf(b); gb != nil {
		rep, err = gb.SyncWith(sctx, gitbackend.SyncOptions{Squash: a.urlPushSettings(gb, raw).Squash()})
	} else {
		rep, err = b.Sync(sctx)
	}
	if gb := gitOf(b); gb != nil && notes != nil {
		// the push line every push to a store prints (pushreport.go):
		// a sync that sent a waiting clone said only its counts line
		// (devco D-684)
		printPushes(notes, OrgStoreName(a.opts.Home, raw), gb.TakePushes())
	}
	if err != nil {
		return opsstores.PushOut{}, storeNowPush{}, err
	}
	if rep.Held != "" && rep.Commits == 0 {
		// nothing went out because the person left the org: say so, not "sent 0"
		return opsstores.PushOut{}, storeNowPush{}, errs.New(errs.Refused, "store push: "+rep.Held).
			WithHint("lore doctor --all shows your status in each org")
	}
	if len(rep.Refused) > 0 {
		return opsstores.PushOut{}, storeNowPush{}, errs.New(errs.Refused, "store push: the remote refused the push: "+rep.Refused[0].Reason)
	}
	// D-517: the commits that went out, not the rows of the root folder
	// (the clone is opened at its root, so an org folder's rows and a
	// commit of lore-org.yaml counted 0)
	out.Sent, out.Rows, out.Offline = rep.Commits, rep.Sent, rep.Offline
	for _, c := range rep.Others {
		out.Rows += c.Sent
	}
	moved := storeNowPush{sentCommits: rep.Commits, receivedCommits: rep.CommitsIn, received: rep.Received}
	for _, c := range rep.Others {
		moved.received += c.Received
	}
	return out, moved, nil
}

// urlPushSettings is the push settings of the store clone gb opened by
// url raw: a personal store url reads the personal: block, any other
// store its lore-store.yaml (as an attached store's push does,
// storePush.pushSettings), so `lore store push` squashes like `lore sync`.
func (a *App) urlPushSettings(gb *gitbackend.Backend, raw string) storepush.Settings {
	rk, _ := storeKey(raw)
	for _, u := range personalStoreURLs(a.opts.Home) {
		if k, _ := storeKey(u); u == raw || (rk != "" && k == rk) {
			return a.storeSettings(gb, placePersonal)
		}
	}
	return a.storeSettings(gb, placeStore)
}
