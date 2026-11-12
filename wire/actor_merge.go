package wire

// actor_merge.go — one identity, two actor ids (owner report 2026-11-12:
// adopting a v0.1.17 repo brought in act_A with stable_key human:<email>
// while the org roster already had act_B with the same key).
//
//	MergeActor (opsinfra.ActorMerger, `lore actor merge <from> <into>`)
//	   both actors read in the stores this command reads ─▶ refusals
//	   every live row naming <from> (a ref, a text id, a list item) in a
//	   writable store ─▶ an ordinary edit naming <into> (new HLC, audit)
//	   each copy of <from>'s row ─▶ merged_into <into>, active false,
//	   status left (actors are never deleted: the marked row is the
//	   tombstone, and reports follow merged_into)
//	DoctorSharedKeys (`lore doctor`, check actor_stable_key)
//	   two live, unmerged actors with one stable key ─▶ warning naming
//	   both ids and `lore actor merge <from> <into>` (into: the roster's,
//	   else the id the key derives, else the older); `lore fix --yes` /
//	   `lore doctor --fix --yes` runs that merge itself (MergeActor, the
//	   same pair) and prints what it merged; without --yes the warning
//	   stays, saying it needs --yes, and nothing is written
//	   human:<login> with no email whose login is a roster person's
//	   handle ─▶ note: likely the same person, never merged on its own
//	adoptActorMap (store adopt) / migrateActorKeys (migrate run): a
//	   source actor whose stable key the roster (or the folder) already
//	   has under another id is mapped onto it: its row is not brought in,
//	   the rows naming it name the existing actor, and the output says so.

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

var _ opsinfra.ActorMerger = (*App)(nil)

// actorMergeVerb starts the merge's messages.
const actorMergeVerb = "actor merge"

// mapActorValue is v with every actor id of m replaced: a ref, a text that
// is exactly the id, or the items of a list. changed is false when
// nothing named an id of m.
func mapActorValue(v value.Value, m map[string]ids.AnyID) (value.Value, bool) {
	switch x := v.(type) {
	case value.Ref:
		if to, ok := m[x.String()]; ok {
			if r, err := value.RefOf(to); err == nil {
				return r, true
			}
		}
	case value.Text:
		if to, ok := m[x.String()]; ok {
			if t, err := value.TextOf(to.String()); err == nil {
				return t, true
			}
		}
	case value.List:
		items := x.Items()
		hit := false
		for i, it := range items {
			if nv, ok := mapActorValue(it, m); ok {
				items[i], hit = nv, true
			}
		}
		if hit {
			if l, err := value.ListOf(x.Elem(), items...); err == nil {
				return l, true
			}
		}
	}
	return v, false
}

// mapActorFields is the patch that makes fields name the actors of m:
// only the changed fields (nil when none).
func mapActorFields(fields map[string]value.Value, m map[string]ids.AnyID) map[string]value.Value {
	var patch map[string]value.Value
	for k, v := range fields {
		if v == nil {
			continue
		}
		if nv, ok := mapActorValue(v, m); ok {
			if patch == nil {
				patch = map[string]value.Value{}
			}
			patch[k] = nv
		}
	}
	return patch
}

// actorCopy is one store's copy of an actor row.
type actorCopy struct {
	src store.Source
	r   row.Row
}

