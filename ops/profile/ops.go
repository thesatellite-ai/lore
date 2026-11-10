// Package profile is the team profile noun of an actor (TEAM_PROFILES_DESIGN
// 3.1-3.3): actor.edit, actor.contact.add|edit|remove|list and
// contact_type.list|add|edit, with the effective contact-type list (the
// built-ins of logic/teamprofile overlaid by contact_types rows, TS-2).
//
// Every op reads and writes the org roster store through
// profilestore.Store (TS-1) and runs direct on the roster App
// (profilestore.IsDirect); write ops open it with write=true, which refuses
// an agent unless agents.may_manage_people (TS-4).
//
// Flow of a write op:
//
//	op ──▶ Store.Roster(ctx, true) ──▶ resolve <actor> / the contact row
//	   ──▶ validate (2.1 fields, 2.3 type, 6.1 Normalize, TP-3, TP-4)
//	   ──▶ Roster.Create / Update / Delete ──▶ Roster.Push once ──▶ done()
//
// Contacts are plain values everywhere (TP-6): nothing here masks them.
package profile

import (
	"context"
	"slices"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
)

// Env is what the profile ops need; *wire.App implements it.
type Env interface {
	profilestore.Store
}

// Output marks.
const (
	handleMark = "@"
	// noneShown is printed for an empty column.
	noneShown = "-"
)

// The error codes of the profile ops. Every write may also answer the
// Roster refusals (E_REFUSED with no org, TS-1; E_LEAK_BLOCKED in a
// client store; E_POLICY_DENIED for an agent, TS-4).
// An op that resolves an actor reference may also answer actorRefErrors:
// E_IN_TRASH when the reference is in contact form (github:…, email:…)
// and its only holder is a removed identity contact (DELETION 3.2).
var (
	rosterErrors      = []errs.Code{errs.Refused, errs.LeakBlocked, errs.PolicyDenied}
	actorRefErrors    = []errs.Code{errs.InTrash}
	readErrors        = slices.Concat(work.ReadErrors, []errs.Code{errs.LeakBlocked})
	contactListErrors = slices.Concat(readErrors, actorRefErrors)
	actorEditErrors   = slices.Concat(work.WriteErrors, rosterErrors, actorRefErrors, []errs.Code{errs.SetupNotFound})
	contactAddErrors  = slices.Concat(work.WriteErrors, rosterErrors, []errs.Code{errs.ContactType, errs.ContactTaken, errs.URLUserinfo}, actorRefErrors)
	contactEditErrors = contactAddErrors // E_IN_TRASH: a removed contact id (liveContact)
	contactRmErrors   = slices.Concat(work.WriteErrors, rosterErrors, []errs.Code{errs.InTrash})
	typeWriteErrors   = slices.Concat(work.WriteErrors, rosterErrors, []errs.Code{errs.NameTaken, errs.ContactType})
)

