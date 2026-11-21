package team

// teamcontact.go — a team's contacts (owner org-setup 2026-11-21, item 4:
// people had contacts, a team had nowhere to keep its chat channel). A
// team contact is a team_contacts row beside the team's row on the roster,
// checked by the same contact types as a person's (lore contact-type list:
// slack, discord, email, website, …). It is never an identity: no git email
// or login resolves to a team.
//
//	team.contact.add <@h> --type <t> --value <v> [--label] [--primary] [--note]
//	    ──▶ the team on the roster (a removed one is refused: team add first)
//	    ──▶ admin? (a team change, like its members)
//	    ──▶ type live in the effective list (E_CONTACT_TYPE)
//	    ──▶ Normalize(kind, value) ──▶ same (team, type, value_norm)? "already there"
//	    ──▶ primary: --primary, or the first of its type; the others cleared
//	    ──▶ Create ──▶ Push
//	team.contact.edit <tcn_id> [--value] [--label] [--note] [--primary|--no-primary]
//	team.contact.remove <tcn_id> [--reason]   ──▶ the row to the trash
//	team.contact.list [<@h>] [--type]         ──▶ every team's contacts, or one team's
//
// team show prints a team's contacts and team list its primary ones; both
// carry them in --json as "contacts".

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/ops/profile"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
)

// Team contact operation names (CLI `lore team contact add|edit|remove|list`).
const (
	OpTeamContactAdd    = profilestore.OpTeamContactAdd
	OpTeamContactEdit   = profilestore.OpTeamContactEdit
	OpTeamContactRemove = profilestore.OpTeamContactRemove
	OpTeamContactList   = profilestore.OpTeamContactList
)

// Team contact write verbs (TeamContactOut.Verb).
const (
	verbTeamContactAdded   = "added"
	verbTeamContactEdited  = "edited"
	verbTeamContactRemoved = "removed"
	verbTeamContactThere   = "already there"
)

// defaultTeamContactReason is the trash reason of team contact remove
// without --reason.
const defaultTeamContactReason = "removed with lore team contact remove"

// TeamContactOps returns the team contact operations bound to env.
func TeamContactOps(env profilestore.Store) []op.Entry {
	readErrs := append([]errs.Code{errs.Refused, errs.LeakBlocked}, work.ReadErrors...)
	writeErrs := append([]errs.Code{errs.Refused, errs.LeakBlocked, errs.ContactType, errs.StatusChange, errs.Unsupported}, work.WriteErrors...)
	return []op.Entry{
		op.Define(op.Spec{Name: OpTeamContactAdd, Summary: "Add a contact to a team: its chat channel, mailing list, … (types from lore contact-type list)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrs,
			Examples: []op.Example{{Title: "the team's Slack channel", CLI: `lore team contact add --handle @web --type slack --value "#web-team"`, Want: "#web-team"}}},
			func(ctx context.Context, in TeamContactAddIn) (TeamContactOut, error) {
				return teamContactAdd(ctx, env, in)
			}),
		op.Define(op.Spec{Name: OpTeamContactEdit, Summary: "Change a team contact's value, label, note or primary flag",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrs,
			Examples: []op.Example{{Title: "the channel was renamed", CLI: `lore team contact edit --id tcn_… --value "#web"`, Want: "edited"}}},
			func(ctx context.Context, in TeamContactEditIn) (TeamContactOut, error) {
				return teamContactEdit(ctx, env, in)
			}),
		op.Define(op.Spec{Name: OpTeamContactRemove, Summary: "Remove a team contact (to the trash, restorable)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrs,
			Examples: []op.Example{{Title: "the channel was archived", CLI: "lore team contact remove --id tcn_…", Want: "removed"}}},
			func(ctx context.Context, in TeamContactRemoveIn) (TeamContactOut, error) {
				return teamContactRemove(ctx, env, in)
			}),
		op.Define(op.Spec{Name: OpTeamContactList, Summary: "List the teams' contacts (--handle for one team)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: readErrs,
			Examples: []op.Example{{Title: "every team's channels as JSON", CLI: "lore team contact list --json", Want: `"contacts"`}}},
			func(ctx context.Context, in TeamContactListIn) (TeamContactListOut, error) {
				return teamContactList(ctx, env, in)
			}),
	}
}