// MergeActor implements opsinfra.ActorMerger.
func (a *App) MergeActor(ctx context.Context, in opsinfra.ActorMergeIn) (opsinfra.ActorMergeOut, error) {
	from, err := ids.ParseAny(in.From)
	if err != nil || !strings.HasPrefix(in.From, string(ids.PrefixActor)+actorIDSep) {
		return opsinfra.ActorMergeOut{}, errs.New(errs.InvalidInput, actorMergeVerb+": "+quoteName(in.From)+" is not an actor id (act_…)").WithHint("`lore actor list` shows the ids")
	}
	into, err := ids.ParseAny(in.Into)
	if err != nil || !strings.HasPrefix(in.Into, string(ids.PrefixActor)+actorIDSep) {
		return opsinfra.ActorMergeOut{}, errs.New(errs.InvalidInput, actorMergeVerb+": "+quoteName(in.Into)+" is not an actor id (act_…)").WithHint("`lore actor list` shows the ids")
	}
	if from == into {
		return opsinfra.ActorMergeOut{}, errs.New(errs.InvalidInput, actorMergeVerb+": "+in.From+" is both actors; give two different ids")
	}
	st, err := a.state(ctx)
	if err != nil {
		return opsinfra.ActorMergeOut{}, err
	}
	fromCopies, intoCopies, err := actorCopies(ctx, st, from, into)
	if err != nil {
		return opsinfra.ActorMergeOut{}, err
	}
	stores := storeNames(st.eng.Stores())
	if len(fromCopies) == 0 || len(intoCopies) == 0 {
		missing := in.From
		if len(fromCopies) > 0 {
			missing = in.Into
		}
		return opsinfra.ActorMergeOut{}, errs.New(errs.NotFound, actorMergeVerb+": no actor "+missing+" in the stores this command reads ("+strings.Join(stores, ", ")+")").
			WithHint("run it in a repo that attaches the store holding it (the org roster's actors: a repo linked to the org); `lore actor list` shows the ids")
	}
	f, t := fromCopies[0].r, intoCopies[0].r
	if fk, tk := actorKindOf(fieldText(f, actorFieldKind)), actorKindOf(fieldText(t, actorFieldKind)); fk != tk {
		return opsinfra.ActorMergeOut{}, errs.New(errs.Refused, actorMergeVerb+": "+in.From+" is a "+fk+" and "+in.Into+" a "+tk+"; only one person's (or one agent's) two ids are merged").
			WithHint("check the ids with `lore actor show <id>`")
	}
	if m := fieldText(f, schema.FieldActorMergedInto); m != "" && m != into.String() {
		return opsinfra.ActorMergeOut{}, errs.New(errs.Refused, actorMergeVerb+": "+in.From+" is already merged into "+m).
			WithHint("merge into that actor instead: lore actor merge " + in.From + " " + m)
	}
	labels := a.storeLabels(ctx, st)
	out := opsinfra.ActorMergeOut{From: from.String(), Into: into.String(), FromName: fieldText(f, actorFieldDisplay), IntoName: fieldText(t, actorFieldDisplay),
		DryRun: in.DryRun}
	for _, s := range stores {
		out.Stores = append(out.Stores, labels(store.Source(s)))
	}
	if k := fieldText(f, actorFieldKey); k != "" && k == fieldText(t, actorFieldKey) {
		out.Key = k
	}
	m := map[string]ids.AnyID{from.String(): into}
	edits, readOnly, err := actorRefEdits(ctx, st, m, map[ids.AnyID]bool{from: true})
	if err != nil {
		return opsinfra.ActorMergeOut{}, err
	}
	for _, s := range readOnly {
		out.ReadOnly = append(out.ReadOnly, labels(store.Source(s)))
	}
	perTable := map[string]int{}
	perStore := map[store.Source]int{}
	for _, e := range edits {
		perTable[string(e.ref.Table)]++
		perStore[e.ref.Store]++
	}
	out.Rows = len(edits)
	for _, tb := range slices.Sorted(maps.Keys(perTable)) {
		out.Tables = append(out.Tables, opsinfra.ActorMergeTable{Table: tb, Rows: perTable[tb]})
	}
	out.Written = mergeStoreCounts(perStore, labels)
	if in.DryRun {
		return out, nil
	}
	// every row of a store in ONE store commit and one push (owner report
	// 2026-11-12: one commit and push per row took 10 s a row); a row that
	// cannot be edited is listed with its reason, the rest are written
	title := actorMergeVerb + " " + from.String() + " into " + into.String()
	b := a.bulk(ctx, title, func(r engine.Ref) string {
		return "`lore show " + r.ID.String() + "` shows the row; edit what the reason names, then rerun"
	})
	edited := map[store.Source]int{}
	for _, e := range edits {
		if b.update(e.ref, e.patch) {
			edited[e.ref.Store]++
		}
	}
	marked := a.markMerged(ctx, st, b, fromCopies, into)
	marked += a.rosterTombstone(ctx, st, b, fromCopies, into)
	cerr := b.commit()
	out.Rows = 0
	for src, n := range edited {
		edited[src] = min(n, b.written[src]) // a store whose commit failed wrote none
		out.Rows += edited[src]
	}
	out.Marked = min(marked, b.total()-out.Rows)
	out.Written = mergeStoreCounts(edited, labels)
	if ferr := b.failedErr(actorMergeVerb+" "+from.String()+" "+into.String(), "lore actor merge "+from.String()+" "+into.String()+" --yes"); ferr != nil {
		return out, ferr
	}
	return out, cerr
}