// Ops returns the profile operations bound to env (TEAM_PROFILES 3.1-3.3).
func Ops(env Env) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{Name: profilestore.OpActorEdit, Summary: "Edit a person's profile: title, timezone, location, photo, bio, birthday (team profiles)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: actorEditErrors,
			Examples: []op.Example{
				{Title: "fill the profile", CLI: `lore actor edit @mira --title "Backend engineer" --timezone Europe/Istanbul --location "Ankara, TR"`, Want: "edited"},
				{Title: "clear fields", CLI: "lore actor edit @mira --clear photo_url,bio", Want: "edited"},
				{Title: "rename at the source", CLI: `lore actor edit @mira --name "Mira Patel"`, Want: "edited"},
			}},
			func(ctx context.Context, in ActorEditIn) (ActorEditOut, error) { return actorEdit(ctx, env, in) }),
		op.Define(op.Spec{Name: profilestore.OpContactAdd, Summary: "Add a contact (email, GitHub login, phone, …) to a person",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: contactAddErrors,
			// --merge asks first (6.4): E_NEEDS_CONFIRM without --yes.
			Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{
				{Title: "an identity GitHub login", CLI: "lore actor contact add @mira github mirap --label personal --identity", Want: "added"},
				{Title: "a work phone", CLI: `lore actor contact add @mira phone "+90 532 000 0000" --label work --primary`, Want: "added"},
			}},
			func(ctx context.Context, in ContactAddIn) (ContactOut, error) { return contactAdd(ctx, env, in) }),
		op.Define(op.Spec{Name: profilestore.OpContactEdit, Summary: "Edit a contact: value, label, primary, identity, note",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: contactEditErrors,
			Examples: []op.Example{{Title: "make it the primary", CLI: "lore actor contact edit acn_… --primary", Want: "edited"}}},
			func(ctx context.Context, in ContactEditIn) (ContactOut, error) { return contactEdit(ctx, env, in) }),
		op.Define(op.Spec{Name: profilestore.OpContactRemove, Summary: "Remove a contact (to the trash, restorable)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: contactRmErrors,
			Examples: []op.Example{{Title: "an old phone", CLI: `lore actor contact remove acn_… --reason "number changed"`, Want: "removed"}}},
			func(ctx context.Context, in ContactRemoveIn) (ContactRemoveOut, error) {
				return contactRemove(ctx, env, in)
			}),
		op.Define(op.Spec{Name: profilestore.OpContactList, Summary: "List contacts: one person's, or everyone's (the org's phone list)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: contactListErrors,
			Examples: []op.Example{{Title: "every phone in the org", CLI: "lore actor contact list --type phone", Want: ""}}},
			func(ctx context.Context, in ContactListIn) (ContactListOut, error) { return contactList(ctx, env, in) }),
		op.Define(op.Spec{Name: profilestore.OpContactTypeList, Summary: "List the contact types: built-ins and the org's own",
			Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors,
			Examples: []op.Example{{Title: "the types", CLI: "lore contact-type list", Want: "github"}}},
			func(ctx context.Context, in ContactTypeListIn) (ContactTypeListOut, error) {
				return contactTypeList(ctx, env, in)
			}),
		op.Define(op.Spec{Name: profilestore.OpContactTypeAdd, Summary: "Add a contact type to the org's list",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: typeWriteErrors,
			Examples: []op.Example{{Title: "Bitbucket logins", CLI: `lore contact-type add bitbucket --kind handle --identity --url-template "https://bitbucket.org/{value}"`, Want: "added"}}},
			func(ctx context.Context, in ContactTypeAddIn) (ContactTypeOut, error) {
				return contactTypeAdd(ctx, env, in)
			}),
		op.Define(op.Spec{Name: profilestore.OpContactTypeEdit, Summary: "Change a contact type's label or link, or retire it",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: typeWriteErrors,
			Examples: []op.Example{{Title: "retire a type", CLI: "lore contact-type edit discord --retire", Want: "edited"}}},
			func(ctx context.Context, in ContactTypeEditIn) (ContactTypeOut, error) {
				return contactTypeEdit(ctx, env, in)
			}),
	}
}

// openWrite opens the roster for a write and runs fn; Push runs once
// after fn succeeds with wrote true. done always runs.
func openWrite(ctx context.Context, env Env, warns *[]string, fn func(r profilestore.Roster) (wrote bool, err error)) error {
	r, done, err := env.Roster(ctx, true)
	defer done()
	if err != nil {
		return err
	}
	wrote, err := fn(r)
	// The engine's warnings of fn's writes reach the output (set after fn,
	// which builds the output).
	*warns = append(*warns, r.Warnings()...)
	if wrote {
		// Push also after a partial failure: what was written is committed
		// locally and should reach the org (rosterPush is best effort).
		r.Push(ctx)
	}
	return err
}

// text is a field's text, "" when absent or null.
func text(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// flag is a bool field; absent, null or anything but true is false.
func flag(r row.Row, name string) bool {
	if b, ok := r.Fields[name].(value.Bool); ok {
		return b.Bool()
	}
	return text(r, name) == "true"
}

// textValue is value.TextOf as an E_INVALID_INPUT naming the field.
func textValue(field, s string) (value.Value, error) {
	t, err := value.TextOf(s)
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, field+": not valid text", err)
	}
	return t, nil
}

// actorRef is the ref value of an actor id.
func actorRef(id ids.AnyID) (value.Value, error) {
	ref, err := value.RefOf(id)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "actor ref "+id.String(), err)
	}
	return ref, nil
}

// actorName is "Display Name (@handle)", the way the roster verbs name
// a person.
func actorName(a row.Row) string {
	n := text(a, schema.FieldActorDisplayName)
	if h := text(a, schema.FieldActorHandle); h != "" {
		n += " (" + handleMark + h + ")"
	}
	return n
}
