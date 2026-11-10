package profile

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/identmatch"
	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
)

// actor.contact.edit, .remove and .list (TEAM_PROFILES 3.2).

// contactID parses an acn_ id; anything else is E_INVALID_ID.
func contactID(s string) (ids.AnyID, error) {
	id, err := ids.ParseAny(strings.TrimSpace(s))
	if err != nil || id.Prefix() != ids.PrefixActorContact {
		return ids.AnyID{}, errs.New(errs.InvalidID, s+" is not a contact id (acn_…)").WithHint("lore actor contact list")
	}
	return id, nil
}

// liveContact is the live contact id among v.contacts. A miss is
// E_IN_TRASH with the restore hint when r has the contact in its trash
// (DELETION 3.2, the same answer as any trashed row), else E_NOT_FOUND
// (never written, or purged).
func (v rosterView) liveContact(ctx context.Context, r profilestore.Roster, id ids.AnyID) (row.Row, error) {
	i := slices.IndexFunc(v.contacts, func(c row.Row) bool { return c.ID == id })
	if i < 0 {
		if err := r.InTrash(ctx, schema.TableActorContacts, id); err != nil {
			return row.Row{}, err
		}
		return row.Row{}, errs.New(errs.NotFound, "no contact "+id.String()).WithHint("lore actor contact list")
	}
	return v.contacts[i], nil
}

func contactEdit(ctx context.Context, env Env, in ContactEditIn) (ContactOut, error) {
	switch {
	case in.Primary && in.NoPrimary:
		return ContactOut{}, errs.New(errs.InvalidInput, "actor contact edit: --primary and --no-primary together; give one")
	case in.Identity && in.NoIdentity:
		return ContactOut{}, errs.New(errs.InvalidInput, "actor contact edit: --identity and --no-identity together; give one")
	case in.Value == "" && in.Label == "" && in.Note == "" && !in.Primary && !in.NoPrimary && !in.Identity && !in.NoIdentity:
		return ContactOut{}, errs.New(errs.InvalidInput, "actor contact edit: nothing to change").
			WithHint("give --value, --label, --note, --primary, --no-primary, --identity or --no-identity")
	}
	id, err := contactID(in.ID)
	if err != nil {
		return ContactOut{}, err
	}
	var out ContactOut
	err = openWrite(ctx, env, &out.Warnings, func(r profilestore.Roster) (bool, error) {
		v, err := loadView(ctx, r)
		if err != nil {
			return false, err
		}
		cur, err := v.liveContact(ctx, r, id)
		if err != nil {
			return false, err
		}
		aid, typ := text(cur, schema.FieldContactActor), text(cur, schema.FieldContactType)
		// A retired type still edits (its contacts keep it, 2.3); an unknown
		// one (its row removed) only blocks what needs the type's rules.
		t, known := findType(v.types, typ)
		patch := map[string]value.Value{}
		norm := text(cur, schema.FieldContactValueNorm)
		if strings.TrimSpace(in.Value) != "" {
			if !known {
				return false, errs.New(errs.ContactType, "contact type "+typ+" is not in the list; its value cannot be checked").WithHint("lore contact-type list")
			}
			if norm, err = teamprofile.Normalize(t.Kind, in.Value); err != nil {
				return false, err
			}
			if x, dup := v.sameContact(aid, typ, norm, id); dup {
				return false, errs.New(errs.InvalidInput, "actor contact edit: "+typ+" "+norm+" is already a contact of this person ("+x.ID.String()+")").
					WithHint("lore actor contact remove " + id.String())
			}
			for k, s := range map[string]string{schema.FieldContactValue: strings.TrimSpace(in.Value), schema.FieldContactValueNorm: norm} {
				tv, err := textValue(k, s)
				if err != nil {
					return false, err
				}
				patch[k] = tv
			}
		}
		ident := (flag(cur, schema.FieldContactIdentity) || in.Identity) && !in.NoIdentity
		if ident && (in.Identity || patch[schema.FieldContactValueNorm] != nil) {
			if !known || !t.Identity {
				return false, notIdentityType(EffectiveType{ContactType: teamprofile.ContactType{Name: typ}})
			}
			vd, err := v.identityVerdict(aid, typ, t.Kind, norm)
			if err != nil {
				return false, err
			}
			switch vd.Outcome {
			case identmatch.AddTaken:
				return false, takenErr(v, typ, norm, []row.Row{v.contactByID(vd.Contact)})
			case identmatch.AddMergeable:
				d := v.actors[vd.Holder.String()]
				return false, errs.New(errs.ContactTaken, "an actor row already exists for "+norm+": "+actorName(d)+" "+d.ID.String()).
					WithHint("if it is the same person: lore actor contact add " + aid + " " + typ + " " + norm + " --identity --merge")
			case identmatch.AddFree, identmatch.AddAlready:
			}
		}
		if in.Identity || in.NoIdentity {
			patch[schema.FieldContactIdentity] = value.BoolOf(in.Identity)
		}
		for k, s := range map[string]string{schema.FieldContactLabel: in.Label, schema.FieldContactNote: in.Note} {
			if s = strings.TrimSpace(s); s == "" {
				continue
			}
			tv, err := textValue(k, s)
			if err != nil {
				return false, err
			}
			patch[k] = tv
		}
		wrote := false
		if in.Primary {
			if wrote, err = clearOtherPrimaries(ctx, r, v, aid, typ, id); err != nil {
				return wrote, err
			}
			patch[schema.FieldContactPrimary] = value.BoolOf(true)
		}
		if in.NoPrimary {
			patch[schema.FieldContactPrimary] = value.BoolOf(false)
		}
		x, err := r.Update(ctx, schema.TableActorContacts, id, patch)
		if err != nil {
			return wrote, err
		}
		out = ContactOut{Contact: contactRow(x, v.actors, v.types), Verb: verbEdited, Where: r.Where()}
		return true, nil
	})
	return out, err
}

