// Package identmatch answers "which actor is this identity?" from actor
// rows and identity contact rows (TEAM_PROFILES_DESIGN 6, TP-4): every git
// email and GitHub login flagged "use for identity" of one person resolves
// to the same actor, never a duplicate.
//
//	KeyOf       an actor reference or stable key ──▶ (type, value_norm)
//	Resolve     key + actors + contacts ──▶ one actor, no match, or ambiguous
//	CheckAdd    may this identity contact be added to that actor? (6.4)
//	Duplicates  identity contacts held by two actors (lore doctor, 6.4)
//
// Pure (layer L1): no I/O. Callers pass the live rows they read, each
// contact marked with whether it came from a client-class store; contacts
// from a client store are never used (they should not exist there, TP-7,
// and a client must never steer who a write is attributed to).
//
// Fail closed: when one identity contact is held by two actors, lore does
// not guess which person wrote a row; Resolve answers no match plus the
// candidates, and the caller keeps its own answer.
package identmatch

import (
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/ids"
)

// Stable-key kinds (adapters/identity KindHuman, KindAgent; this package
// sits below adapters, so the closed values are named here).
const (
	keyKindHuman = "human"
	// keySep separates a reference's kind or type from its value
	// ("human:mira@x", "github:mirap").
	keySep = ":"
	// emailAt marks a bare email reference ("mira@x"); a leading @ is a
	// handle reference ("@mira"), never an email.
	emailAt = "@"
)

// Type is what matching needs of a contact type: its normalisation kind
// and whether contacts of it may be identity contacts.
type Type struct {
	Kind     string
	Identity bool
}

// Types is the effective contact type list by name, as matching sees it.
type Types map[string]Type

// TypesOf lays contact_types rows over the built-in list (TS-2): a row
// adds a type or changes one, except that a built-in identity type
// (github, gitlab, email) always keeps its kind and identity flag, because
// identity matching depends on them (TEAM_PROFILES 2.3). A retired type
// still matches: its existing contacts keep it.
func TypesOf(rows []teamprofile.ContactType) Types {
	out := Types{}
	for _, b := range teamprofile.BuiltinContactTypes() {
		out[b.Name] = Type{Kind: b.Kind, Identity: b.Identity}
	}
	for _, r := range rows {
		if b, ok := out[r.Name]; ok && b.Identity && isBuiltin(r.Name) {
			continue
		}
		out[r.Name] = Type{Kind: r.Kind, Identity: r.Identity}
	}
	return out
}

// isBuiltin reports whether name is a compiled-in contact type.
func isBuiltin(name string) bool {
	for _, b := range teamprofile.BuiltinContactTypes() {
		if b.Name == name {
			return true
		}
	}
	return false
}

// Key is one identity: a contact type and a normalised value.
type Key struct {
	Type      string
	ValueNorm string
}

// String is "<type>:<value_norm>", the reference form lore prints.
func (k Key) String() string { return k.Type + keySep + k.ValueNorm }

// KeyOf reads an identity key from ref (TEAM_PROFILES 6.2 step 1):
//
//	human:<email>    a person's stable key       ──▶ (email, Normalize(email))
//	<type>:<value>   any identity contact type   ──▶ (type, Normalize(kind, value))
//	<email>          a bare email                ──▶ (email, Normalize(email))
//
// ok is false when ref is no identity key: an act_ id, an @handle, a
// display name, an agent or other non-human stable key, a type that is not
// an identity type, or a value its kind's check refuses (a reference that
// does not parse cannot name anyone, so it is no match rather than an
// error).
func KeyOf(ref string, types Types) (Key, bool) {
	ref = strings.TrimSpace(ref)
	if kind, val, has := strings.Cut(ref, keySep); has {
		if kind == keyKindHuman {
			return keyFor(teamprofile.ContactTypeEmail, val, types)
		}
		return keyFor(kind, val, types)
	}
	if strings.HasPrefix(ref, emailAt) || !strings.Contains(ref, emailAt) {
		return Key{}, false
	}
	return keyFor(teamprofile.ContactTypeEmail, ref, types)
}

// keyFor normalises value as an identity contact of type typ.
func keyFor(typ, value string, types Types) (Key, bool) {
	t, ok := types[typ]
	if !ok || !t.Identity {
		return Key{}, false
	}
	norm, err := teamprofile.Normalize(t.Kind, value)
	if err != nil {
		return Key{}, false
	}
	return Key{Type: typ, ValueNorm: norm}, true
}

