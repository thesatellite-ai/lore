package profile

import (
	"context"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/teamprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	"github.com/thesatellite-ai/lore/schema"
)

// contact_type.list|add|edit and the effective contact-type list
// (TEAM_PROFILES 2.3, 3.3, TP-1, TS-2): the built-ins of logic/teamprofile
// with every live contact_types row laid over them by name. A row named
// like a built-in changes its label, link or retired flag; a new name adds
// a type. Nothing is seeded: `contact-type edit github --label …`
// materialises the built-in as a row with its natural id.

// Origins of an effective type, shown by contact-type list.
const (
	OriginBuiltin = "built-in"
	OriginChanged = "changed"
	OriginAdded   = "added"
)

// typeName is a contact type name: snake_case, the key contacts store.
var typeName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// EffectiveType is one contact type of the effective list.
type EffectiveType struct {
	teamprofile.ContactType
	// Origin is OriginBuiltin, OriginChanged or OriginAdded.
	Origin string
	// Row is the contact_types row, zero for an untouched built-in.
	Row row.Row
}

// effectiveTypes overlays rows (live contact_types rows) on the built-ins
// by name, sorted built-ins first in TP-1 order, then added names
// alphabetically. A row may not change Kind or Identity of a built-in
// identity type (github, gitlab, email): identity matching depends on
// them (2.3), so those two stay the built-in's whatever the row says.
func effectiveTypes(rows []row.Row) []EffectiveType {
	byName := map[string]row.Row{}
	for _, x := range rows {
		if n := text(x, schema.FieldContactTypeName); n != "" {
			byName[n] = x
		}
	}
	var out []EffectiveType
	seen := map[string]bool{}
	for _, b := range teamprofile.BuiltinContactTypes() {
		seen[b.Name] = true
		x, ok := byName[b.Name]
		if !ok {
			out = append(out, EffectiveType{ContactType: b, Origin: OriginBuiltin})
			continue
		}
		t := typeOfRow(x)
		if t.Label == "" {
			t.Label = b.Label
		}
		if b.Identity {
			t.Kind, t.Identity = b.Kind, b.Identity
		}
		if t.Kind == "" {
			t.Kind = b.Kind
		}
		out = append(out, EffectiveType{ContactType: t, Origin: OriginChanged, Row: x})
	}
	var added []EffectiveType
	for n, x := range byName {
		if !seen[n] {
			added = append(added, EffectiveType{ContactType: typeOfRow(x), Origin: OriginAdded, Row: x})
		}
	}
	slices.SortFunc(added, func(a, b EffectiveType) int { return strings.Compare(a.Name, b.Name) })
	return append(out, added...)
}

// typeOfRow reads a contact_types row.
func typeOfRow(x row.Row) teamprofile.ContactType {
	return teamprofile.ContactType{
		Name:        text(x, schema.FieldContactTypeName),
		Label:       text(x, schema.FieldContactTypeLabel),
		Kind:        text(x, schema.FieldContactTypeKind),
		Identity:    flag(x, schema.FieldContactTypeIdentity),
		URLTemplate: text(x, schema.FieldContactTypeURLTemplate),
		Retired:     flag(x, schema.FieldContactTypeRetired),
	}
}

// loadTypes reads the effective list from r.
func loadTypes(ctx context.Context, r profilestore.Roster) ([]EffectiveType, error) {
	rows, err := r.List(ctx, schema.TableContactTypes)
	if err != nil {
		return nil, err
	}
	return effectiveTypes(rows), nil
}

// findType is name in types.
func findType(types []EffectiveType, name string) (EffectiveType, bool) {
	i := slices.IndexFunc(types, func(t EffectiveType) bool { return t.Name == name })
	if i < 0 {
		return EffectiveType{}, false
	}
	return types[i], true
}