// mergeStoreCounts is per-store row counts as the output lists them,
// sorted by label.
func mergeStoreCounts(per map[store.Source]int, label func(store.Source) string) []opsinfra.ActorMergeStore {
	var out []opsinfra.ActorMergeStore
	for src, n := range per {
		if n > 0 {
			out = append(out, opsinfra.ActorMergeStore{Store: label(src), Rows: n})
		}
	}
	slices.SortFunc(out, func(x, y opsinfra.ActorMergeStore) int { return strings.Compare(x.Store, y.Store) })
	return out
}

// actorCopies reads every live copy of the two actors' rows.
func actorCopies(ctx context.Context, st *state, from, into ids.AnyID) ([]actorCopy, []actorCopy, error) {
	var f, t []actorCopy
	for _, src := range st.eng.Stores() {
		rows, err := st.eng.List(ctx, src, schema.TableActors)
		if err != nil {
			return nil, nil, err
		}
		for _, r := range rows {
			switch {
			case r.Deleted:
			case r.ID == from:
				f = append(f, actorCopy{src, r})
			case r.ID == into:
				t = append(t, actorCopy{src, r})
			}
		}
	}
	return f, t, nil
}

// actorEdit is one row a merge edits.
type actorEdit struct {
	ref   engine.Ref
	patch map[string]value.Value
}

// actorRefEdits lists the edits that make every live row of st's
// writable stores name the actors of m (skip: actor rows left alone, the
// merged ones themselves), and the read-only stores holding such rows.
func actorRefEdits(ctx context.Context, st *state, m map[string]ids.AnyID, skip map[ids.AnyID]bool, only ...store.Source) ([]actorEdit, []string, error) {
	var out []actorEdit
	var readOnly []string
	// a machine-local table (runs, sessions, ...) lives in the machine
	// store whatever store it is listed from: its rows are edited there,
	// and listed once, not once per store
	machineDone := map[row.Table]bool{}
	for _, store0 := range st.eng.Stores() {
		if len(only) > 0 && !slices.Contains(only, store0) {
			continue
		}
		for _, def := range st.eng.Registry().Tables() {
			src := store0
			if st.eng.HasMachineStore() && st.eng.IsMachineLocal(def.Name) {
				if machineDone[def.Name] {
					continue
				}
				machineDone[def.Name], src = true, store.SourceMachine
			}
			ro := st.eng.ReadOnly(src)
			rows, err := st.eng.List(ctx, src, def.Name)
			if err != nil {
				return nil, nil, err
			}
			for _, r := range rows {
				if r.Deleted || (def.Name == schema.TableActors && skip[r.ID]) {
					continue
				}
				p := mapActorFields(r.Fields, m)
				if p == nil {
					continue
				}
				if ro {
					if !slices.Contains(readOnly, string(src)) {
						readOnly = append(readOnly, string(src))
					}
					continue
				}
				out = append(out, actorEdit{ref: engine.Ref{Store: src, Table: def.Name, ID: r.ID}, patch: p})
			}
		}
	}
	slices.SortFunc(out, func(x, y actorEdit) int {
		return cmp.Or(strings.Compare(string(x.ref.Store), string(y.ref.Store)), strings.Compare(string(x.ref.Table), string(y.ref.Table)), strings.Compare(x.ref.ID.String(), y.ref.ID.String()))
	})
	return out, readOnly, nil
}

