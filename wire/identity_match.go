package wire

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/logic/actorsource"
	"github.com/thesatellite-ai/lore/logic/identmatch"
	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Identity matching at open (TEAM_PROFILES_DESIGN 6, TP-4): every git email
// and GitHub login a person flagged "use for identity" resolves to the same
// actor, never a duplicate.
//
// Flow:
//
//	open ──▶ resolveActor (identity.ActorID of the stable key; no store yet)
//	     ──▶ engine.New(WithActor) ──▶ refreshAll (caches readable)
//	     ──▶ canonicalActor: a person's key ──▶ identmatch.KeyOf
//	            identityInputs (actors, actor_contacts, contact_types of every
//	            attached store; client-store contacts marked, never used)
//	            ──▶ identmatch.Resolve ──▶ another actor?
//	                ──▶ engine.RebindActor + st.actor (before any write)
//	     ──▶ ensureActor writes the canonical actor's row (reboundRow), never
//	         one keyed by the second email
//
// The same inputs answer `lore actor register` (rosterContactMatch), the
// contact form of an actor reference (profileRoster.ResolveActor,
// ops/task actorRef through Env), `lore whoami` and the doctor check.

var _ opstask.ContactActorer = (*App)(nil)

// identityTables are the tables identity matching reads.
var identityTables = []row.Table{schema.TableActors, schema.TableActorContacts, schema.TableContactTypes}

// identityInputs is what identmatch needs, read from stores of st.
type identityInputs struct {
	types    identmatch.Types
	actors   []identmatch.Actor
	contacts []identmatch.Contact
	// rows are the actors rows by id (the first store that has it), for
	// display names and stable keys.
	rows map[ids.ID[ids.Actor]]row.Row
}

// readIdentityInputs lists the live actors, actor_contacts and
// contact_types rows of srcs on st's engine. A contact from a client-class
// store is kept but marked FromClient, so identmatch never uses it
// (TEAM_PROFILES 6.2 step 2; 6.5 target 3).
func readIdentityInputs(ctx context.Context, st *state, srcs []store.Source) (identityInputs, error) {
	in := identityInputs{rows: map[ids.ID[ids.Actor]]row.Row{}}
	var typeRows []teamprofile.ContactType
	for _, src := range srcs {
		client := st.eng.IsClient(src)
		for _, table := range identityTables {
			rows, err := st.eng.List(ctx, src, table)
			if err != nil {
				return identityInputs{}, errs.Prefix("wire: read "+string(table)+" of store "+string(src)+" for identity matching", err)
			}
			for _, r := range rows {
				if r.Deleted {
					continue
				}
				switch table {
				case schema.TableActors:
					in.addActor(r)
				case schema.TableActorContacts:
					if c, ok := contactOfRow(r, client); ok {
						in.contacts = append(in.contacts, c)
					}
				case schema.TableContactTypes:
					if !client {
						typeRows = append(typeRows, contactTypeOfRow(r))
					}
				}
			}
		}
	}
	in.types = identmatch.TypesOf(typeRows)
	return in, nil
}

// addActor records an actors row (the first copy of an id wins: srcs come
// nearest-first).
func (in *identityInputs) addActor(r row.Row) {
	id, err := ids.As[ids.Actor](r.ID)
	if err != nil {
		return
	}
	if _, seen := in.rows[id]; seen {
		return
	}
	in.rows[id] = r
	a := identmatch.Actor{ID: id}
	if m, merr := ids.Parse[ids.Actor](fieldText(r, actorFieldMergedInto)); merr == nil {
		a.MergedInto = m
	}
	in.actors = append(in.actors, a)
}

// actorFieldMergedInto is actors.merged_into (schema/builtin.go, Internal):
// the actor a duplicate was merged into (TEAM_PROFILES 6.4).
const actorFieldMergedInto = "merged_into"

// contactOfRow reads one actor_contacts row; ok false when it names no
// valid actor.
func contactOfRow(r row.Row, fromClient bool) (identmatch.Contact, bool) {
	a, err := ids.Parse[ids.Actor](fieldText(r, schema.FieldContactActor))
	if err != nil {
		return identmatch.Contact{}, false
	}
	return identmatch.Contact{ID: r.ID, Actor: a, Type: fieldText(r, schema.FieldContactType),
		ValueNorm: fieldText(r, schema.FieldContactValueNorm), Identity: identityFlag(r, schema.FieldContactIdentity), FromClient: fromClient}, true
}

// contactTypeOfRow reads one contact_types row.
func contactTypeOfRow(r row.Row) teamprofile.ContactType {
	return teamprofile.ContactType{Name: fieldText(r, schema.FieldContactTypeName), Kind: fieldText(r, schema.FieldContactTypeKind),
		Identity: identityFlag(r, schema.FieldContactTypeIdentity), Retired: identityFlag(r, schema.FieldContactTypeRetired)}
}

