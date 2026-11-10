package wire

// setupidentity_roster.go — the setup page Identity step of a member or
// admin setup changes the person's roster actor row too (SOURCE_OF_TRUTH
// rows 1 and 3: the org roster row is the team's source of the name), by
// calling the actor.edit operation itself (ops/profile), never a copy of
// its rules.

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	opsprofile "github.com/thesatellite-ai/lore/ops/profile"
	"github.com/thesatellite-ai/lore/ops/profilestore"
)

// renameRosterActor runs actor.edit <key> --display-name <name> on org's
// roster (orgRosterStore: the org's own folder store, whatever repo the
// page runs in). config.yaml is already written; a failure says so and
// names the command that finishes the rename.
func (ap setupApplier) renameRosterActor(ctx context.Context, org string, want identity.Entry) error {
	var edit op.Entry
	for _, e := range opsprofile.Ops(orgRosterStore{a: ap.a, org: org}) {
		if e.Spec().Name == profilestore.OpActorEdit {
			edit = e
		}
	}
	if edit == nil {
		return errs.New(errs.Internal, "wire: the "+profilestore.OpActorEdit+" operation is not registered")
	}
	body, err := json.Marshal(opsprofile.ActorEditIn{Actor: want.Key, DisplayName: want.Name})
	if err != nil {
		return errs.Wrap(errs.Internal, "wire: encode "+profilestore.OpActorEdit+" input", err)
	}
	if _, err = runDirect(ctx, edit, op.JSONDecoder{Body: body}); err != nil {
		return errs.Prefix("setup identity: saved in ~/.lore/"+ConfigFile+", but the "+org+" roster still shows the old name (finish with lore actor edit "+
			want.Key+" --display-name "+strconv.Quote(want.Name)+")", err)
	}
	return nil
}