// markMerged marks each writable copy of the merged actor's row: merged_into
// into, inactive, status left (TEAM_PROFILES 6.4's marking, which every
// report follows), staged in b with the merge's edits. A copy already
// marked is left alone. A store that keeps only an actor's id, name and
// kind (a repo or client store, F18) drops the Internal merged_into and
// stable_key on the write: that copy is then inactive and no longer
// carries the shared key; the roster's copy keeps the mark. It returns
// the copies staged.
func (a *App) markMerged(ctx context.Context, st *state, b *bulkWrite, copies []actorCopy, into ids.AnyID) int {
	mv, err := value.TextOf(into.String())
	if err != nil {
		return 0
	}
	left, err := value.TextOf(schema.ActorStatusLeft)
	if err != nil {
		return 0
	}
	n := 0
	for _, c := range copies {
		marked := fieldText(c.r, schema.FieldActorMergedInto) == into.String() ||
			(fieldText(c.r, schema.FieldActorActive) == "false" && fieldText(c.r, actorFieldKey) == "") // a store that keeps no merged_into: already inactive
		if st.eng.ReadOnly(c.src) || marked {
			continue
		}
		patch := map[string]value.Value{schema.FieldActorMergedInto: mv, schema.FieldActorActive: value.BoolOf(false), schema.FieldActorStatus: left}
		if b.update(engine.Ref{Store: c.src, Table: schema.TableActors, ID: c.r.ID}, patch) {
			n++
		}
	}
	return n
}

// rosterTombstone writes the merged actor's marked row into the org
// roster when the roster holds no copy of it (owner report 2026-11-12:
// the old id lived only in a client folder, whose actor copies keep no
// merged_into (F18), so nothing said where it went and `--created-by
// <kept id>` missed the rows its history stamps name). The roster's row
// carries the mark every read follows (engine mergedActors). It returns
// 1 when it staged the row.
func (a *App) rosterTombstone(ctx context.Context, st *state, b *bulkWrite, copies []actorCopy, into ids.AnyID) int {
	roster := a.actorRosterStore(ctx, st)
	if roster == "" || st.eng.ReadOnly(roster) || len(copies) == 0 {
		return 0
	}
	for _, c := range copies {
		if c.src == roster {
			return 0 // markMerged marks the roster's own copy
		}
	}
	f := copies[0].r
	fields := map[string]value.Value{}
	for k, v := range map[string]string{actorFieldKind: actorKindOf(fieldText(f, actorFieldKind)), actorFieldDisplay: fieldText(f, actorFieldDisplay),
		schema.FieldActorMergedInto: into.String(), schema.FieldActorStatus: schema.ActorStatusLeft} {
		if v == "" {
			continue
		}
		tv, err := value.TextOf(v)
		if err != nil {
			return 0
		}
		fields[k] = tv
	}
	fields[schema.FieldActorActive] = value.BoolOf(false)
	if b.create(roster, schema.TableActors, f.ID, fields) {
		return 1
	}
	return 0
}

// storeLabels names the stores of st as the merge's output lists them:
// a folder of an org store by the store that holds it and the folder
// ("git@host:org/notes-two.git/client_a", or "<org>/<folder>" for the
// store holding the org folder), never by the org's name alone (owner
// report 2026-11-12: the dry run named "<org>" for rows that live in the
// org's second store); other stores by StoreLabel.
func (a *App) storeLabels(ctx context.Context, st *state) func(store.Source) string {
	byFolder := map[string]string{}
	for _, f := range a.orgFolders(ctx).Folders {
		byFolder[f.Folder] = f.Label + targetSep + f.Folder
	}
	a.mu.Lock()
	folders := maps.Clone(st.folders)
	a.mu.Unlock()
	return func(src store.Source) string {
		if l, ok := byFolder[strings.Trim(folders[src], "/")]; ok {
			return l
		}
		return a.StoreLabel(src)
	}
}

// storeNames is srcs as text.
func storeNames(srcs []store.Source) []string {
	out := make([]string, 0, len(srcs))
	for _, s := range srcs {
		out = append(out, string(s))
	}
	return out
}

// DoctorCheckSharedKeys is the check's JSON name.
const DoctorCheckSharedKeys DoctorCheck = "actor_stable_key"

// keyActor is one live, unmerged actor as the check sees it.
type keyActor struct {
	id      ids.AnyID
	name    string
	kind    string
	key     string
	handle  string
	created string
	roster  bool
}

// sharedKeyNeedsYes ends a shared-key warning: the fix runs the merge
// only with --yes. The same words in every unmerged run, so the fix run's
// line and the re-check's match (a changed line would count as fixed).
const sharedKeyNeedsYes = "; needs --yes: `lore fix --yes` runs the merge"