// boolTrue is the text of a true Bool (a row file's plain JSON may also
// read back as text).
const boolTrue = "true"

// identityFlag reads a bool field of a profile row: a Bool true, or its
// text form; absent or anything else is false (fail closed: a contact is
// an identity contact only when it says so).
func identityFlag(r row.Row, name string) bool { return fieldText(r, name) == boolTrue }

// resolve answers which actor key belongs to.
func (in identityInputs) resolve(key identmatch.Key) identmatch.Result {
	return identmatch.Resolve(key, in.types, in.actors, in.contacts)
}

// display is "Mira Patel (act_…)" for an actor of in, or its id alone.
func (in identityInputs) display(a ids.ID[ids.Actor]) string {
	if r, ok := in.rows[a]; ok && fieldText(r, actorFieldDisplay) != "" {
		return fieldText(r, actorFieldDisplay) + " (" + a.String() + ")"
	}
	return a.String()
}

// displays joins display for each actor.
func (in identityInputs) displays(as []ids.ID[ids.Actor]) string {
	parts := make([]string, len(as))
	for i, a := range as {
		parts[i] = in.display(a)
	}
	return strings.Join(parts, ", ")
}

// personKey reports whether st's identity is a person's own (not an agent,
// not CI or another automated kind, not an actor set by Options): only a
// person's key is matched at open (TEAM_PROFILES 6.3).
func personKey(st *state) bool {
	return st.who.StableKey != "" && st.who.Kind == identity.KindHuman
}

// canonicalActor is the TEAM_PROFILES 6.3 hook, called once by open right
// after refreshAll: when the caller's stable key is an identity contact of
// exactly one other actor in the attached stores, every write of this open
// is stamped as that actor (engine.RebindActor) and st.actor follows. No
// contact, or an ambiguous one (two people hold it: fail closed, notice
// names them), keeps the identity chain's own answer.
func (st *state) canonicalActor(ctx context.Context, notice func(string)) error {
	if !personKey(st) {
		return st.movedAgentOwner(ctx)
	}
	in, err := readIdentityInputs(ctx, st, st.eng.Stores())
	if err != nil {
		return err
	}
	if moved, ok := st.movedKeyActor(ctx); ok {
		if err := st.eng.RebindActor(moved.id); err != nil {
			return err
		}
		st.actor, st.who.Aliases = moved.id, moved.aliases
		return nil
	}
	if len(in.contacts) == 0 {
		return nil
	}
	key, ok := identmatch.KeyOf(st.who.StableKey, in.types)
	if !ok {
		return nil
	}
	res := in.resolve(key)
	if len(res.Ambiguous) > 0 && notice != nil {
		notice("identity contact " + key.String() + " is held by " + in.displays(res.Ambiguous) +
			"; writing as " + st.actor.String() + " (lore doctor names the fix)")
	}
	if !res.Matched() || res.Actor == st.actor {
		return nil
	}
	if err := st.eng.RebindActor(res.Actor); err != nil {
		return err
	}
	st.actor = res.Actor
	return nil
}

// movedActor is the caller's own actor found under an earlier key.
type movedActor struct {
	id      ids.ID[ids.Actor]
	aliases []string
}

// movedKeyActor finds the caller's actor after an email change made on
// another machine (SOURCE_OF_TRUTH, an email change keeps one person): a
// non-client store's actors row whose stable_key is the caller's key, under
// the id of its first key_aliases entry rather than the key's own id. Only
// when this machine's identity names no aliases itself (they win), and
// never for an agent or a client store's row (no key there).
func (st *state) movedKeyActor(ctx context.Context) (movedActor, bool) {
	if !personKey(st) || len(st.who.Aliases) > 0 {
		return movedActor{}, false
	}
	return st.movedActorOf(ctx, st.who.StableKey)
}

// movedAgentOwner is canonicalActor for a detected coding agent: when its
// owner's key is a moved actor's (the person changed email on another
// machine and this machine only knows the new one), the agent is derived
// again from the owner's kept id, so it writes as the same agent actor as
// before the change instead of a new one. The 6.1 width is settled again
// and every write of this open is stamped as that id.
func (st *state) movedAgentOwner(ctx context.Context) error {
	o := st.who.Owner
	if st.who.Step != identity.StepDetected || o == nil || o.Kind != identity.KindHuman || o.StableKey == "" || len(o.Aliases) > 0 {
		return nil
	}
	moved, ok := st.movedActorOf(ctx, o.StableKey)
	if !ok {
		return nil
	}
	owner := *o
	owner.Aliases = moved.aliases
	who, ok, err := identity.ReownAgent(st.who, owner)
	if err != nil || !ok {
		return err
	}
	id, err := identity.ActorID(who.StableKey)
	if err != nil {
		return err
	}
	st.who, st.actor = who, id
	if err = st.settleAgentKey(ctx, st.byName); err != nil {
		return err
	}
	return st.eng.RebindActor(st.actor)
}