// TeamContactAddIn is the input of team.contact.add.
type TeamContactAddIn struct {
	Handle  string `json:"handle" arg:"0" flag:"handle" desc:"the team's @handle (or its tem_ id)"`
	Type    string `json:"type" flag:"type" desc:"a contact type (lore contact-type list): slack, discord, email, website, …"`
	Value   string `json:"value" flag:"value" desc:"as typed: #web-team, web@acme.example, https://chat.acme.example/web"`
	Label   string `json:"label,omitempty" flag:"label" desc:"free text: announcements, on-call, …"`
	Primary bool   `json:"primary,omitempty" flag:"primary" desc:"the primary contact of its type (one per type; the first one is primary by default)"`
	Note    string `json:"note,omitempty" flag:"note" desc:"free text"`
}

// TeamContactEditIn is the input of team.contact.edit; an empty flag
// leaves its field as it is.
type TeamContactEditIn struct {
	ID        string `json:"id" arg:"0" flag:"id" desc:"the contact's tcn_ id (lore team contact list)"`
	Value     string `json:"value,omitempty" flag:"value" desc:"a new value (checked by its type again)"`
	Label     string `json:"label,omitempty" flag:"label" desc:"free text"`
	Note      string `json:"note,omitempty" flag:"note" desc:"free text"`
	Primary   bool   `json:"primary,omitempty" flag:"primary" desc:"make it the primary of its type (clears the other)"`
	NoPrimary bool   `json:"no_primary,omitempty" flag:"no-primary" desc:"no longer the primary"`
}

// TeamContactRemoveIn is the input of team.contact.remove.
type TeamContactRemoveIn struct {
	ID     string `json:"id" arg:"0" flag:"id" desc:"the contact's tcn_ id"`
	Reason string `json:"reason,omitempty" flag:"reason" desc:"why (kept with the row in the trash)"`
}

// TeamContactListIn is the input of team.contact.list.
type TeamContactListIn struct {
	Handle string `json:"handle,omitempty" arg:"0" flag:"handle" desc:"one team (@handle or tem_ id); none = every team"`
	Type   string `json:"type,omitempty" flag:"type" desc:"only this contact type"`
	metalist.RosterStore
	metalist.Audit
	op.PageFlags
}