// Actor is what matching needs of an actors row.
type Actor struct {
	ID ids.ID[ids.Actor]
	// MergedInto is merged_into: the actor this one was merged into (zero
	// when none). Matching follows the chain to its end.
	MergedInto ids.ID[ids.Actor]
}

// Contact is what matching needs of a live actor_contacts row.
type Contact struct {
	ID        ids.AnyID
	Actor     ids.ID[ids.Actor]
	Type      string
	ValueNorm string
	Identity  bool
	// FromClient marks a row read from a client-class store; such a row is
	// never used for matching (TEAM_PROFILES 6.2 step 2).
	FromClient bool
}

// matches reports whether c is a usable identity contact for key.
func (c Contact) matches(key Key, types Types) bool {
	return c.usable(types) && c.Type == key.Type && c.ValueNorm == key.ValueNorm
}

// usable: flagged for identity, of an identity type, not from a client
// store, naming an actor.
func (c Contact) usable(types Types) bool {
	return c.Identity && !c.FromClient && !c.Actor.IsZero() && types[c.Type].Identity
}

// Follow returns the end of id's merged_into chain among actors (id itself
// when it has none). A cycle stops at the first actor seen twice, so a
// broken chain never loops.
func Follow(id ids.ID[ids.Actor], actors []Actor) ids.ID[ids.Actor] {
	next := make(map[ids.ID[ids.Actor]]ids.ID[ids.Actor], len(actors))
	for _, a := range actors {
		if !a.MergedInto.IsZero() {
			next[a.ID] = a.MergedInto
		}
	}
	seen := map[ids.ID[ids.Actor]]bool{id: true}
	for {
		n, ok := next[id]
		if !ok || seen[n] {
			return id
		}
		seen[n] = true
		id = n
	}
}

// Result is Resolve's answer.
type Result struct {
	// Actor is the one actor the key resolves to (merged_into followed);
	// zero when there is no match or more than one.
	Actor ids.ID[ids.Actor]
	// Contact is the matching contact row (the first, by id) when Actor is
	// set: what `lore whoami` names.
	Contact ids.AnyID
	// Ambiguous lists the distinct actors holding the key when there are
	// two or more (sorted); Actor is zero then.
	Ambiguous []ids.ID[ids.Actor]
}

// Matched reports whether the key resolved to exactly one actor.
func (r Result) Matched() bool { return !r.Actor.IsZero() }

// Resolve answers which actor key belongs to (TEAM_PROFILES 6.2): the live
// identity contacts of an identity type with that (type, value_norm), not
// from a client store, each holder followed along merged_into. One actor
// is a match; none is no match (the caller keeps its own answer); two or
// more is no match with Ambiguous set (fail closed, never the first one).
// Left and archived people still match: the caller does not filter them.
func Resolve(key Key, types Types, actors []Actor, contacts []Contact) Result {
	holders := map[ids.ID[ids.Actor]]ids.AnyID{}
	for _, c := range sortedContacts(contacts) {
		if !c.matches(key, types) {
			continue
		}
		a := Follow(c.Actor, actors)
		if _, seen := holders[a]; !seen {
			holders[a] = c.ID
		}
	}
	switch len(holders) {
	case 0:
		return Result{}
	case 1:
		for a, c := range holders {
			return Result{Actor: a, Contact: c}
		}
	}
	return Result{Ambiguous: sortedActors(holders)}
}

// Held returns the first contact of contacts (by id) that would answer
// key as Resolve reads it: an identity contact of an identity type, not
// from a client store, naming an actor, with key's type and value_norm.
// ok is false when none does. Callers pass rows Resolve never sees: the
// trashed contacts of a store, so a reference whose only holder was
// removed answers E_IN_TRASH with the restore hint instead of "no actor"
// (DELETION 3.2). merged_into does not matter here: the caller names the
// contact row, not its actor.
func Held(key Key, types Types, contacts []Contact) (Contact, bool) {
	for _, c := range sortedContacts(contacts) {
		if c.matches(key, types) {
			return c, true
		}
	}
	return Contact{}, false
}

// Outcome is CheckAdd's verdict.
type Outcome int