// DoctorSharedKeys is the doctor check (registered in doctor_op.go). With
// fix and the caller's yes (op.Confirmed: `lore fix --yes`, `lore doctor
// --fix --yes`) it runs the merge the warning names (MergeActor, from
// each other id into keepOf's), one fixed line per merge naming what it
// did; a merge that fails leaves the warning as it was and adds a note
// with the reason. Only actors sharing one stable key are merged (one
// key is one person or one agent), never the likely-same-login notes.
func (a *App) DoctorSharedKeys(ctx context.Context, fix bool) ([]DoctorFinding, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	roster := a.actorRosterStore(ctx, st)
	byID := map[ids.AnyID]keyActor{}
	merged := map[ids.AnyID]bool{}
	for _, src := range st.eng.Stores() {
		rows, lerr := st.eng.List(ctx, src, schema.TableActors)
		if lerr != nil {
			continue // an unreadable store: the store checks report it
		}
		for _, r := range rows {
			if r.Deleted {
				continue
			}
			if fieldText(r, schema.FieldActorMergedInto) != "" {
				merged[r.ID] = true
				continue
			}
			k := keyActor{id: r.ID, name: fieldText(r, actorFieldDisplay), kind: actorKindOf(fieldText(r, actorFieldKind)), key: fieldText(r, actorFieldKey), handle: fieldText(r, actorFieldHandle),
				created: fieldText(r, row.KeyCreatedAt), roster: roster != "" && src == roster}
			have, ok := byID[r.ID]
			if !ok || (have.key == "" && k.key != "") {
				k.roster = k.roster || have.roster
				byID[r.ID] = k
			} else if k.roster {
				have.roster = true
				byID[r.ID] = have
			}
		}
	}
	byKey := map[string][]keyActor{}
	for id, k := range byID {
		if !merged[id] && k.key != "" {
			byKey[k.key] = append(byKey[k.key], k)
		}
	}
	var out []DoctorFinding
	for _, key := range slices.Sorted(maps.Keys(byKey)) {
		g := byKey[key]
		if len(g) < 2 {
			continue
		}
		into := keepOf(g)
		var fixes, names []string
		var froms []keyActor
		for _, k := range g {
			names = append(names, k.id.String())
			if k.id != into.id {
				fixes = append(fixes, "lore actor merge "+k.id.String()+" "+into.id.String())
				froms = append(froms, k)
			}
		}
		slices.Sort(names)
		warning := DoctorFinding{Check: DoctorCheckSharedKeys, Level: DoctorWarning, Store: roster,
			Message: plural(len(g), "actor") + " share the stable key " + key + " (" + strings.Join(names, ", ") + "): one identity with two ids, as an adopt or migrate by an older lore left it; merge into " +
				into.id.String() + " (asks first; rows naming the other are edited to name it, the other keeps its row marked merged_into)" + sharedKeyNeedsYes,
			Fix: strings.Join(fixes, "; ")}
		if !fix || !op.Confirmed(ctx) {
			out = append(out, warning)
			continue
		}
		out = append(out, a.mergeSharedKey(ctx, roster, key, into, froms, warning)...)
	}
	out = append(out, likelyHandleDuplicates(byID, merged)...)
	return out, nil
}