// TeamContact is one team contact as shown (team show, team list,
// team contact list and every team contact write).
type TeamContact struct {
	ID         string `json:"id"`
	TeamID     string `json:"team_id"`
	TeamHandle string `json:"team_handle,omitempty"`
	Type       string `json:"type"`
	Value      string `json:"value"`
	ValueNorm  string `json:"value_norm"`
	Label      string `json:"label,omitempty"`
	Primary    bool   `json:"primary"`
	Note       string `json:"note,omitempty"`
	// URL is the type's url_template with value_norm, when it has one.
	URL       string `json:"url,omitempty"`
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// line is "slack #web-team  announcements  [primary]  <url>" (no id).
func (c TeamContact) line() string {
	parts := []string{c.Type, c.Value}
	if c.Label != "" {
		parts = append(parts, c.Label)
	}
	if c.Primary {
		parts = append(parts, "["+schema.FieldContactPrimary+"]")
	}
	if c.URL != "" {
		parts = append(parts, c.URL)
	}
	return strings.Join(parts, "  ")
}

// TeamContactOut is a team contact write's answer.
type TeamContactOut struct {
	Contact TeamContact `json:"contact"`
	// Verb is added, edited, removed or "already there".
	Verb     string   `json:"verb"`
	Where    string   `json:"where"`
	Warnings []string `json:"warnings,omitempty"`
	label    string
}

// Text is "✓ tcn_… slack #web-team added to @web [primary] on the roster
// of acme (acme/org)", then the warnings.
func (o TeamContactOut) Text() string {
	c := o.Contact
	mark := work.Success
	if o.Verb == verbTeamContactThere {
		mark = "·"
	}
	to := " to "
	switch o.Verb {
	case verbTeamContactRemoved:
		to = " from "
	case verbTeamContactEdited, verbTeamContactThere:
		to = " for "
	}
	line := mark + " " + c.ID + " " + c.Type + " " + c.Value + " " + o.Verb + to + "@" + c.TeamHandle
	if c.Primary && o.Verb != verbTeamContactRemoved {
		line += " [" + schema.FieldContactPrimary + "]"
	}
	line += " on the roster of " + o.label
	if o.Verb == verbTeamContactRemoved {
		line += " (restorable from the trash)"
	}
	return line + "\n" + profilestore.WarningText(o.Warnings)
}

// TeamContactListOut is team.contact.list's answer.
type TeamContactListOut struct {
	Contacts []TeamContact `json:"contacts"`
	Where    string        `json:"where"`
	label    string
}

// Text is one line per contact in columns, or "no team contacts on the
// roster of …".
func (o TeamContactListOut) Text() string {
	if len(o.Contacts) == 0 {
		return "no team contacts on the roster of " + o.label + "\n"
	}
	rows := make([][]string, len(o.Contacts))
	for i, c := range o.Contacts {
		fl := ""
		if c.Primary {
			fl = schema.FieldContactPrimary
		}
		rows[i] = []string{c.ID, "@" + c.TeamHandle, c.Type, c.Value, c.Label, fl, c.URL}
	}
	var b strings.Builder
	writeColumns(&b, rows)
	return b.String()
}

// teamContactView is what a team contact op reads once: the teams by id,
// every live team contact and the effective contact types.
type teamContactView struct {
	teams    map[string]row.Row
	contacts []row.Row
	types    []profile.EffectiveType
}

func loadTeamContactView(ctx context.Context, r profilestore.Roster) (teamContactView, error) {
	ts, err := r.List(ctx, schema.TableTeams)
	if err != nil {
		return teamContactView{}, err
	}
	cs, err := r.List(ctx, schema.TableTeamContacts)
	if err != nil {
		return teamContactView{}, err
	}
	types, err := profile.ContactTypes(ctx, r)
	if err != nil {
		return teamContactView{}, err
	}
	v := teamContactView{teams: make(map[string]row.Row, len(ts)), contacts: cs, types: types}
	for _, t := range ts {
		v.teams[t.ID.String()] = t
	}
	return v, nil
}

// contactOf renders x.
func (v teamContactView) contactOf(x row.Row) TeamContact {
	w := auditrec.Of(x)
	c := TeamContact{ID: x.ID.String(), TeamID: fieldText(x, schema.FieldTeamContactTeam),
		Type: fieldText(x, schema.FieldContactType), Value: fieldText(x, schema.FieldContactValue),
		ValueNorm: fieldText(x, schema.FieldContactValueNorm), Label: fieldText(x, schema.FieldContactLabel),
		Primary: fieldText(x, schema.FieldContactPrimary) == "true", Note: fieldText(x, schema.FieldContactNote),
		CreatedBy: w.CreatedBy, UpdatedBy: w.UpdatedBy}
	if t, ok := v.teams[c.TeamID]; ok {
		c.TeamHandle = fieldText(t, teamFieldHandle)
	}
	if t, ok := profile.FindType(v.types, c.Type); ok && t.URLTemplate != "" {
		c.URL = strings.ReplaceAll(t.URLTemplate, teamprofile.URLValue, c.ValueNorm)
	}
	return c
}

// of is team id's contacts, sorted by type then value (the primary of a
// type first); only typ when it is not "".
func (v teamContactView) of(teamID, typ string) []TeamContact {
	out := []TeamContact{}
	for _, x := range v.contacts {
		if fieldText(x, schema.FieldTeamContactTeam) != teamID || typ != "" && fieldText(x, schema.FieldContactType) != typ {
			continue
		}
		out = append(out, v.contactOf(x))
	}
	sortTeamContacts(out)
	return out
}

// sortTeamContacts orders by team handle, type, primary first, value.
func sortTeamContacts(cs []TeamContact) {
	slices.SortFunc(cs, func(a, b TeamContact) int {
		if c := strings.Compare(a.TeamHandle, b.TeamHandle); c != 0 {
			return c
		}
		if c := strings.Compare(a.Type, b.Type); c != 0 {
			return c
		}
		if a.Primary != b.Primary {
			if a.Primary {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ValueNorm, b.ValueNorm)
	})
}

// find is the live contact (teamID, typ, norm) other than skip.
func (v teamContactView) find(teamID, typ, norm string, skip ids.AnyID) (row.Row, bool) {
	for _, x := range v.contacts {
		if x.ID != skip && fieldText(x, schema.FieldTeamContactTeam) == teamID && fieldText(x, schema.FieldContactType) == typ &&
			fieldText(x, schema.FieldContactValueNorm) == norm {
			return x, true
		}
	}
	return row.Row{}, false
}

// clearPrimaries writes primary false on every other primary of (teamID,
// typ) before the new one is written (at most one per type).
func (v teamContactView) clearPrimaries(ctx context.Context, r profilestore.Roster, teamID, typ string, keep ids.AnyID) error {
	for _, x := range v.contacts {
		if x.ID == keep || fieldText(x, schema.FieldTeamContactTeam) != teamID || fieldText(x, schema.FieldContactType) != typ ||
			fieldText(x, schema.FieldContactPrimary) != "true" {
			continue
		}
		if _, err := r.Update(ctx, schema.TableTeamContacts, x.ID, map[string]value.Value{schema.FieldContactPrimary: value.BoolOf(false)}); err != nil {
			return err
		}
	}
	return nil
}

// hasType reports whether team teamID holds a live contact of typ.
func (v teamContactView) hasType(teamID, typ string) bool {
	return slices.ContainsFunc(v.contacts, func(x row.Row) bool {
		return fieldText(x, schema.FieldTeamContactTeam) == teamID && fieldText(x, schema.FieldContactType) == typ
	})
}

// teamContactID parses a tcn_ id.
func teamContactID(s string) (ids.AnyID, error) {
	id, err := ids.ParseAny(strings.TrimSpace(s))
	if err != nil || id.Prefix() != ids.PrefixTeamContact {
		return ids.AnyID{}, errs.New(errs.InvalidID, work.Quote(s)+" is not a team contact id (tcn_…)").WithHint("lore team contact list")
	}
	return id, nil
}

// live is the live contact id, E_IN_TRASH or E_NOT_FOUND otherwise.
func (v teamContactView) live(ctx context.Context, r profilestore.Roster, id ids.AnyID) (row.Row, error) {
	i := slices.IndexFunc(v.contacts, func(x row.Row) bool { return x.ID == id })
	if i < 0 {
		if err := r.InTrash(ctx, schema.TableTeamContacts, id); err != nil {
			return row.Row{}, err
		}
		return row.Row{}, errs.New(errs.NotFound, "no team contact "+id.String()).WithHint("lore team contact list")
	}
	return v.contacts[i], nil
}

// putText sets each non-empty field of texts in fields.
func putText(fields map[string]value.Value, texts map[string]string) error {
	for k, s := range texts {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		tv, err := textValue(k, s)
		if err != nil {
			return err
		}
		fields[k] = tv
	}
	return nil
}

func teamContactAdd(ctx context.Context, env profilestore.Store, in TeamContactAddIn) (TeamContactOut, error) {
	h, err := teamHandle(in.Handle)
	if err != nil {
		return TeamContactOut{}, err
	}
	typ := strings.TrimSpace(in.Type)
	if typ == "" || strings.TrimSpace(in.Value) == "" {
		return TeamContactOut{}, errs.New(errs.InvalidInput, "team contact add: give --type and --value").
			WithHint(`lore team contact add --handle ` + teamShown(h) + ` --type slack --value "#` + h + `"`)
	}
	r, done, err := env.Roster(ctx, true)
	defer done()
	if err != nil {
		return TeamContactOut{}, err
	}
	line := "lore team contact add --handle " + teamShown(h) + " --type " + shellWord(typ) + " --value " + shellWord(strings.TrimSpace(in.Value))
	if err = requireTeamAdmin(ctx, r, h, line); err != nil {
		return TeamContactOut{}, err
	}
	team, ok, err := findTeam(ctx, r, h)
	if err != nil {
		return TeamContactOut{}, err
	}
	if !ok {
		return TeamContactOut{}, teamMissErr(h, r.Label())
	}
	shown := fieldText(team, teamFieldHandle)
	if fieldText(team, teamFieldActive) == boolFalse {
		return TeamContactOut{}, errs.New(errs.StatusChange, "team @"+shown+" is removed; bring it back before adding a contact").
			WithHint("lore team add --handle @" + shown)
	}
	v, err := loadTeamContactView(ctx, r)
	if err != nil {
		return TeamContactOut{}, err
	}
	t, err := profile.TypeForAdd(v.types, typ)
	if err != nil {
		return TeamContactOut{}, err
	}
	norm, err := teamprofile.Normalize(t.Kind, in.Value)
	if err != nil {
		return TeamContactOut{}, err
	}
	tid := team.ID.String()
	if x, there := v.find(tid, t.Name, norm, ids.AnyID{}); there {
		return TeamContactOut{Contact: v.contactOf(x), Verb: verbTeamContactThere, Where: r.Where(), label: r.Label()}, nil
	}
	ref, err := refValue(team.ID)
	if err != nil {
		return TeamContactOut{}, err
	}
	primary := in.Primary || !v.hasType(tid, t.Name)
	fields := map[string]value.Value{schema.FieldTeamContactTeam: ref, schema.FieldContactPrimary: value.BoolOf(primary)}
	if err = putText(fields, map[string]string{schema.FieldContactType: t.Name, schema.FieldContactValue: in.Value,
		schema.FieldContactValueNorm: norm, schema.FieldContactLabel: in.Label, schema.FieldContactNote: in.Note}); err != nil {
		return TeamContactOut{}, err
	}
	if primary {
		if err = v.clearPrimaries(ctx, r, tid, t.Name, ids.AnyID{}); err != nil {
			return TeamContactOut{}, err
		}
	}
	x, err := r.Create(ctx, schema.TableTeamContacts, fields)
	if err != nil {
		return TeamContactOut{}, err
	}
	r.Push(ctx)
	return TeamContactOut{Contact: v.contactOf(x), Verb: verbTeamContactAdded, Warnings: r.Warnings(), Where: r.Where(), label: r.Label()}, nil
}

// openTeamContact opens the roster for a write on contact id: the view,
// the live row, and the admin check naming line.
func openTeamContact(ctx context.Context, r profilestore.Roster, id ids.AnyID, line string) (teamContactView, row.Row, error) {
	v, err := loadTeamContactView(ctx, r)
	if err != nil {
		return v, row.Row{}, err
	}
	cur, err := v.live(ctx, r, id)
	if err != nil {
		return v, row.Row{}, err
	}
	h := v.contactOf(cur).TeamHandle
	if h == "" {
		h = fieldText(cur, schema.FieldTeamContactTeam)
	}
	return v, cur, requireTeamAdmin(ctx, r, h, line)
}

func teamContactEdit(ctx context.Context, env profilestore.Store, in TeamContactEditIn) (TeamContactOut, error) {
	switch {
	case in.Primary && in.NoPrimary:
		return TeamContactOut{}, errs.New(errs.InvalidInput, "team contact edit: --primary and --no-primary together; give one")
	case strings.TrimSpace(in.Value+in.Label+in.Note) == "" && !in.Primary && !in.NoPrimary:
		return TeamContactOut{}, errs.New(errs.InvalidInput, "team contact edit: nothing to change").
			WithHint("give --value, --label, --note, --primary or --no-primary")
	}
	id, err := teamContactID(in.ID)
	if err != nil {
		return TeamContactOut{}, err
	}
	r, done, err := env.Roster(ctx, true)
	defer done()
	if err != nil {
		return TeamContactOut{}, err
	}
	v, cur, err := openTeamContact(ctx, r, id, "lore team contact edit --id "+id.String())
	if err != nil {
		return TeamContactOut{}, err
	}
	tid, typ := fieldText(cur, schema.FieldTeamContactTeam), fieldText(cur, schema.FieldContactType)
	patch := map[string]value.Value{}
	texts := map[string]string{schema.FieldContactLabel: in.Label, schema.FieldContactNote: in.Note}
	if strings.TrimSpace(in.Value) != "" {
		t, known := profile.FindType(v.types, typ)
		if !known {
			return TeamContactOut{}, errs.New(errs.ContactType, "contact type "+typ+" is not in the list; its value cannot be checked").WithHint("lore contact-type list")
		}
		norm, nerr := teamprofile.Normalize(t.Kind, in.Value)
		if nerr != nil {
			return TeamContactOut{}, nerr
		}
		if x, dup := v.find(tid, typ, norm, id); dup {
			return TeamContactOut{}, errs.New(errs.InvalidInput, "team contact edit: "+typ+" "+norm+" is already a contact of this team ("+x.ID.String()+")").
				WithHint("lore team contact remove --id " + id.String())
		}
		texts[schema.FieldContactValue], texts[schema.FieldContactValueNorm] = in.Value, norm
	}
	if err = putText(patch, texts); err != nil {
		return TeamContactOut{}, err
	}
	if in.Primary {
		if err = v.clearPrimaries(ctx, r, tid, typ, id); err != nil {
			return TeamContactOut{}, err
		}
		patch[schema.FieldContactPrimary] = value.BoolOf(true)
	}
	if in.NoPrimary {
		patch[schema.FieldContactPrimary] = value.BoolOf(false)
	}
	x, err := r.Update(ctx, schema.TableTeamContacts, id, patch)
	if err != nil {
		return TeamContactOut{}, err
	}
	r.Push(ctx)
	return TeamContactOut{Contact: v.contactOf(x), Verb: verbTeamContactEdited, Warnings: r.Warnings(), Where: r.Where(), label: r.Label()}, nil
}

func teamContactRemove(ctx context.Context, env profilestore.Store, in TeamContactRemoveIn) (TeamContactOut, error) {
	id, err := teamContactID(in.ID)
	if err != nil {
		return TeamContactOut{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = defaultTeamContactReason
	}
	r, done, err := env.Roster(ctx, true)
	defer done()
	if err != nil {
		return TeamContactOut{}, err
	}
	v, cur, err := openTeamContact(ctx, r, id, "lore team contact remove --id "+id.String())
	if err != nil {
		return TeamContactOut{}, err
	}
	if err = r.Delete(ctx, schema.TableTeamContacts, id, reason); err != nil {
		return TeamContactOut{}, err
	}
	r.Push(ctx)
	return TeamContactOut{Contact: v.contactOf(cur), Verb: verbTeamContactRemoved, Warnings: r.Warnings(), Where: r.Where(), label: r.Label()}, nil
}

func teamContactList(ctx context.Context, env profilestore.Store, in TeamContactListIn) (TeamContactListOut, error) {
	r, done, err := env.Roster(ctx, false)
	defer done()
	if err != nil {
		return TeamContactListOut{}, err
	}
	only := ""
	if strings.TrimSpace(in.Handle) != "" {
		h, herr := teamHandle(in.Handle)
		if herr != nil {
			return TeamContactListOut{}, herr
		}
		team, ok, ferr := findTeam(ctx, r, h)
		if ferr != nil {
			return TeamContactListOut{}, ferr
		}
		if !ok {
			return TeamContactListOut{}, teamMissErr(h, r.Label())
		}
		only = team.ID.String()
	}
	v, err := loadTeamContactView(ctx, r)
	if err != nil {
		return TeamContactListOut{}, err
	}
	typ := strings.TrimSpace(in.Type)
	out := TeamContactListOut{Contacts: []TeamContact{}, Where: r.Where(), label: r.Label()}
	for _, x := range v.contacts {
		c := v.contactOf(x)
		if (only == "" || c.TeamID == only) && (typ == "" || c.Type == typ) {
			out.Contacts = append(out.Contacts, c)
		}
	}
	sortTeamContacts(out.Contacts)
	return out, nil
}

// teamContactsByID is every team's contacts by team id (team show and
// team list), nil-safe for a roster written before the table existed.
func teamContactsByID(ctx context.Context, r profilestore.Roster) (map[string][]TeamContact, error) {
	v, err := loadTeamContactView(ctx, r)
	if err != nil {
		return nil, err
	}
	out := map[string][]TeamContact{}
	for id := range v.teams {
		if cs := v.of(id, ""); len(cs) > 0 {
			out[id] = cs
		}
	}
	return out, nil
}