// ContactRemoveIn is the input of actor.contact.remove.
type ContactRemoveIn struct {
	ID     string `json:"id" arg:"0" desc:"the contact's acn_ id"`
	Reason string `json:"reason,omitempty" flag:"reason" desc:"why (kept with the row in the trash)"`
}

// ContactRemoveOut is the removed contact.
type ContactRemoveOut struct {
	Contact ContactRow `json:"contact"`
	Where   string     `json:"where"`
	// Warnings are the engine's warnings of the write
	// (profilestore.WarningLine), printed after the success line.
	Warnings []string `json:"warnings,omitempty"`
}

// Text is "✓ acn_… github mirap removed from Mira Patel (@mira) (restorable: lore trash)".
func (o ContactRemoveOut) Text() string { return o.text() + profilestore.WarningText(o.Warnings) }

// text is Text without the warnings.
func (o ContactRemoveOut) text() string {
	c := o.Contact
	return work.Success + " " + c.ID + " " + c.Type + " " + c.Value + " removed from " + c.ActorName + " in " + o.Where + " (restorable from the trash)\n"
}

// contactRemove moves the contact to the trash (DELETION_DESIGN); the
// rows it attributed keep their actor id (2.2). A removed primary leaves
// its type without one: lore never picks the next primary for a person.
func contactRemove(ctx context.Context, env Env, in ContactRemoveIn) (ContactRemoveOut, error) {
	id, err := contactID(in.ID)
	if err != nil {
		return ContactRemoveOut{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = defaultRemoveReason
	}
	var out ContactRemoveOut
	err = openWrite(ctx, env, &out.Warnings, func(r profilestore.Roster) (bool, error) {
		v, err := loadView(ctx, r)
		if err != nil {
			return false, err
		}
		cur, err := v.liveContact(ctx, r, id)
		if err != nil {
			return false, err
		}
		if err := r.Delete(ctx, schema.TableActorContacts, id, reason); err != nil {
			return false, err
		}
		out = ContactRemoveOut{Contact: contactRow(cur, v.actors, v.types), Where: r.Where()}
		return true, nil
	})
	return out, err
}

// ContactListIn is the input of actor.contact.list.
type ContactListIn struct {
	Actor    string `json:"actor,omitempty" arg:"0" desc:"one person (@handle, act_ id, stable key); none = everyone"`
	Type     string `json:"type,omitempty" flag:"type" desc:"only this contact type"`
	Identity bool   `json:"identity,omitempty" flag:"identity" desc:"only identity contacts"`
	// --created-by / --updated-by: the shared writer filters of every
	// list (INTEG-AUDIT-1; engine.List applies them to the table that
	// declares this list's noun, schema.Noun).
	metalist.Audit
}

// ContactListOut is the contacts, by person, type, primary first, value.
type ContactListOut struct {
	Contacts []ContactRow `json:"contacts"`
}

// Text is one line per contact.
func (o ContactListOut) Text() string {
	var b strings.Builder
	for _, c := range o.Contacts {
		cols := []string{c.ID, pad(c.ActorName, 24), pad(c.Type, 9), pad(c.Value, 28), pad(c.Label, 10), pad(c.flags(), 17)}
		if c.URL != "" {
			cols = append(cols, c.URL)
		}
		b.WriteString(strings.TrimRight(strings.Join(cols, " "), " ") + "\n")
	}
	return b.String()
}

// contactList reads the roster without the write check: open to agents
// (3: every read op is).
func contactList(ctx context.Context, env Env, in ContactListIn) (ContactListOut, error) {
	r, done, err := env.Roster(ctx, false)
	defer done()
	if err != nil {
		return ContactListOut{}, err
	}
	only := ""
	if ref := strings.TrimSpace(in.Actor); ref != "" {
		a, err := r.ResolveActor(ctx, ref)
		if err != nil {
			return ContactListOut{}, err
		}
		only = a.ID.String()
	}
	v, err := loadView(ctx, r)
	if err != nil {
		return ContactListOut{}, err
	}
	typ := strings.TrimSpace(in.Type)
	out := ContactListOut{Contacts: []ContactRow{}}
	for _, x := range v.contacts {
		c := contactRow(x, v.actors, v.types)
		if (only != "" && c.ActorID != only) || (typ != "" && c.Type != typ) || (in.Identity && !c.Identity) {
			continue
		}
		out.Contacts = append(out.Contacts, c)
	}
	slices.SortStableFunc(out.Contacts, func(a, b ContactRow) int {
		if n := strings.Compare(a.ActorName, b.ActorName); n != 0 {
			return n
		}
		if n := strings.Compare(a.ActorID, b.ActorID); n != 0 {
			return n
		}
		if n := strings.Compare(a.Type, b.Type); n != 0 {
			return n
		}
		if a.Primary != b.Primary {
			if a.Primary {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ValueNorm, b.ValueNorm)
	})
	return out, nil
}
