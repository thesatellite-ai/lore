package wire

// storemove_identity.go — who owns a store while `lore store set-url`
// moves it (D-514). set-url opens the new address (to read its marker)
// before it rewrites orgs[].stores in config.yaml, so for that open the
// org entry still names only the old address: the new clone found no
// owning org and got the lore identity (a personal email in an org store)
// while set-url's own commit there was authored with the org identity.
// set-url marks its ctx with the move; ownerAddresses adds the old address
// to the new one, so storeSigner.owner (the one place a clone's git
// user and lore's commit identity are resolved: cloneIdentity, identityFor,
// ownedByOrg) answers the owning org's identity for both.

import (
	"context"

	"github.com/thesatellite-ai/lore/logic/storeaddr"
)

// storeMoveKey carries the store move set-url is making.
type storeMoveKey struct{}

// storeMove is one set-url move: the store at to is the store that was at
// from.
type storeMove struct{ from, to string }

// withStoreMove marks ctx as set-url moving the store at from to to.
func withStoreMove(ctx context.Context, from, to string) context.Context {
	if from == "" || to == "" || storeaddr.Same(from, to) {
		return ctx
	}
	return context.WithValue(ctx, storeMoveKey{}, storeMove{from: from, to: to})
}

// ownerAddresses are the addresses whose org entry may decide who owns the
// store at remote, in order: remote itself, then, while set-url moves the
// store to remote, the address it moves from (before orgs[].stores is
// rewritten only that one names the org; after, remote does).
func ownerAddresses(ctx context.Context, remote string) []string {
	if m, ok := ctx.Value(storeMoveKey{}).(storeMove); ok && storeaddr.Same(m.to, remote) {
		return []string{remote, m.from}
	}
	return []string{remote}
}

// orgOfMovingStore is the org whose entry owns the store at remote (orgOfURL
// over ownerAddresses); "" when none.
func orgOfMovingStore(ctx context.Context, home, remote string) string {
	for _, u := range ownerAddresses(ctx, remote) {
		if org := orgOfURL(home, u); org != "" {
			return org
		}
	}
	return ""
}