// typeForAdd is the live type name a new contact may use: unknown or
// retired is E_CONTACT_TYPE (3.2).
func typeForAdd(types []EffectiveType, name string) (EffectiveType, error) {
	t, ok := findType(types, name)
	switch {
	case !ok:
		return EffectiveType{}, errs.New(errs.ContactType, "contact type "+name+" is not in the list").
			WithHint("lore contact-type list; lore contact-type add --name " + name + " --kind …")
	case t.Retired:
		return EffectiveType{}, errs.New(errs.ContactType, "contact type "+name+" is retired").
			WithHint("lore contact-type edit --name " + name + " --unretire")
	}
	return t, nil
}

// TypeOut is one contact type in output.
type TypeOut struct {
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Kind        string `json:"kind"`
	Identity    bool   `json:"identity"`
	URLTemplate string `json:"url_template,omitempty"`
	Retired     bool   `json:"retired"`
	// Origin: built-in, changed (a row over a built-in) or added.
	Origin string `json:"origin"`
	// ID is the contact_types row id; empty for an untouched built-in.
	ID string `json:"id,omitempty"`
	// CreatedBy / UpdatedBy are the row envelope's writers (INTEG-AUDIT-1,
	// auditrec), last so the older keys keep their order.
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

func typeOut(t EffectiveType) TypeOut {
	o := TypeOut{Name: t.Name, Label: t.Label, Kind: t.Kind, Identity: t.Identity, URLTemplate: t.URLTemplate, Retired: t.Retired, Origin: t.Origin}
	if !t.Row.ID.IsZero() {
		o.ID = t.Row.ID.String()
		w := auditrec.Of(t.Row)
		o.CreatedBy, o.UpdatedBy = w.CreatedBy, w.UpdatedBy
	}
	return o
}

// ContactTypeListIn is the input of contact_type.list.
type ContactTypeListIn struct {
	Retired bool `json:"retired,omitempty" flag:"retired" desc:"include retired types"`
	// --created-by / --updated-by: the shared writer filters of every
	// noun list (INTEG-AUDIT-1; engine.List applies them).
	metalist.Audit
}

// ContactTypeListOut is the effective list.
type ContactTypeListOut struct {
	Types []TypeOut `json:"types"`
}

// Text is one line per type: name, kind, identity, origin, label, link.
func (o ContactTypeListOut) Text() string {
	var b strings.Builder
	for _, t := range o.Types {
		id := ""
		if t.Identity {
			id = "identity"
		}
		cols := []string{pad(t.Name, 12), pad(t.Kind, 7), pad(id, 9), pad(t.Origin, 9), pad(t.Label, 12), t.URLTemplate}
		if t.Retired {
			cols = append(cols, "(retired)")
		}
		b.WriteString(strings.TrimRight(strings.Join(cols, " "), " ") + "\n")
	}
	return b.String()
}

func contactTypeList(ctx context.Context, env Env, in ContactTypeListIn) (ContactTypeListOut, error) {
	r, done, err := env.Roster(ctx, false)
	defer done()
	if err != nil {
		return ContactTypeListOut{}, err
	}
	types, err := loadTypes(ctx, r)
	if err != nil {
		return ContactTypeListOut{}, err
	}
	out := ContactTypeListOut{Types: []TypeOut{}}
	for _, t := range types {
		if t.Retired && !in.Retired {
			continue
		}
		out.Types = append(out.Types, typeOut(t))
	}
	return out, nil
}

// ContactTypeAddIn is the input of contact_type.add.
type ContactTypeAddIn struct {
	Name        string `json:"name" arg:"0" desc:"snake_case name (bitbucket); never renamed"`
	Kind        string `json:"kind" flag:"kind" desc:"email, handle, phone, url or text: picks the value check"`
	Label       string `json:"label,omitempty" flag:"label" desc:"the shown name (Bitbucket)"`
	Identity    bool   `json:"identity,omitempty" flag:"identity" desc:"contacts of this type may be flagged for identity"`
	URLTemplate string `json:"url_template,omitempty" flag:"url-template" desc:"how actor show links a value: https://bitbucket.org/{value}"`
}

// ContactTypeOut is the added or edited type.
type ContactTypeOut struct {
	Type TypeOut `json:"type"`
	// Verb is "added" or "edited".
	Verb  string `json:"verb"`
	Where string `json:"where"`
	// Warnings are the engine's warnings of the write
	// (profilestore.WarningLine), printed after the success line.
	Warnings []string `json:"warnings,omitempty"`
}

// Text is "✓ contact type <name> added in <where>".
func (o ContactTypeOut) Text() string { return o.text() + profilestore.WarningText(o.Warnings) }

// text is Text without the warnings.
func (o ContactTypeOut) text() string {
	return work.Success + " contact type " + o.Type.Name + " " + o.Verb + " in " + o.Where + "\n"
}

// Verbs of ContactTypeOut.
const (
	verbAdded  = "added"
	verbEdited = "edited"
)

func contactTypeAdd(ctx context.Context, env Env, in ContactTypeAddIn) (ContactTypeOut, error) {
	name := strings.TrimSpace(in.Name)
	if !typeName.MatchString(name) {
		return ContactTypeOut{}, errs.New(errs.InvalidInput, "contact-type add: "+in.Name+" is not a snake_case name (bitbucket, work_slack)")
	}
	if !slices.Contains(schema.ContactKinds(), in.Kind) {
		return ContactTypeOut{}, errs.New(errs.InvalidInput, "contact-type add --kind "+in.Kind+": want one of "+strings.Join(schema.ContactKinds(), ", "))
	}
	if err := checkTemplate(in.URLTemplate); err != nil {
		return ContactTypeOut{}, err
	}
	var out ContactTypeOut
	err := openWrite(ctx, env, &out.Warnings, func(r profilestore.Roster) (bool, error) {
		types, err := loadTypes(ctx, r)
		if err != nil {
			return false, err
		}
		if t, ok := findType(types, name); ok {
			e := errs.New(errs.NameTaken, "contact type "+name+" is already in the list ("+t.Origin+")")
			if t.Retired {
				return false, e.WithHint("lore contact-type edit --name " + name + " --unretire")
			}
			return false, e.WithHint("lore contact-type edit --name " + name)
		}
		fields := map[string]value.Value{}
		for k, s := range map[string]string{
			schema.FieldContactTypeName: name, schema.FieldContactTypeKind: in.Kind,
			schema.FieldContactTypeLabel: in.Label, schema.FieldContactTypeURLTemplate: in.URLTemplate,
		} {
			if s = strings.TrimSpace(s); s == "" {
				continue
			}
			v, err := textValue(k, s)
			if err != nil {
				return false, err
			}
			fields[k] = v
		}
		fields[schema.FieldContactTypeIdentity] = value.BoolOf(in.Identity)
		x, err := r.Create(ctx, schema.TableContactTypes, fields)
		if err != nil {
			return false, err
		}
		out = ContactTypeOut{Type: typeOut(EffectiveType{ContactType: typeOfRow(x), Origin: OriginAdded, Row: x}), Verb: verbAdded, Where: r.Where()}
		return true, nil
	})
	return out, err
}

// ContactTypeEditIn is the input of contact_type.edit. kind and identity
// are not editable: contacts were normalised and flagged under them.
type ContactTypeEditIn struct {
	Name        string `json:"name" arg:"0" desc:"the type's name"`
	Label       string `json:"label,omitempty" flag:"label" desc:"the shown name"`
	URLTemplate string `json:"url_template,omitempty" flag:"url-template" desc:"how actor show links a value; {value} is replaced"`
	Retire      bool   `json:"retire,omitempty" flag:"retire" desc:"take it out of contact add; existing contacts keep it"`
	Unretire    bool   `json:"unretire,omitempty" flag:"unretire" desc:"bring a retired type back"`
}

func contactTypeEdit(ctx context.Context, env Env, in ContactTypeEditIn) (ContactTypeOut, error) {
	name := strings.TrimSpace(in.Name)
	if in.Retire && in.Unretire {
		return ContactTypeOut{}, errs.New(errs.InvalidInput, "contact-type edit: --retire and --unretire together; give one")
	}
	if in.Label == "" && in.URLTemplate == "" && !in.Retire && !in.Unretire {
		return ContactTypeOut{}, errs.New(errs.InvalidInput, "contact-type edit: nothing to change").
			WithHint("give --label, --url-template, --retire or --unretire")
	}
	if err := checkTemplate(in.URLTemplate); err != nil {
		return ContactTypeOut{}, err
	}
	patch := map[string]value.Value{}
	for k, s := range map[string]string{schema.FieldContactTypeLabel: in.Label, schema.FieldContactTypeURLTemplate: in.URLTemplate} {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		v, err := textValue(k, s)
		if err != nil {
			return ContactTypeOut{}, err
		}
		patch[k] = v
	}
	if in.Retire || in.Unretire {
		patch[schema.FieldContactTypeRetired] = value.BoolOf(in.Retire)
	}
	var out ContactTypeOut
	err := openWrite(ctx, env, &out.Warnings, func(r profilestore.Roster) (bool, error) {
		types, err := loadTypes(ctx, r)
		if err != nil {
			return false, err
		}
		t, ok := findType(types, name)
		if !ok {
			return false, errs.New(errs.ContactType, "contact type "+name+" is not in the list").WithHint("lore contact-type list")
		}
		var x row.Row
		if t.Row.ID.IsZero() {
			// TS-2: the first change of a built-in materialises it as a row
			// (natural id from its name), carrying the built-in's kind and
			// identity so the row is whole on its own.
			fields := map[string]value.Value{schema.FieldContactTypeIdentity: value.BoolOf(t.Identity)}
			for k, s := range map[string]string{schema.FieldContactTypeName: t.Name, schema.FieldContactTypeKind: t.Kind,
				schema.FieldContactTypeLabel: t.Label, schema.FieldContactTypeURLTemplate: t.URLTemplate} {
				if s == "" {
					continue
				}
				v, err := textValue(k, s)
				if err != nil {
					return false, err
				}
				fields[k] = v
			}
			for k, v := range patch {
				fields[k] = v
			}
			x, err = r.Create(ctx, schema.TableContactTypes, fields)
		} else {
			x, err = r.Update(ctx, schema.TableContactTypes, t.Row.ID, patch)
		}
		if err != nil {
			return false, err
		}
		eff, _ := findType(effectiveTypes([]row.Row{x}), name)
		out = ContactTypeOut{Type: typeOut(eff), Verb: verbEdited, Where: r.Where()}
		return true, nil
	})
	return out, err
}

// checkTemplate: a URL template, when given, holds {value} (or it would
// link every contact of the type to one page) and no user:password@
// (E_URL_CREDENTIALS): url_template is a text field the engine's URL strip
// does not cover by name (engine urlSweepExempt names this check), and a
// template is shown to everyone who reads a profile.
func checkTemplate(t string) error {
	t = strings.TrimSpace(t)
	if t == "" {
		return nil
	}
	if !strings.Contains(t, teamprofile.URLValue) {
		return errs.New(errs.InvalidInput, "--url-template "+t+" has no "+teamprofile.URLValue+" placeholder")
	}
	if u, err := url.Parse(strings.ReplaceAll(t, teamprofile.URLValue, templateProbe)); err == nil && u.User != nil {
		return errs.New(errs.URLUserinfo, "--url-template holds credentials; give the address without user:password@")
	}
	return nil
}

// templateProbe stands in for {value} when a template is parsed as a URL.
const templateProbe = "x"

// pad is s padded with spaces to n runes (a column of a text listing).
func pad(s string, n int) string {
	if s == "" {
		s = noneShown
	}
	if l := len([]rune(s)); l < n {
		return s + strings.Repeat(" ", n-l)
	}
	return s
}
