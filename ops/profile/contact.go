package profile

import (
	"context"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/logic/identmatch"
	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
)

// actor.contact.add|edit|remove|list (TEAM_PROFILES 2.2, 3.2, 6.4):
//
//	add ──▶ resolve <actor> ──▶ type live in the effective list (E_CONTACT_TYPE)
//	    ──▶ Normalize(kind, value) (E_CONTACT_TYPE naming the check)
//	    ──▶ same (actor, type, value_norm) live? "already there", no write
//	    ──▶ --identity: type allows it (E_CONTACT_TYPE); no other actor holds
//	        it (E_CONTACT_TAKEN); no older actor row of that email
//	        (E_CONTACT_TAKEN, or --merge: preview, confirm, merged_into)
//	    ──▶ primary: --primary, or the first of its type (TP-3); the others
//	        of (actor, type) cleared first ──▶ Create ──▶ Push
//
// Labels are free text (TP-2): the suggestions are help text, never a
// check. Values are stored and shown plain (TP-6).

// humanKeyPrefix is the stable-key form of a person's email
// (identity.ActorID("human:"+email), the id v0.1.17 gives that actor).
const humanKeyPrefix = "human:"

// typeValueSep joins a contact reference <type>:<value> (section 3).
const typeValueSep = ":"

// defaultRemoveReason is the trash reason of contact remove without
// --reason.
const defaultRemoveReason = "removed with lore actor contact remove"

// ContactAddIn is the input of actor.contact.add.
type ContactAddIn struct {
	Actor    string `json:"actor" arg:"0" desc:"the person: @handle, act_ id or stable key"`
	Type     string `json:"type" arg:"1" desc:"a contact type (lore contact-type list): github, email, phone, …"`
	Value    string `json:"value" arg:"2" desc:"as typed: mira@acme.com, mirap, +90 532 000 0000"`
	Label    string `json:"label,omitempty" flag:"label" desc:"free text; suggested: personal, work, client-<name>"`
	Primary  bool   `json:"primary,omitempty" flag:"primary" desc:"the primary contact of its type (one per type; the first one is primary by default)"`
	Identity bool   `json:"identity,omitempty" flag:"identity" desc:"use for identity: git emails and logins resolve to this person (email, github, gitlab)"`
	Note     string `json:"note,omitempty" flag:"note" desc:"free text"`
	Merge    bool   `json:"merge,omitempty" flag:"merge" desc:"an older actor row exists for this email: merge it into this person (asks first)"`
}

// ContactOut is one contact after a write.
type ContactOut struct {
	Contact ContactRow `json:"contact"`
	// Verb is added, edited or "already there".
	Verb string `json:"verb"`
	// Merged is the actor id merged into this person by --merge.
	Merged string `json:"merged,omitempty"`
	Where  string `json:"where"`
	// Warnings are the engine's warnings of the write
	// (profilestore.WarningLine), printed after the success line.
	Warnings []string `json:"warnings,omitempty"`
}

// Verbs of ContactOut (verbAdded and verbEdited are shared with the
// contact types).
const verbAlreadyThere = "already there"

// Text is "✓ acn_… github mirap added to Mira Patel (@mira) [primary, identity] in agency org/".
func (o ContactOut) Text() string { return o.text() + profilestore.WarningText(o.Warnings) }

// text is Text without the warnings.
func (o ContactOut) text() string {
	c := o.Contact
	var b strings.Builder
	mark := work.Success
	if o.Verb == verbAlreadyThere {
		mark = "·"
	}
	b.WriteString(mark + " " + c.ID + " " + c.Type + " " + c.Value + " " + o.Verb)
	if o.Verb != verbAlreadyThere {
		b.WriteString(" for " + c.ActorName)
	}
	if fl := c.flags(); fl != "" {
		b.WriteString(" [" + fl + "]")
	}
	b.WriteString(" in " + o.Where + "\n")
	if o.Merged != "" {
		b.WriteString("  merged " + o.Merged + " into " + c.ActorID + " (its rows are not rewritten; reports follow merged_into)\n")
	}
	return b.String()
}

