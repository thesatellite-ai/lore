package wire

// doctor_dupactors.go — the doctor's duplicate_actors check: one person
// with two actors, left by an email change made before an email change
// kept one actor (owner decision 8, 2026-11-10: since then the actor keeps
// its id and the old key becomes an alias, so no new pair appears).
//
//	0.1.17 / early 0.1.18: git or identity email old@ ─▶ act_A (id of human:old@)
//	the person moves to new@                         ─▶ act_B (id of human:new@)
//	both rows carry the person's name ─▶ `actor list` shows two humans
//
//	lore doctor ─▶ duplicate_actors: human actors of one name, neither
//	               merged into the other (merged_into), are named together
//	   org roster attached ─▶ warning; fix: lore actor contact add <keep>
//	                          email <old email> --identity --merge (TEAM_PROFILES
//	                          6.4: asks first; the old row gets merged_into,
//	                          no row is rewritten, reports follow the chain)
//	   no org             ─▶ note: team profiles (and so the merge) live in an
//	                          org store (TS-1); both actors stay, nothing is lost
//
// The kept actor is the caller's own when it is one of the pair (the
// identity in use now), else the one created last. The old email is the
// other actor's stable_key when that key names its id (as every key-made
// actor's does), else its first key_aliases entry that does. A name is the
// only signal (the rows of a no-org repo carry no email), so two people of
// one name are named too; the message says "if they are one person" and
// the merge asks before it writes. Agents, CI and system actors are never
// paired (an agent's name is its owner's plus the tool).

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/logic/actorsource"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// DoctorCheckDuplicateActors is the check's JSON name.
const DoctorCheckDuplicateActors DoctorCheck = "duplicate_actors"

// dupActor is one human actor as the check sees it.
type dupActor struct {
	id      ids.AnyID
	name    string
	key     string
	aliases []string
	created string // created_at (RFC 3339 text sorts in time order)
}

// email is the address the merge names for a: the key (or alias) that
// derives a's id, without its human: prefix ("" when none does).
func (d dupActor) email() string {
	for _, k := range append([]string{d.key}, d.aliases...) {
		email, ok := strings.CutPrefix(strings.TrimSpace(k), identity.KindHuman+":")
		if !ok || email == "" {
			continue
		}
		if id, err := identity.ActorID(k); err == nil && id.Any() == d.id {
			return email
		}
	}
	return ""
}

// shown is "Una Upgrader (act_…)".
func (d dupActor) shown() string { return d.name + " (" + d.id.String() + ")" }

// DoctorDuplicateActors is the doctor check (registered in doctor_op.go).
func (a *App) DoctorDuplicateActors(ctx context.Context) ([]DoctorFinding, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	roster := a.actorRosterStore(ctx, st)
	srcs := []store.Source{roster}
	if roster == "" {
		srcs = nil
		shadow := shadowedStores(st.eng)
		for _, s := range st.eng.Stores() {
			if s != store.SourceMachine && !shadow[s] {
				srcs = append(srcs, s)
			}
		}
	}
	groups := dupActorGroups(ctx, st, srcs)
	out := make([]DoctorFinding, 0, len(groups))
	for _, g := range groups {
		out = append(out, dupFinding(st, g, roster))
	}
	return out, nil
}

// dupActorGroups reads every live human actor of srcs (one entry per id,
// the copy with the most fields) and returns the groups of two or more
// that share a name, in name order, each newest first.
func dupActorGroups(ctx context.Context, st *state, srcs []store.Source) [][]dupActor {
	byID := map[ids.AnyID]dupActor{}
	merged := map[ids.AnyID]bool{}
	for _, src := range srcs {
		rows, err := st.eng.List(ctx, src, schema.TableActors)
		if err != nil {
			continue // an unreadable store: the store checks report it
		}
		for _, r := range rows {
			if r.Deleted {
				continue
			}
			if fieldText(r, schema.FieldActorMergedInto) != "" {
				merged[r.ID] = true // merged already: settled (6.4)
				continue
			}
			if actorKindOf(fieldText(r, actorFieldKind)) != schema.ActorKindHuman {
				continue
			}
			d := dupActorOf(r)
			if d.name == "" {
				continue
			}
			if have, ok := byID[r.ID]; !ok || (have.key == "" && d.key != "") {
				byID[r.ID] = d
			}
		}
	}
	byName := map[string][]dupActor{}
	for id, d := range byID {
		if !merged[id] {
			n := strings.ToLower(strings.Join(strings.Fields(d.name), " "))
			byName[n] = append(byName[n], d)
		}
	}
	var out [][]dupActor
	for _, g := range byName {
		if len(g) < 2 {
			continue
		}
		slices.SortFunc(g, func(x, y dupActor) int {
			if c := strings.Compare(y.created, x.created); c != 0 {
				return c
			}
			return strings.Compare(x.id.String(), y.id.String())
		})
		out = append(out, g)
	}
	slices.SortFunc(out, func(x, y []dupActor) int { return strings.Compare(x[0].name, y[0].name) })
	return out
}

// dupActorOf is the check's view of an actors row.
func dupActorOf(r row.Row) dupActor {
	return dupActor{id: r.ID, name: strings.TrimSpace(fieldText(r, actorFieldDisplay)), key: fieldText(r, actorFieldKey),
		aliases: actorsource.SplitAliases(fieldText(r, schema.FieldActorKeyAliases)), created: fieldText(r, row.KeyCreatedAt)}
}

// dupFinding is the finding of one group: keep the caller's actor (else
// the newest, g[0]) and merge each other into it.
func dupFinding(st *state, g []dupActor, roster store.Source) DoctorFinding {
	keep := g[0]
	for _, d := range g {
		if d.id == st.actor.Any() {
			keep = d
		}
	}
	var others, fixes []string
	for _, d := range g {
		if d.id == keep.id {
			continue
		}
		others = append(others, d.shown())
		email := d.email()
		if email == "" {
			email = "<" + d.id.String() + "'s email>"
		}
		fixes = append(fixes, "lore actor contact add "+keep.id.String()+" email "+email+" --identity --merge")
	}
	msg := plural(len(g), "human actor") + " share the name " + quoteName(keep.name) + ": " + keep.shown() + " and " + strings.Join(others, ", ") +
		"; if they are one person (an email change before 0.1.18 kept one actor), merge the older into " + keep.id.String()
	if roster == "" {
		return DoctorFinding{Check: DoctorCheckDuplicateActors, Level: DoctorNote,
			Message: msg + " once this repo has an org (team profiles live in an org store: lore org create <name>); until then both stay and nothing is lost"}
	}
	return DoctorFinding{Check: DoctorCheckDuplicateActors, Level: DoctorWarning, Store: roster,
		Message: msg + " (asks first; no row is rewritten, reports follow merged_into)", Fix: strings.Join(fixes, "; ")}
}

// quoteName is name in double quotes.
func quoteName(name string) string { return "\"" + name + "\"" }
