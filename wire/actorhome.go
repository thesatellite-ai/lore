package wire

// actorhome.go — a person's actor row and machine profile live in one
// folder of a store clone, not in every folder a write touches (devco
// D-486).
//
// Every folder of a store clone attaches as its own store ("<store>/sync",
// "<store>/org"), so ensureActor wrote the caller's actor row into each
// folder its first write reached, and the machine profile went to the
// first own store attached (a product folder in a clone). A one-field
// `task edit` in sync/ then committed sync/actors/act_….json beside the
// task, and a second commit with sync/machines/mch_….json. References
// resolve across every attached store (engine checkRefs, holders), and
// a folder's open attaches the clone's org folder beside it, so the copy
// was never needed:
//
//	write into <clone>/sync ─▶ a sibling folder of the same clone holds
//	                            the row (org/, where the roster lives)?
//	   yes ─▶ no copy; doctor's actor_roster counts the sibling's row
//	   no  ─▶ the copy is written as before (refs must resolve)

import (
	"context"
	"sort"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// dirOf is a backend that names the folder it works in (git: the clone's
// work tree).
type dirOf interface{ Dir() string }

// cloneSiblings lists the other attached stores that are folders of the
// same store clone as name (same work tree, a different folder), in name
// order. Why: their rows are one clone's rows, so a row one of them holds
// need not be copied into name. A store that is not a folder of a clone,
// the repo store and any client-class store get none (a client folder
// must hold its own trimmed copies, and its readers never read the
// sibling).
func cloneSiblings(st *state, name store.Source) []store.Source {
	if name == store.SourceRepo || st.eng.IsClient(name) || rootFolder(st.folders[name]) == "" {
		return nil
	}
	d, ok := concrete(st.byName[name]).(dirOf)
	if !ok || d.Dir() == "" {
		return nil
	}
	var out []store.Source
	for n, b := range st.byName {
		if n == name || n == store.SourceRepo || st.eng.IsClient(n) || st.folders[n] == st.folders[name] {
			continue
		}
		if o, ok := concrete(b).(dirOf); ok && o.Dir() == d.Dir() {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// siblingHolding is the first folder of name's store clone (cloneSiblings)
// whose live rows include table/id; ok is false when none does. Read
// errors other than not-found are returned.
func siblingHolding(ctx context.Context, st *state, name store.Source, table row.Table, id ids.AnyID) (store.Source, bool, error) {
	for _, n := range cloneSiblings(st, name) {
		_, _, err := st.eng.Get(ctx, engine.Ref{Store: n, Table: table, ID: id})
		if err == nil {
			return n, true, nil
		}
		if !errs.Has(err, errs.NotFound) {
			return "", false, err
		}
	}
	return "", false, nil
}

// actorHeldBySibling reports whether a sibling folder of target's store
// clone already holds the actor row id, so a write into target needs no
// copy of it (D-486).
func actorHeldBySibling(ctx context.Context, st *state, target store.Source, id ids.ID[ids.Actor]) (bool, error) {
	_, ok, err := siblingHolding(ctx, st, target, schema.TableActors, id.Any())
	return ok, err
}

// machineHome is where the machine profile of a write into name goes:
// the folder of name's store clone that holds the caller's actor row (the
// roster's folder) when name itself does not, else name. Why (D-486): the
// profile followed the first own store attached, a product folder inside
// a clone, so every folder got its own machines row.
func machineHome(ctx context.Context, st *state, name store.Source) store.Source {
	if st.actor.IsZero() {
		return name
	}
	if _, _, err := st.eng.Get(ctx, engine.Ref{Store: name, Table: schema.TableActors, ID: st.actor.Any()}); err == nil {
		return name
	}
	if n, ok, err := siblingHolding(ctx, st, name, schema.TableActors, st.actor.Any()); err == nil && ok && !st.eng.ReadOnly(n) {
		return n
	}
	return name
}

// actorHomeFor is where the caller's actor row for a write into target
// belongs (D-486): target itself when it is no folder of a store clone or
// already holds the row; "" when a sibling folder of the clone holds it
// (no write needed); else the sibling folder holding the person behind
// the identity (the agent's owner, or the person), where the row is
// written once; else target (no sibling knows the person, so the copy
// keeps the write's refs resolvable, as before).
func (a *App) actorHomeFor(ctx context.Context, st *state, target store.Source) (store.Source, error) {
	if len(cloneSiblings(st, target)) == 0 {
		return target, nil
	}
	who, person := st.who, st.actor
	if w, ok := st.storeWho[target]; ok {
		who, person = w.who, w.id
	}
	id := person
	if _, _, err := st.eng.Get(ctx, engine.Ref{Store: target, Table: schema.TableActors, ID: id.Any()}); err == nil {
		return target, nil
	} else if !errs.Has(err, errs.NotFound) {
		return "", err
	}
	if held, err := actorHeldBySibling(ctx, st, target, id); err != nil || held {
		return "", err
	}
	if who.Owner != nil {
		oid, err := who.Owner.ActorID()
		if err != nil {
			return target, nil
		}
		person = oid
	}
	n, ok, err := siblingHolding(ctx, st, target, schema.TableActors, person.Any())
	if err != nil {
		return "", err
	}
	if !ok || st.eng.ReadOnly(n) {
		return target, nil
	}
	return n, nil
}
