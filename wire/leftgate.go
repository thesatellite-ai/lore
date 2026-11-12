package wire

// leftgate.go — a person the org removed (`lore member remove`, `lore
// actor offboard`: their actor row says left or archived) no longer
// writes to the org's stores or its repos (B2-12). Before this, their
// lore kept writing and pushing with no word; only `lore doctor --all`
// said "no open period".
//
//	open ──▶ caches refreshed ──▶ canonicalActor (the caller's actor id)
//	     ──▶ refuseWritesIfLeft: the caller's actor row in the org folder
//	         store (the roster, Level org) says left|archived?
//	           yes ─▶ every store but the personal ones (and the machine
//	                  store, never attached here) refuses writes with
//	                  E_REFUSED naming the org, the day and the fix
//	           no  ─▶ nothing changes
//
// Reads keep working, and so does the personal store: the person's own
// notes are theirs. The roster on this machine is the one last synced, so
// the refusal starts at the first command after `lore sync` brings the
// removal, and ends the same way after an admin restores them.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// refuseWritesIfLeft marks the org's stores and the repo store
// write-refused when the caller's actor row on the org roster is left or
// archived. Best effort on reads: a roster that cannot be read here
// refuses nothing (doctor reports the read).
func (st *state) refuseWritesIfLeft(ctx context.Context, attached []attach.Opened) {
	if st.eng == nil || st.actor.IsZero() {
		return
	}
	var status, leftAt string
	for _, o := range attached {
		if o.Level != policy.LevelOrg {
			continue
		}
		_, r, err := st.eng.Get(ctx, engine.Ref{Store: o.Name, Table: schema.TableActors, ID: st.actor.Any()})
		if err != nil || r.Deleted {
			continue
		}
		status, leftAt = fieldText(r, schema.FieldActorStatus), fieldText(r, schema.FieldActorLeftAt)
		break
	}
	if !rosterGone(status) {
		return
	}
	refusal := leftRefusal(st.who.Org, status, leftAt)
	st.eng.RefuseWrites(store.SourceRepo, refusal)
	for _, o := range attached {
		if o.Level == policy.LevelPersonal {
			continue
		}
		st.eng.RefuseWrites(o.Name, refusal)
	}
}

// leftRefusal is the E_REFUSED every write of a removed person gets: the
// org, since when, and what to do.
func leftRefusal(org, status, leftAt string) error {
	if org == "" {
		org = "this org"
	}
	since := ""
	if day, _, ok := strings.Cut(leftAt, "T"); ok || day != "" {
		since = " since " + day
	}
	return errs.New(errs.Refused, "you are marked as "+status+" in "+org+since+"; writes to its stores and repos are refused (your personal store still works)").
		WithHint("if you are back, ask an admin of " + org + " to run lore member add <your email>")
}