// mergeSharedKey runs the merges of one shared key (doctor --fix --yes):
// each of froms into into, in id order. A merged pair is a fixed line
// naming both ids and the rows it edited; a pair it cannot merge (two
// kinds, a refusal, a write that failed) keeps the warning as it was and
// adds a note with the reason.
func (a *App) mergeSharedKey(ctx context.Context, roster store.Source, key string, into keyActor, froms []keyActor, warning DoctorFinding) []DoctorFinding {
	slices.SortFunc(froms, func(x, y keyActor) int { return strings.Compare(x.id.String(), y.id.String()) })
	var out []DoctorFinding
	failed := false
	for _, f := range froms {
		if f.kind != into.kind {
			failed = true
			out = append(out, DoctorFinding{Check: DoctorCheckSharedKeys, Level: DoctorNote, Store: roster,
				Message: "not merged " + f.id.String() + " into " + into.id.String() + ": a " + f.kind + " and a " + into.kind + " share " + key + "; lore merges only one person's (or one agent's) ids"})
			continue
		}
		res, err := a.MergeActor(ctx, opsinfra.ActorMergeIn{From: f.id.String(), Into: into.id.String()})
		if err != nil {
			failed = true
			out = append(out, DoctorFinding{Check: DoctorCheckSharedKeys, Level: DoctorNote, Store: roster,
				Message: "not merged " + f.id.String() + " into " + into.id.String() + " (" + key + "): " + err.Error(),
				Fix:     "lore actor merge " + f.id.String() + " " + into.id.String()})
			continue
		}
		out = append(out, DoctorFinding{Check: DoctorCheckSharedKeys, Level: DoctorWarning, Store: roster, Fixed: true,
			Message: "merged " + f.id.String() + " into " + into.id.String() + " (stable key " + key + "): " + plural(res.Rows, "row") +
				" edited to name " + into.id.String() + ", " + copies(res.Marked) + " of " + f.id.String() + " marked merged_into (lore actor merge " + f.id.String() + " " + into.id.String() + ")"})
	}
	if failed {
		out = append(out, warning)
	}
	return out
}

// copies is "1 copy" / "n copies".
func copies(n int) string {
	if n == 1 {
		return "1 copy"
	}
	return strconv.Itoa(n) + " copies"
}

// keepOf is the actor a merge keeps: the roster's, else the one whose id
// the key derives (as every key-made actor's), else the oldest.
func keepOf(g []keyActor) keyActor {
	slices.SortFunc(g, func(x, y keyActor) int {
		return cmp.Or(strings.Compare(x.created, y.created), strings.Compare(x.id.String(), y.id.String()))
	})
	for _, k := range g {
		if k.roster {
			return k
		}
	}
	for _, k := range g {
		if id, err := identity.ActorID(k.key); err == nil && id.Any() == k.id {
			return k
		}
	}
	return g[0]
}

// likelyHandleDuplicates are the notes for a human actor keyed by a git
// host login (human:<login>, no email) whose login is another person's
// roster handle: likely one person, but a login is no proof, so it is
// never merged on its own.
func likelyHandleDuplicates(byID map[ids.AnyID]keyActor, merged map[ids.AnyID]bool) []DoctorFinding {
	byHandle := map[string]keyActor{}
	for id, k := range byID {
		if k.roster && k.handle != "" && !merged[id] {
			byHandle[strings.ToLower(k.handle)] = k
		}
	}
	var out []DoctorFinding
	for _, id := range slices.SortedFunc(maps.Keys(byID), func(x, y ids.AnyID) int { return strings.Compare(x.String(), y.String()) }) {
		k := byID[id]
		login, ok := strings.CutPrefix(k.key, identity.KindHuman+":")
		if !ok || login == "" || strings.Contains(login, "@") || merged[id] {
			continue
		}
		p, hit := byHandle[strings.ToLower(login)]
		if !hit || p.id == id || p.key == k.key {
			continue
		}
		out = append(out, DoctorFinding{Check: DoctorCheckSharedKeys, Level: DoctorNote,
			Message: id.String() + " (" + k.key + ", no email) is likely @" + p.handle + " " + p.id.String() + " (" + p.name + "): the login is that person's roster handle; lore does not merge on a login alone",
			Fix:     "if it is the same person: lore actor merge " + id.String() + " " + p.id.String()})
	}
	return out
}

// knownActorKeys maps the stable key of every live, unmerged actor the
// org roster holds to its id (an empty map with no org: a solo repo has
// no roster). A roster that cannot be read is an error naming it: the
// mapping must not guess.
func (a *App) knownActorKeys(ctx context.Context) (map[string]ids.AnyID, error) {
	out := map[string]ids.AnyID{}
	r, err := a.repoRoster(ctx)
	if err != nil || r.src == store.SourceRepo {
		return out, nil // no org roster here: only the folder's own actors count
	}
	x, done, err := a.rosterApp(ctx, r)
	if err != nil {
		return nil, errs.Prefix("read the org roster ("+r.where+")", err)
	}
	defer done()
	st, err := x.state(ctx)
	if err != nil {
		return nil, errs.Prefix("read the org roster ("+r.where+")", err)
	}
	rows, err := st.eng.List(ctx, r.src, schema.TableActors)
	if err != nil {
		return nil, errs.Prefix("read the org roster ("+r.where+")", err)
	}
	addActorKeys(out, rows)
	return out, nil
}