// movedActorOf finds the actor a person's key moved to (movedKeyActor):
// a non-client store's actors row whose stable_key is key, under the id of
// its first key_aliases entry rather than the key's own id.
func (st *state) movedActorOf(ctx context.Context, key string) (movedActor, bool) {
	own, err := identity.ActorID(key)
	if err != nil {
		return movedActor{}, false
	}
	// every store's copy, not only the first per id: the repo's copy is
	// the F18 projection (no stable_key) and comes first
	for _, src := range st.eng.Stores() {
		if src == store.SourceMachine || st.eng.IsClient(src) {
			continue
		}
		rows, lerr := st.eng.List(ctx, src, schema.TableActors)
		if lerr != nil {
			continue // an unreadable store: the store checks report it
		}
		for _, r := range rows {
			if r.Deleted || r.ID == own.Any() || fieldText(r, actorFieldKey) != key {
				continue
			}
			aliases := actorsource.SplitAliases(fieldText(r, schema.FieldActorKeyAliases))
			if len(aliases) == 0 {
				continue
			}
			if first, ferr := identity.ActorID(aliases[0]); ferr == nil && first.Any() == r.ID {
				return movedActor{id: first, aliases: aliases}, true
			}
		}
	}
	return movedActor{}, false
}

// rebound reports whether st's writing actor is not the one its stable key
// names: canonicalActor found the key as another actor's identity contact.
func (st *state) rebound() bool {
	if !personKey(st) {
		return false
	}
	own, err := st.who.ActorID()
	return err == nil && own != st.actor
}

// reboundRow is the canonical actor's own row for ensureActor's rebound
// case: the nearest non-client store's copy, whose stable_key, handle and
// display name are written into a new store instead of the caller's key
// (so the second email never becomes a second actor row and
// UniqueOn(stable_key) never clashes). ok false when no store has it.
func (st *state) reboundRow(ctx context.Context) (row.Row, bool, error) {
	for _, src := range st.eng.Stores() {
		if st.eng.IsClient(src) {
			continue
		}
		rows, err := st.eng.List(ctx, src, schema.TableActors)
		if err != nil {
			return row.Row{}, false, err
		}
		for _, r := range rows {
			if !r.Deleted && r.ID == st.actor.Any() {
				return r, true, nil
			}
		}
	}
	return row.Row{}, false, nil
}

// identityContactLine is `lore whoami`'s identity-contact line (6.3):
// "identity contact: mira.patel@gmail.com is an identity contact of Mira
// Patel (act_…)", or "" when the caller's key is no identity contact of
// another actor. An ambiguous key says so.
func (st *state) identityContactLine(ctx context.Context) (string, error) {
	if !personKey(st) {
		return "", nil
	}
	in, err := readIdentityInputs(ctx, st, st.eng.Stores())
	if err != nil {
		return "", err
	}
	key, ok := identmatch.KeyOf(st.who.StableKey, in.types)
	if !ok {
		return "", nil
	}
	res := in.resolve(key)
	switch {
	case len(res.Ambiguous) > 0:
		return identityContactPrefix + key.ValueNorm + " is an identity contact of " + in.displays(res.Ambiguous) + " (two people: lore doctor)", nil
	case res.Matched():
		own, _ := st.who.ActorID()
		if res.Actor == own {
			return "", nil // the key's own actor holds it: nothing to say
		}
		return identityContactPrefix + key.ValueNorm + " is an identity contact of " + in.display(res.Actor), nil
	}
	return "", nil
}

// actorByContact resolves an actor reference in contact form (email:…,
// github:…, <type>:<value>, a bare email, human:<email>) against srcs of
// st: the TEAM_PROFILES 6.3 fallback after the id, stable-key and handle
// forms. ok false when ref is no contact form or nobody holds it; two
// holders is E_AMBIGUOUS_ID naming them.
func (st *state) actorByContact(ctx context.Context, srcs []store.Source, ref string) (row.Row, bool, error) {
	in, err := readIdentityInputs(ctx, st, srcs)
	if err != nil {
		return row.Row{}, false, err
	}
	return in.byContact(ref)
}

// byContact is actorByContact on inputs already read.
func (in identityInputs) byContact(ref string) (row.Row, bool, error) {
	key, ok := identmatch.KeyOf(ref, in.types)
	if !ok {
		return row.Row{}, false, nil
	}
	res := in.resolve(key)
	if len(res.Ambiguous) > 0 {
		return row.Row{}, false, errs.New(errs.AmbiguousID, "actor "+ref+" is an identity contact of "+in.displays(res.Ambiguous)).
			WithHint("name one by its act_ id; lore doctor names the duplicate contact")
	}
	if !res.Matched() {
		return row.Row{}, false, nil
	}
	r, found := in.rows[res.Actor]
	return r, found, nil
}