// ContactRow is a contact in output: the row with its actor's name and
// the type's link. Plain, never masked (TP-6).
type ContactRow struct {
	ID        string `json:"id"`
	ActorID   string `json:"actor_id"`
	ActorName string `json:"actor_name"`
	Type      string `json:"type"`
	Value     string `json:"value"`
	ValueNorm string `json:"value_norm"`
	Label     string `json:"label,omitempty"`
	Primary   bool   `json:"primary"`
	Identity  bool   `json:"identity"`
	Note      string `json:"note,omitempty"`
	// URL is the type's url_template with value_norm, when it has one.
	URL string `json:"url,omitempty"`
	// CreatedBy / UpdatedBy are the row envelope's writers (INTEG-AUDIT-1,
	// auditrec), last so the older keys keep their order.
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// flags is "primary, identity" (the set ones).
func (c ContactRow) flags() string {
	var f []string
	if c.Primary {
		f = append(f, schema.FieldContactPrimary)
	}
	if c.Identity {
		f = append(f, schema.FieldContactIdentity)
	}
	return strings.Join(f, ", ")
}

// contactRow renders x; actors names the actor, types links the value.
func contactRow(x row.Row, actors map[string]row.Row, types []EffectiveType) ContactRow {
	c := ContactRow{
		ID: x.ID.String(), ActorID: text(x, schema.FieldContactActor), Type: text(x, schema.FieldContactType),
		Value: text(x, schema.FieldContactValue), ValueNorm: text(x, schema.FieldContactValueNorm),
		Label: text(x, schema.FieldContactLabel), Primary: flag(x, schema.FieldContactPrimary),
		Identity: flag(x, schema.FieldContactIdentity), Note: text(x, schema.FieldContactNote),
		CreatedBy: auditrec.Of(x).CreatedBy, UpdatedBy: auditrec.Of(x).UpdatedBy,
	}
	if a, ok := actors[c.ActorID]; ok {
		c.ActorName = actorName(a)
	}
	if t, ok := findType(types, c.Type); ok && t.URLTemplate != "" {
		// value_norm, not the value as typed: a handle pasted as a profile
		// URL or with a leading @ must still link to one page.
		c.URL = strings.ReplaceAll(t.URLTemplate, teamprofile.URLValue, c.ValueNorm)
	}
	return c
}

// rosterView is what a contact write reads once: the actors by id, every
// live contact and the effective types.
type rosterView struct {
	actors   map[string]row.Row
	contacts []row.Row
	types    []EffectiveType
}

func loadView(ctx context.Context, r profilestore.Roster) (rosterView, error) {
	as, err := r.List(ctx, schema.TableActors)
	if err != nil {
		return rosterView{}, err
	}
	cs, err := r.List(ctx, schema.TableActorContacts)
	if err != nil {
		return rosterView{}, err
	}
	ts, err := loadTypes(ctx, r)
	if err != nil {
		return rosterView{}, err
	}
	v := rosterView{actors: make(map[string]row.Row, len(as)), contacts: cs, types: ts}
	for _, a := range as {
		v.actors[a.ID.String()] = a
	}
	return v, nil
}

// identityVerdict asks identmatch.CheckAdd (TEAM_PROFILES 3.2, 6.4, TP-4)
// whether (typ, norm) may be an identity contact of actorID. It is the one
// identity-collision decision: identity matching, the doctor check and
// these ops all read it, so they can never disagree about who holds a key.
// The key actor is the email's own actor row (identity.ActorID of
// human:<email>, a duplicate made before the contact was known) when that
// row exists.
func (v rosterView) identityVerdict(actorID, typ, kind, norm string) (identmatch.Verdict, error) {
	target, err := ids.Parse[ids.Actor](actorID)
	if err != nil {
		return identmatch.Verdict{}, errs.Wrap(errs.Internal, "actor id "+actorID, err)
	}
	check := identmatch.AddCheck{Target: target, Key: identmatch.Key{Type: typ, ValueNorm: norm}}
	if kind == schema.ContactKindEmail {
		id, kerr := identity.ActorID(humanKeyPrefix + norm)
		if kerr != nil {
			return identmatch.Verdict{}, errs.Wrap(errs.Internal, "actor id of "+humanKeyPrefix+norm, kerr)
		}
		if _, ok := v.actors[id.String()]; ok {
			check.KeyActor = id
		}
	}
	types := make([]teamprofile.ContactType, 0, len(v.types))
	for _, t := range v.types {
		types = append(types, t.ContactType)
	}
	actors := make([]identmatch.Actor, 0, len(v.actors))
	for _, a := range v.actors {
		id, aerr := ids.As[ids.Actor](a.ID)
		if aerr != nil {
			continue // not an actor id: never a holder
		}
		m, _ := ids.Parse[ids.Actor](text(a, schema.FieldActorMergedInto)) // empty or bad: no chain
		actors = append(actors, identmatch.Actor{ID: id, MergedInto: m})
	}
	contacts := make([]identmatch.Contact, 0, len(v.contacts))
	for _, c := range v.contacts {
		holder, herr := ids.Parse[ids.Actor](text(c, schema.FieldContactActor))
		if herr != nil {
			continue
		}
		contacts = append(contacts, identmatch.Contact{ID: c.ID, Actor: holder, Type: text(c, schema.FieldContactType),
			ValueNorm: text(c, schema.FieldContactValueNorm), Identity: flag(c, schema.FieldContactIdentity)})
	}
	return identmatch.CheckAdd(check, identmatch.TypesOf(types), actors, contacts), nil
}

// contactByID is the live contact row id from the view.
func (v rosterView) contactByID(id ids.AnyID) row.Row {
	for _, c := range v.contacts {
		if c.ID == id {
			return c
		}
	}
	return row.Row{ID: id}
}

// takenErr is E_CONTACT_TAKEN for (typ, value) held by holders.
func takenErr(v rosterView, typ, value string, holders []row.Row) error {
	var who []string
	for _, c := range holders {
		aid := text(c, schema.FieldContactActor)
		n := aid
		if a, ok := v.actors[aid]; ok {
			n = actorName(a) + " " + aid
		}
		who = append(who, n+" ("+c.ID.String()+")")
	}
	return errs.New(errs.ContactTaken, "identity contact "+typ+typeValueSep+value+" already belongs to "+strings.Join(who, ", ")).
		WithHint("lore actor show --id " + typ + typeValueSep + value + " to see who has it; if it is on the wrong person, lore actor contact remove --id <acn_id> there")
}

// sameContact is the live contact of (actorID, typ, norm) other than skip.
func (v rosterView) sameContact(actorID, typ, norm string, skip ids.AnyID) (row.Row, bool) {
	for _, c := range v.contacts {
		if c.ID != skip && text(c, schema.FieldContactActor) == actorID && text(c, schema.FieldContactType) == typ &&
			text(c, schema.FieldContactValueNorm) == norm {
			return c, true
		}
	}
	return row.Row{}, false
}

// ofType is actorID's live contacts of typ other than skip.
func (v rosterView) ofType(actorID, typ string, skip ids.AnyID) []row.Row {
	var out []row.Row
	for _, c := range v.contacts {
		if c.ID != skip && text(c, schema.FieldContactActor) == actorID && text(c, schema.FieldContactType) == typ {
			out = append(out, c)
		}
	}
	return out
}

// clearOtherPrimaries writes primary false on every other primary of
// (actorID, typ) (TP-3: at most one). It runs BEFORE the new primary is
// written, so a crash in between leaves no primary rather than two.
func clearOtherPrimaries(ctx context.Context, r profilestore.Roster, v rosterView, actorID, typ string, keep ids.AnyID) (bool, error) {
	wrote := false
	for _, c := range v.ofType(actorID, typ, keep) {
		if !flag(c, schema.FieldContactPrimary) {
			continue
		}
		if _, err := r.Update(ctx, schema.TableActorContacts, c.ID, map[string]value.Value{schema.FieldContactPrimary: value.BoolOf(false)}); err != nil {
			return wrote, err
		}
		wrote = true
	}
	return wrote, nil
}

func contactAdd(ctx context.Context, env Env, in ContactAddIn) (ContactOut, error) {
	typ := strings.TrimSpace(in.Type)
	if strings.TrimSpace(in.Value) == "" {
		return ContactOut{}, errs.New(errs.InvalidInput, "actor contact add: an empty value")
	}
	var out ContactOut
	err := openWrite(ctx, env, &out.Warnings, func(r profilestore.Roster) (bool, error) {
		a, err := r.ResolveActor(ctx, strings.TrimSpace(in.Actor))
		if err != nil {
			return false, err
		}
		v, err := loadView(ctx, r)
		if err != nil {
			return false, err
		}
		t, err := typeForAdd(v.types, typ)
		if err != nil {
			return false, err
		}
		norm, err := teamprofile.Normalize(t.Kind, in.Value)
		if err != nil {
			return false, err
		}
		if in.Identity && !t.Identity {
			return false, notIdentityType(t)
		}
		aid := a.ID.String()
		if x, ok := v.sameContact(aid, t.Name, norm, ids.AnyID{}); ok {
			out = ContactOut{Contact: contactRow(x, v.actors, v.types), Verb: verbAlreadyThere, Where: r.Where()}
			return false, nil
		}
		var dup row.Row
		if in.Identity {
			vd, err := v.identityVerdict(aid, t.Name, t.Kind, norm)
			if err != nil {
				return false, err
			}
			switch vd.Outcome {
			case identmatch.AddTaken:
				return false, takenErr(v, t.Name, norm, []row.Row{v.contactByID(vd.Contact)})
			case identmatch.AddMergeable:
				d := v.actors[vd.Holder.String()]
				if err := mergeGate(ctx, env, in, a, d, norm); err != nil {
					return false, err
				}
				dup = d
			case identmatch.AddFree, identmatch.AddAlready:
				// AddAlready here is a contact held by a duplicate already
				// merged into this person: adding it to the person is fine.
			}
		}
		fields := map[string]value.Value{
			schema.FieldContactPrimary:  value.BoolOf(in.Primary || len(v.ofType(aid, t.Name, ids.AnyID{})) == 0),
			schema.FieldContactIdentity: value.BoolOf(in.Identity),
		}
		ref, err := actorRef(a.ID)
		if err != nil {
			return false, err
		}
		fields[schema.FieldContactActor] = ref
		for k, s := range map[string]string{schema.FieldContactType: t.Name, schema.FieldContactValue: strings.TrimSpace(in.Value),
			schema.FieldContactValueNorm: norm, schema.FieldContactLabel: strings.TrimSpace(in.Label), schema.FieldContactNote: strings.TrimSpace(in.Note)} {
			if s == "" {
				continue
			}
			tv, err := textValue(k, s)
			if err != nil {
				return false, err
			}
			fields[k] = tv
		}
		wrote := false
		if fields[schema.FieldContactPrimary].String() == "true" {
			if wrote, err = clearOtherPrimaries(ctx, r, v, aid, t.Name, ids.AnyID{}); err != nil {
				return wrote, err
			}
		}
		x, err := r.Create(ctx, schema.TableActorContacts, fields)
		if err != nil {
			return wrote, err
		}
		out = ContactOut{Contact: contactRow(x, v.actors, v.types), Verb: verbAdded, Where: r.Where()}
		if !dup.ID.IsZero() {
			// 6.4: the duplicate points at this person; no row is rewritten.
			// active false and status left keep it out of pickers, in step
			// with 2.7 (StatusOf reads status first).
			mv, err := textValue(schema.FieldActorMergedInto, aid)
			if err != nil {
				return true, err
			}
			if _, err := r.Update(ctx, schema.TableActors, dup.ID, map[string]value.Value{
				schema.FieldActorMergedInto: mv, schema.FieldActorActive: value.BoolOf(false), schema.FieldActorStatus: mustText(schema.ActorStatusLeft),
			}); err != nil {
				return true, errs.Prefix("contact added; merging "+dup.ID.String()+" failed, rerun with --merge --yes", err)
			}
			out.Merged = dup.ID.String()
		}
		return true, nil
	})
	return out, err
}

// mergeGate decides the 6.4 duplicate case: without --merge it is
// E_CONTACT_TAKEN naming --merge; with --merge and no --yes it is
// E_NEEDS_CONFIRM with the preview; confirmed, nil.
func mergeGate(ctx context.Context, env Env, in ContactAddIn, target, dup row.Row, norm string) error {
	if !in.Merge {
		return errs.New(errs.ContactTaken, "an actor row already exists for "+norm+": "+actorName(dup)+" "+dup.ID.String()).
			WithHint("lore actor show --id " + dup.ID.String() + "; if it is " + actorName(target) + ", rerun with --merge")
	}
	if op.Confirmed(ctx) {
		return nil
	}
	q := "merge " + actorName(dup) + " " + dup.ID.String() + " (" + humanKeyPrefix + norm + ") into " + actorName(target) + " " +
		target.ID.String() + "? its merged_into is set and it becomes inactive; no row is rewritten, reports follow the chain" +
		"; " + mergeRowsText(ctx, env, dup)
	return errs.New(errs.NeedsConfirm, "actor contact add --merge: "+q).WithQuestion(q).WithHint("ask the person, then rerun with --yes")
}

// mergeRowsText is the 6.4 preview's row counts of dup by table ("rows it
// wrote: comments 3, tasks 12"), from the host's ActorRowCounter upgrade.
// A host without it, or one whose stores cannot be read, says so instead:
// the counts inform the person, they never block the merge.
func mergeRowsText(ctx context.Context, env Env, dup row.Row) string {
	rc, ok := upgrade.As[profilestore.ActorRowCounter](env)
	if !ok {
		return "its row counts are not shown by this surface"
	}
	counts, err := rc.ActorRowCounts(ctx, dup.ID)
	if err != nil {
		return "its row counts could not be read here (" + err.Error() + ")"
	}
	if len(counts) == 0 {
		return "rows it wrote: none in the stores read here"
	}
	parts := make([]string, 0, len(counts))
	for _, c := range counts {
		parts = append(parts, c.Table+" "+strconv.Itoa(c.Rows))
	}
	return "rows it wrote: " + strings.Join(parts, ", ")
}

// notIdentityType is E_CONTACT_TYPE for --identity on t.
func notIdentityType(t EffectiveType) error {
	return errs.New(errs.ContactType, "contact type "+t.Name+" is not an identity type; --identity is for email, github, gitlab and types added with --identity").
		WithHint("lore contact-type list")
}

// mustText is a text value of a constant (always valid text).
func mustText(s string) value.Value {
	t, err := value.TextOf(s)
	if err != nil {
		return value.Null{}
	}
	return t
}

// ContactEditIn is the input of actor.contact.edit. An empty flag leaves
// its field as it is.
type ContactEditIn struct {
	ID         string `json:"id" arg:"0" desc:"the contact's acn_ id (lore actor contact list)"`
	Value      string `json:"value,omitempty" flag:"value" desc:"a new value (normalised again)"`
	Label      string `json:"label,omitempty" flag:"label" desc:"free text; suggested: personal, work, client-<name>"`
	Primary    bool   `json:"primary,omitempty" flag:"primary" desc:"make it the primary of its type (clears the other)"`
	NoPrimary  bool   `json:"no_primary,omitempty" flag:"no-primary" desc:"no longer the primary"`
	Identity   bool   `json:"identity,omitempty" flag:"identity" desc:"use for identity"`
	NoIdentity bool   `json:"no_identity,omitempty" flag:"no-identity" desc:"no longer used for identity"`
	Note       string `json:"note,omitempty" flag:"note" desc:"free text"`
}
