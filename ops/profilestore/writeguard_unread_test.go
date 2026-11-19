package profilestore_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
)

// unreadEnv is a groundEnv opened in a folder no org matches: it has no
// roster of its own, and the roster of the target's org (client) answers
// readErr (nil with found false: that org keeps no roster here).
type unreadEnv struct {
	groundEnv
	readErr error
}

func (unreadEnv) Roster(context.Context, bool) (profilestore.Roster, func(), error) {
	return nil, func() {}, errs.New(errs.NotFound, "no org roster in this folder")
}

func (e unreadEnv) RosterOfOrg(context.Context, string) (profilestore.Roster, func(), bool, error) {
	return nil, func() {}, false, e.readErr
}

// TestWriteGuardFailsClosedOnUnreadRoster (devco D-694): a member's note
// --to client/app from a folder no org matches is judged by the client
// org's roster; when that roster exists but cannot be read the write is
// refused and nothing lands, never let through as "no roles to follow".
// An org that keeps no roster on this machine still has no roles to follow.
func TestWriteGuardFailsClosedOnUnreadRoster(t *testing.T) {
	ctx := context.Background()
	member, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	admin, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	memID, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	note := []engine.GuardedWrite{{After: row.Row{Table: schema.TableMemories, ID: memID.Any(), Fields: map[string]value.Value{"body": txt(t, "api pages are cached")}}}}
	g := newGroundEnv(t, member, admin)

	broken := unreadEnv{groundEnv: g, readErr: errs.New(errs.Internal, "the roster's clone is unreadable")}
	err = profilestore.WriteGuard{Env: broken}.GuardWrite(ctx, "client/app", note)
	if !errs.Has(err, errs.Refused) || !strings.Contains(err.Error(), "roster of client could not be read") || !strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("member note with the org's roster unreadable: %v, want E_REFUSED naming the unread roster", err)
	}
	if err := (profilestore.WriteGuard{Env: unreadEnv{groundEnv: g}}).GuardWrite(ctx, "client/app", note); err != nil {
		t.Errorf("member note where the org keeps no roster here: %v, want ok (no roles to follow)", err)
	}
}