// reboundFields are the fields of the canonical actor's row that
// ensureActor writes into target when the open was rebound (6.3): its
// kind, display name and, where target may hold them (fullActorRow), its
// own stable_key and handle, never the caller's second key. Without a
// copy of the canonical row in reach, a person's minimal row is written
// (kind and the identity's display name).
func (st *state) reboundFields(ctx context.Context, target store.Source) (map[string]string, error) {
	vals := map[string]string{actorFieldKind: schema.ActorKindHuman, actorFieldDisplay: st.who.DisplayName}
	r, ok, err := st.reboundRow(ctx)
	if err != nil || !ok {
		return vals, err
	}
	for _, k := range []string{actorFieldKind, actorFieldDisplay} {
		if v := fieldText(r, k); v != "" {
			vals[k] = v
		}
	}
	if fullActorRow(st, target) {
		vals[actorFieldKey], vals[actorFieldHandle] = fieldText(r, actorFieldKey), fieldText(r, actorFieldHandle)
	}
	return vals, nil
}

// rosterContactMatch answers `lore actor register` for a person whose
// stable key is an identity contact of another actor on roster r (6.3):
// ok true with that actor, already registered (Created false), and nothing
// written; the stable_key of that actor is never replaced by the second
// key. ok false when the key is no such contact (or is its own actor's),
// so register goes on as before. An ambiguous key is E_AMBIGUOUS_ID: lore
// does not register a third actor while two people hold the contact.
func (a *App) rosterContactMatch(ctx context.Context, r rosterAt, stableKey string) (opsinfra.ActorRegisterOut, bool, error) {
	st, err := a.rosterState(ctx, r.src, false)
	if err != nil {
		return opsinfra.ActorRegisterOut{}, false, err
	}
	in, err := readIdentityInputs(ctx, st, []store.Source{r.src})
	if err != nil {
		return opsinfra.ActorRegisterOut{}, false, err
	}
	key, ok := identmatch.KeyOf(stableKey, in.types)
	if !ok {
		return opsinfra.ActorRegisterOut{}, false, nil
	}
	res := in.resolve(key)
	if len(res.Ambiguous) > 0 {
		return opsinfra.ActorRegisterOut{}, false, errs.New(errs.AmbiguousID, "identity contact "+key.String()+" is held by "+in.displays(res.Ambiguous)).
			WithHint("lore doctor names the duplicate contact; remove it from the wrong person first")
	}
	own, err := identity.ActorID(stableKey)
	if err != nil || !res.Matched() || res.Actor == own {
		return opsinfra.ActorRegisterOut{}, false, nil
	}
	x := in.rows[res.Actor]
	return opsinfra.ActorRegisterOut{ID: res.Actor.String(), Kind: opsinfra.RegisterKindHuman, Name: fieldText(x, actorFieldDisplay),
		Handle: fieldText(x, actorFieldHandle), Created: false, Where: r.shown(), ViaContact: key.String()}, true, nil
}

// identityContactPrefix starts whoami's identity-contact line; the JSON
// field identity_contact carries the rest.
const identityContactPrefix = "identity contact: "

// IdentityContactLine is `lore whoami`'s identity-contact line for the
// caller (TEAM_PROFILES 6.3; identityContactLine), read on a read state.
func (a *App) IdentityContactLine(ctx context.Context) (string, error) {
	st, err := a.state(ctx)
	if err != nil {
		return "", err
	}
	return st.identityContactLine(ctx)
}

// ActorByContact implements ops/task.ContactActorer: an actor reference in
// contact form resolved through the identity contacts of every attached
// store (TEAM_PROFILES 6.3), so `--assign github:mirap` works; a
// reference whose only holder is in the trash is E_IN_TRASH.
func (a *App) ActorByContact(ctx context.Context, ref string) (ids.ID[ids.Actor], bool, error) {
	st, err := a.state(ctx)
	if err != nil {
		return ids.ID[ids.Actor]{}, false, err
	}
	r, ok, err := st.actorByContact(ctx, st.eng.Stores(), ref)
	if err == nil && !ok {
		// A removed identity contact answers E_IN_TRASH with the restore
		// hint, as ResolveActor does on the roster (DELETION 3.2).
		err = st.trashedContact(ctx, st.eng.Stores(), ref)
	}
	if err != nil || !ok {
		return ids.ID[ids.Actor]{}, false, err
	}
	id, err := ids.As[ids.Actor](r.ID)
	return id, err == nil, err
}