// addActorKeys adds each live, unmerged actor's stable key → id to m (the
// first id seen for a key stays: two are doctor's actor_stable_key).
func addActorKeys(m map[string]ids.AnyID, rows []row.Row) {
	slices.SortFunc(rows, func(x, y row.Row) int { return strings.Compare(x.ID.String(), y.ID.String()) })
	for _, r := range rows {
		k := fieldText(r, actorFieldKey)
		if r.Deleted || r.Table != schema.TableActors || k == "" || fieldText(r, schema.FieldActorMergedInto) != "" {
			continue
		}
		if _, ok := m[k]; !ok {
			m[k] = r.ID
		}
	}
}

// actorKeyMap maps each source actor whose stable key known holds under
// another id onto that id, with one line per mapping for the output:
// "act_A (human:x) → act_B".
func actorKeyMap(src []row.Row, known map[string]ids.AnyID) (map[string]ids.AnyID, []string) {
	m := map[string]ids.AnyID{}
	var lines []string
	for _, r := range src {
		k := fieldText(r, actorFieldKey)
		if r.Deleted || r.Table != schema.TableActors || k == "" {
			continue
		}
		if to, ok := known[k]; ok && to != r.ID {
			m[r.ID.String()] = to
			lines = append(lines, r.ID.String()+" ("+k+") → "+to.String())
		}
	}
	slices.Sort(lines)
	return m, lines
}

// StepActors is the migrate step that maps actors onto the roster's.
const StepActors = "actors"

// migrateActorKeys is `lore migrate run`'s mapping of the repo's actors
// onto the org roster's (owner report 2026-11-12): an actor of the repo
// whose stable key the roster holds under another id is merged into the
// roster's actor in the repo's own rows (every row naming it is edited to
// name the roster's; its row is marked as merge marks it), one step line
// per mapping. A repo with no org roster maps nothing; a rerun finds the
// marked rows and does nothing.
func (a *App) migrateActorKeys(ctx context.Context, dryRun bool) ([]string, int, error) {
	known, err := a.knownActorKeys(ctx)
	if err != nil || len(known) == 0 {
		return nil, 0, err
	}
	st, err := a.state(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := st.eng.List(ctx, store.SourceRepo, schema.TableActors)
	if err != nil {
		return nil, 0, err
	}
	var live []row.Row
	for _, r := range rows {
		if !r.Deleted && fieldText(r, schema.FieldActorMergedInto) == "" && fieldText(r, schema.FieldActorActive) != "false" {
			live = append(live, r)
		}
	}
	m, lines := actorKeyMap(live, known)
	if len(m) == 0 {
		return nil, 0, nil
	}
	skip := map[ids.AnyID]bool{}
	for from := range m {
		if id, perr := ids.ParseAny(from); perr == nil {
			skip[id] = true
		}
	}
	edits, _, err := actorRefEdits(ctx, st, m, skip, store.SourceRepo)
	if err != nil {
		return nil, 0, err
	}
	verb := "would map"
	if !dryRun {
		verb = "mapped"
		b := a.bulk(ctx, "migrate "+StepActors, nil) // one store commit for the step's rows
		for _, e := range edits {
			b.update(e.ref, e.patch)
		}
		for _, r := range live {
			if to, ok := m[r.ID.String()]; ok {
				a.markMerged(ctx, st, b, []actorCopy{{store.SourceRepo, r}}, to)
			}
		}
		cerr := b.commit()
		if ferr := b.failedErr("migrate: "+StepActors, "lore migrate run"); ferr != nil {
			return nil, 0, ferr
		}
		if cerr != nil {
			return nil, 0, cerr
		}
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, StepActors+": "+verb+" "+l+", the org roster's actor of the same identity")
	}
	if len(edits) > 0 {
		out = append(out, StepActors+": "+plural(len(edits), "row")+" of this repo name the roster's actor instead")
	}
	return out, len(m) + len(edits), nil
}