// Outcomes of CheckAdd (TEAM_PROFILES 3.2, 6.4).
const (
	// AddFree: nobody else holds the key; the contact may be added.
	AddFree Outcome = iota + 1
	// AddAlready: the target already holds the key as an identity contact
	// (adding it again is the "already there" no-op).
	AddAlready
	// AddTaken: another actor holds the key as an identity contact:
	// E_CONTACT_TAKEN. Holder names it. --merge does not apply.
	AddTaken
	// AddMergeable: the key's own actor row exists (a duplicate made
	// before the contact was known): E_CONTACT_TAKEN with the hint
	// --merge, which sets Holder's merged_into to the target.
	AddMergeable
)

// AddCheck is the question CheckAdd answers.
type AddCheck struct {
	// Target is the actor the identity contact is being added to.
	Target ids.ID[ids.Actor]
	Key    Key
	// KeyActor is the actor row the key itself names, when such a row
	// exists: identity.ActorID("human:"+email) for an email key (the caller
	// computes it and checks the row; this package sits below adapters).
	// Zero when there is no such row or the key is not an email.
	KeyActor ids.ID[ids.Actor]
}

// Verdict is CheckAdd's answer.
type Verdict struct {
	Outcome Outcome
	// Holder is the other actor for AddTaken and AddMergeable.
	Holder ids.ID[ids.Actor]
	// Contact is the contact row that holds the key (AddAlready,
	// AddTaken); zero for AddMergeable found through KeyActor.
	Contact ids.AnyID
}

// CheckAdd decides whether an identity contact may be added to c.Target
// (TEAM_PROFILES 3.2, 6.4). Actors are compared at the end of their
// merged_into chains, so a contact held by a duplicate already merged into
// the target is the target's own. Order: a holder that is another person
// is AddTaken (an identity contact is never silently moved from one actor
// to another); then the target's own contact is AddAlready; then an
// existing key actor row that is not the target is AddMergeable.
func CheckAdd(c AddCheck, types Types, actors []Actor, contacts []Contact) Verdict {
	target := Follow(c.Target, actors)
	var own ids.AnyID
	for _, x := range sortedContacts(contacts) {
		if !x.matches(c.Key, types) {
			continue
		}
		holder := Follow(x.Actor, actors)
		if holder != target {
			return Verdict{Outcome: AddTaken, Holder: holder, Contact: x.ID}
		}
		if own.IsZero() {
			own = x.ID
		}
	}
	if !own.IsZero() {
		return Verdict{Outcome: AddAlready, Contact: own}
	}
	if !c.KeyActor.IsZero() {
		if k := Follow(c.KeyActor, actors); k != target {
			return Verdict{Outcome: AddMergeable, Holder: k}
		}
	}
	return Verdict{Outcome: AddFree}
}

// Duplicate is one identity key held by two or more actors.
type Duplicate struct {
	Key      Key
	Actors   []ids.ID[ids.Actor] // sorted, merged_into followed
	Contacts []ids.AnyID         // the contact rows holding it, sorted
}

// Duplicates lists every identity key held by two or more distinct actors
// (TEAM_PROFILES 6.4): after a merge of two clones each added the same
// identity contact to a different person. Holders are compared at the end
// of their merged_into chains, so a merged duplicate is not reported. The
// pure function behind the `lore doctor` check; sorted by key.
func Duplicates(types Types, actors []Actor, contacts []Contact) []Duplicate {
	byKey := map[Key][]Contact{}
	for _, c := range sortedContacts(contacts) {
		if c.usable(types) {
			k := Key{Type: c.Type, ValueNorm: c.ValueNorm}
			byKey[k] = append(byKey[k], c)
		}
	}
	var out []Duplicate
	for k, cs := range byKey {
		holders := map[ids.ID[ids.Actor]]ids.AnyID{}
		var rows []ids.AnyID
		for _, c := range cs {
			holders[Follow(c.Actor, actors)] = c.ID
			rows = append(rows, c.ID)
		}
		if len(holders) < 2 {
			continue
		}
		out = append(out, Duplicate{Key: k, Actors: sortedActors(holders), Contacts: rows})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key.String() < out[j].Key.String() })
	return out
}

// sortedContacts is contacts ordered by id, so every answer is the same
// whatever order the stores listed them in.
func sortedContacts(cs []Contact) []Contact {
	out := append([]Contact(nil), cs...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	return out
}

// sortedActors is the keys of m, sorted.
func sortedActors(m map[ids.ID[ids.Actor]]ids.AnyID) []ids.ID[ids.Actor] {
	out := make([]ids.ID[ids.Actor], 0, len(m))
	for a := range m {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}
