package wire

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// A bot actor (H-116) is a non-coding writer named by LORE_ACTOR or
// --actor as system:<label>, cron:<label> or plugin:<label>: a nightly
// job, a plugin, an integration. Inside an org it writes only once a
// person registered it with an owner (`lore actor register --kind <kind>
// --label <label>`), so every row it writes names who answers for it:
//
//	bot key from LORE_ACTOR, in an org
//	  ├─ roster row, active, owner on the roster ─▶ writes; st.who.Owner = the owner
//	  │     (commits are signed as the owner, the actor row names owner_id,
//	  │      and its display name says "<label> (<kind>, owner <name>)")
//	  └─ otherwise ─▶ every write refused, naming the register command
//
// The CI actor (kind system, from CI detection) is not a bot named by the
// caller and is left alone; outside an org a bot key acts as before.

// bindBotOwner checks the open's bot identity against its org's roster and
// binds the owner, or refuses every write of this open (RefuseWrites, as
// refuseWritesIfLeft does) with the step that fixes it.
func (st *state) bindBotOwner(ctx context.Context, home string, attached []attach.Opened) {
	if st.eng == nil || st.actor.IsZero() || st.who.Org == "" || !identity.IsBotKind(st.who.Kind) {
		return
	}
	if st.who.Step != identity.StepEnv && st.who.Step != identity.StepFlag {
		return
	}
	bot, owner, found := st.botRosterRows(ctx, home, attached)
	var refusal error
	switch {
	case !found:
		refusal = botRefusal(st.who, "is not on the roster of "+st.who.Org)
	case !bot.Active || rosterGone(bot.Status):
		refusal = botRefusal(st.who, "was deactivated on the roster of "+st.who.Org+" (registering it again turns it back on)")
	case owner.ID == "" || owner.Kind != schema.ActorKindHuman || !owner.Active || rosterGone(owner.Status):
		refusal = botRefusal(st.who, "has no active owner on the roster of "+st.who.Org)
	}
	if refusal != nil {
		st.eng.RefuseWrites(store.SourceRepo, refusal)
		for _, o := range attached {
			st.eng.RefuseWrites(o.Name, refusal)
		}
		return
	}
	st.who.Owner = &identity.Resolved{StableKey: owner.StableKey, DisplayName: owner.Name, Kind: schema.ActorKindHuman, Handle: owner.Handle, Org: st.who.Org, Step: st.who.Step}
	if bot.Name != "" {
		st.who.DisplayName = bot.Name
	}
	if st.who.Label == "" {
		st.who.Label = bot.Label
	}
}

// botRefusal is the write refusal of a bot actor that cannot write, with
// the register command its owner runs (named flags, H-116).
func botRefusal(who identity.Resolved, why string) error {
	kind, label := who.Kind, strings.TrimPrefix(who.StableKey, who.Kind+":")
	return errs.New(errs.InvalidInput, "the "+kind+" actor "+who.StableKey+" (from "+string(who.Step)+") "+why+", so it cannot write: a bot writes only with a person on the roster answering for it").
		WithHint("its owner registers it once: lore actor register --kind " + kind + " --label " + label + " --owner @<person>")
}

// botRosterRows reads the bot's roster row and its owner's: from an
// attached org folder, else from the org folder's clone on this machine
// (a command run inside a store clone's project folder, B2-25).
func (st *state) botRosterRows(ctx context.Context, home string, attached []attach.Opened) (bot, owner RosterActor, found bool) {
	for _, o := range attached {
		if o.Level != policy.LevelOrg {
			continue
		}
		_, r, err := st.eng.Get(ctx, engine.Ref{Store: o.Name, Table: schema.TableActors, ID: st.actor.Any()})
		if err != nil || r.Deleted {
			continue
		}
		bot = actorOfRow(r)
		if oid, perr := ids.Parse[ids.Actor](bot.OwnerID); perr == nil {
			if _, orow, oerr := st.eng.Get(ctx, engine.Ref{Store: o.Name, Table: schema.TableActors, ID: oid.Any()}); oerr == nil && !orow.Deleted {
				owner = actorOfRow(orow)
			}
		}
		return bot, owner, true
	}
	_, folder, ok := orgRosterClone(home, st.who.Org)
	if !ok {
		return RosterActor{}, RosterActor{}, false
	}
	if bot, found = rosterActorOnDisk(folder, st.actor.String()); !found {
		return RosterActor{}, RosterActor{}, false
	}
	if bot.OwnerID != "" {
		owner, _ = rosterActorOnDisk(folder, bot.OwnerID)
	}
	return bot, owner, true
}

// rosterActorOnDisk reads one actors row file of an org folder clone.
func rosterActorOnDisk(folder, id string) (RosterActor, bool) {
	b, err := os.ReadFile(filepath.Join(folder, schema.TableActors, id+".json"))
	if err != nil {
		return RosterActor{}, false
	}
	var doc struct {
		Deleted   bool   `json:"_deleted"`
		Kind      string `json:"kind"`
		StableKey string `json:"stable_key"`
		Name      string `json:"display_name"`
		Handle    string `json:"handle"`
		Label     string `json:"label"`
		Owner     string `json:"owner_actor_id"`
		Active    *bool  `json:"active"`
		Status    string `json:"status"`
	}
	if json.Unmarshal(b, &doc) != nil || doc.Deleted {
		return RosterActor{}, false
	}
	return RosterActor{ID: id, Kind: doc.Kind, StableKey: doc.StableKey, Name: doc.Name, Handle: doc.Handle, Label: doc.Label,
		OwnerID: doc.Owner, Active: doc.Active == nil || *doc.Active, Status: doc.Status}, true
}
