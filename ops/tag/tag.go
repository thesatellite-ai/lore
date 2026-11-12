// Package tag defines the `tag` noun's operations (layer L5): add, list,
// attach and detach, with v0.1.17's flag spellings (`lore tag add --name
// --color`, `lore tag attach|detach --on-table --on-id --tag`).
//
// Tags are rows of the tags table (per project, natural id from
// (project_id, name), so two machines creating the same tag write the same
// row). A row carries its tags in its own tag_ids list (decision O11): attach
// and detach edit that list on any row whose table declares the tags
// many-to-many relation, through the engine's one write path. There are no
// entity_tags rows any more.
package tag

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/internal/tagref"
	"github.com/thesatellite-ai/lore/ops/internal/textcut"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (`lore tag …`, REST /v1/tags).
const (
	Noun     = "tag"
	OpAdd    = "tag.add"
	OpList   = "tag.list"
	OpAttach = "tag.attach"
	OpDetach = "tag.detach"
)

// Envelope values of v0.1.17's --json (json_helper.go, constants KindTagList).
const (
	SchemaVersion = 1
	KindList      = "tag.list"
)

// Tag and row field names.
const (
	fieldProject = "project_id"
	fieldName    = "name"
	fieldColor   = "color"
	// fieldStatus is the status field of tasks and missions: a tag use on
	// a row whose status is done or cancelled counts as inactive
	// (v0.1.17 tagUsageCounts).
	fieldStatus = "status"
)

// inactiveStatuses are the status values whose rows count as inactive tag
// uses (v0.1.17: task/mission done|cancelled).
var inactiveStatuses = []string{"done", "cancelled"}

// snakeCase is the tag name rule of META 3.5: lowercase letters and digits
// in words joined by single underscores.
var snakeCase = regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)*$`)

// Env is what the tag operations use; *wire.App implements it.
//
// Invariant: every write goes through Create or Update (the engine's one
// write path: validation, scan, policy, audit).
type Env interface {
	Create(ctx context.Context, target store.Source, table row.Table, fields map[string]value.Value) (engine.Written, error)
	Update(ctx context.Context, ref engine.Ref, patch map[string]value.Value) (engine.Written, error)
	Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error)
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
	Stores() []store.Source
	// Project is the repo's own project (tags are per project).
	Project(ctx context.Context) (ids.ID[ids.Project], error)
}

// AddIn is the input of tag.add.
type AddIn struct {
	Name  string `json:"name" flag:"name" desc:"tag name (snake_case)"`
	Color string `json:"color,omitempty" flag:"color" desc:"optional hex color #RRGGBB"`
	To    string `json:"to,omitempty" flag:"to" alias:"store" desc:"store to write to (a name from .lore/stores.yaml, me, <store>/<folder>; default repo)"`
}

// AddOut is the output of tag.add.
type AddOut struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Store string `json:"store"`
}

// Text is v0.1.17's line: "✓ tag <name> <id>".
func (o AddOut) Text() string { return "✓ tag " + o.Name + " " + o.ID + "\n" }

// ListIn is the input of tag.list.
type ListIn struct {
	Store string `json:"store,omitempty" flag:"store" alias:"to" desc:"only tags of this store (default every attached store)"`
	// --created-by / --updated-by: the shared writer filters of every
	// noun list (INTEG-AUDIT-1; engine.List applies them).
	metalist.Audit
}

// ListOut is the output of tag.list: v0.1.17's envelope (count omitted
// when zero).
type ListOut struct {
	SchemaVersion int      `json:"schema_version"`
	Kind          string   `json:"kind"`
	Count         int      `json:"count,omitempty"`
	Data          []Record `json:"data"`
	// Uses is the per-tag usage the human form prints (v0.1.17 printed it
	// in text mode only).
	Uses []Usage `json:"-"`
}

// Usage is one tag's use counts: rows listing it, and those of them that
// are still active.
type Usage struct {
	Active int
	Total  int
}

// Record is one tag in v0.1.17's --json form (the ent Tag struct).
type Record struct {
	ID        string          `json:"id"`
	CreatedAt string          `json:"created_at,omitempty"`
	UpdatedAt string          `json:"updated_at,omitempty"`
	ProjectID string          `json:"project_id"`
	Name      string          `json:"name"`
	Color     string          `json:"color,omitempty"`
	Store     store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
	// CreatedBy / UpdatedBy are the row envelope's writers (INTEG-AUDIT-1,
	// auditrec), last so the older keys keep their order.
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// listNameWidth is v0.1.17's name column ("%-20s").
const listNameWidth = 20

// Text is v0.1.17's list: one line per tag with its use counts.
func (o ListOut) Text() string {
	if len(o.Data) == 0 {
		return "(no tags)\n"
	}
	var b strings.Builder
	for i, t := range o.Data {
		u := Usage{}
		if i < len(o.Uses) {
			u = o.Uses[i]
		}
		if u.Active == u.Total {
			fmt.Fprintf(&b, "%-*s %s  (%d uses)%s\n", listNameWidth, t.Name, t.ID, u.Active, opsmemory.LabelSuffix(t.Label))
		} else {
			fmt.Fprintf(&b, "%-*s %s  (%d active / %d total)%s\n", listNameWidth, t.Name, t.ID, u.Active, u.Total, opsmemory.LabelSuffix(t.Label))
		}
	}
	return b.String()
}

// BindIn is the input of tag.attach and tag.detach.
type BindIn struct {
	OnTable string `json:"on_table" flag:"on-table" desc:"target entity table (memories|rules|decisions|...)"`
	OnID    string `json:"on_id" flag:"on-id" desc:"target entity id"`
	Tag     string `json:"tag" flag:"tag" desc:"tag name"`
}

// AttachOut is the output of tag.attach.
type AttachOut struct {
	Tag     string `json:"tag"`
	TagID   string `json:"tag_id"`
	OnTable string `json:"on_table"`
	OnID    string `json:"on_id"`
}

// Text is v0.1.17's line.
func (o AttachOut) Text() string {
	return fmt.Sprintf("✓ tag %q attached to %s/%s\n", o.Tag, o.OnTable, o.OnID)
}

// DetachOut is the output of tag.detach: how many bindings went (0 or 1).
type DetachOut struct {
	Detached int `json:"detached"`
}

// Text is v0.1.17's line.
func (o DetachOut) Text() string { return fmt.Sprintf("✓ detached %d tag binding(s)\n", o.Detached) }

// writeErrors are the codes a tag write can return.
var writeErrors = []errs.Code{
	errs.InvalidInput, errs.InvalidID, errs.InvalidIdentifier, errs.NotFound, errs.AmbiguousID, errs.Unsupported,
	errs.SecretDetected, errs.NeedsConfirm, errs.Cancelled, errs.PolicyDenied, errs.ConfigInvalid,
	errs.Stale, errs.StoreReadOnly, errs.StoreLocked, errs.StoreUnreachable, errs.OldLore, errs.Internal,
}

// readErrors are the codes a tag read can return.
var readErrors = []errs.Code{errs.NotFound, errs.StoreUnreachable, errs.OldLore, errs.Internal}

// Ops returns every tag operation bound to env.
func Ops(env Env) []op.Entry {
	o := ops{env: env}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Create a new tag in the current project",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
			Confirm:  op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "add a tag", CLI: "lore tag add --name=cert_rotation", Want: "✓ tag cert_rotation tag_"}},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List tags in the current project",
			Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors,
			Examples: []op.Example{{Title: "list as JSON", CLI: "lore tag list --json", Want: `"data"`}},
		}, o.list),
		op.Define(op.Spec{
			Name: OpAttach, Summary: "Attach a tag to an entity",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
			Examples: []op.Example{{Title: "tag a task", CLI: "lore tag attach --on-table tasks --on-id tsk_<id> --tag cert_rotation", Want: "attached"}},
		}, o.attach),
		op.Define(op.Spec{
			Name: OpDetach, Summary: "Detach a tag from an entity",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
			Examples: []op.Example{{Title: "take a tag off a rule", CLI: "lore tag detach --tag billing --on-table rules --on-id rul_1f3a"}},
		}, o.detach),
		o.editEntry(),
	}
}

// ops binds the run functions to one Env.
type ops struct{ env Env }

// add creates the tag row (its id is the natural id of project and name;
// a second add of the same name is E_INVALID_INPUT from the engine).
func (o ops) add(ctx context.Context, in AddIn) (AddOut, error) {
	name := strings.TrimSpace(in.Name)
	if err := checkName(name); err != nil {
		return AddOut{}, err
	}
	project, err := o.env.Project(ctx)
	if err != nil {
		return AddOut{}, err
	}
	fields := map[string]value.Value{}
	if fields[fieldProject], err = value.RefOf(project.Any()); err != nil {
		return AddOut{}, err
	}
	if fields[fieldName], err = value.TextOf(name); err != nil {
		return AddOut{}, err
	}
	if in.Color != "" {
		if err = checkColor(in.Color); err != nil {
			return AddOut{}, err
		}
		if fields[fieldColor], err = value.TextOf(in.Color); err != nil {
			return AddOut{}, err
		}
	}
	w, err := o.env.Create(ctx, store.Source(in.To), schema.TableTags, fields)
	if err != nil {
		return AddOut{}, err
	}
	src := store.Source(in.To)
	if src == "" {
		if s, _, gerr := o.env.Get(ctx, engine.Ref{Table: schema.TableTags, ID: w.Row.ID}); gerr == nil {
			src = s
		}
	}
	return AddOut{ID: w.Row.ID.String(), Name: name, Store: string(src)}, nil
}

// checkName enforces snake_case for a new tag name (META 3.5); the error
// names the snake_case spelling.
func checkName(name string) error {
	if name == "" {
		return errs.New(errs.InvalidIdentifier, "tag: the name is empty").WithHint("lore tag add --name=<snake_case>")
	}
	if snakeCase.MatchString(name) {
		return nil
	}
	// a tag name is ASCII snake_case: letters outside a-z are dropped by
	// the converter, so "été" suggested a bare "t" and "שלום" nothing
	sug := SnakeOf(name)
	if sug == "" {
		return errs.New(errs.InvalidIdentifier, fmt.Sprintf("tag %q has no letter a-z or digit; a tag name is ASCII snake_case", name)).
			WithHint("lore tag add --name=<snake_case>, e.g. --name=cert_rotation; nothing written")
	}
	if strings.ContainsFunc(name, func(r rune) bool { return r > unicode.MaxASCII && unicode.IsLetter(r) }) {
		return errs.New(errs.InvalidIdentifier, fmt.Sprintf("tag %q is not snake_case: a tag name is ASCII (a-z, 0-9, _), so its other letters are dropped. Use: %s", name, sug)).
			WithHint("spell it in a-z, 0-9 and _; nothing written")
	}
	return errs.New(errs.InvalidIdentifier, fmt.Sprintf("tag %q is not snake_case. Use: %s", name, sug)).
		WithHint("nothing written")
}

// SnakeOf is the shared snake_case converter (META 3.5, R13): new tag
// names are checked against it, and lore migrate's tags_snake_case step
// renames old dash names with it.
func SnakeOf(name string) string {
	var b strings.Builder
	sep := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if sep && b.Len() > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r)
			sep = false
			continue
		}
		sep = true
	}
	return b.String()
}

// list returns the live tags of the current project, sorted by name, with
// their use counts across every table that carries tags.
func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	project, err := o.env.Project(ctx)
	if err != nil {
		return ListOut{}, err
	}
	sources := engine.ReadStoresOf(o.env)
	if in.Store != "" {
		sources = []store.Source{store.Source(in.Store)}
	}
	out := ListOut{SchemaVersion: SchemaVersion, Kind: KindList, Data: []Record{}}
	for _, src := range sources {
		rows, lerr := o.env.List(ctx, src, schema.TableTags)
		if lerr != nil {
			return ListOut{}, lerr
		}
		for _, r := range rows {
			if r.Deleted || field(r, fieldProject) != project.String() {
				continue
			}
			rec := Record{
				ID: r.ID.String(), CreatedAt: timeText(r.CreatedAt), UpdatedAt: timeText(r.UpdatedAt), CreatedBy: auditrec.Of(r).CreatedBy, UpdatedBy: auditrec.Of(r).UpdatedBy,
				ProjectID: field(r, fieldProject), Name: field(r, fieldName), Color: field(r, fieldColor),
			}
			if src != store.SourceRepo {
				rec.Store = opsmemory.SourceOf(opsmemory.LabelerOf(o.env), src)
				rec.Label = opsmemory.Label(opsmemory.LabelerOf(o.env), src)
			}
			out.Data = append(out.Data, rec)
		}
	}
	slices.SortStableFunc(out.Data, func(a, b Record) int { return strings.Compare(a.Name, b.Name) })
	out.Count = len(out.Data)
	uses, err := o.usage(ctx)
	if err != nil {
		return ListOut{}, err
	}
	for _, t := range out.Data {
		out.Uses = append(out.Uses, uses[t.ID])
	}
	return out, nil
}

// usage counts, per tag id, the live rows listing it in tag_ids across
// every attached store and every table with the tags relation.
func (o ops) usage(ctx context.Context) (map[string]Usage, error) {
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "tag: built-in schema", err)
	}
	out := map[string]Usage{}
	for _, def := range reg.Tables() {
		if !carriesTags(def) {
			continue
		}
		for _, src := range engine.ReadStoresOf(o.env) {
			rows, err := o.env.List(ctx, src, def.Name)
			if err != nil {
				return nil, err
			}
			for _, r := range rows {
				if r.Deleted {
					continue
				}
				active := !slices.Contains(inactiveStatuses, field(r, fieldStatus))
				for _, id := range tagsOf(r) {
					u := out[id]
					u.Total++
					if active {
						u.Active++
					}
					out[id] = u
				}
			}
		}
	}
	return out, nil
}

// attach adds the tag to the row's tag_ids (already there: nothing to
// write, same answer).
func (o ops) attach(ctx context.Context, in BindIn) (AttachOut, error) {
	ref, r, tags, err := o.bind(ctx, in, true)
	if err != nil {
		return AttachOut{}, err
	}
	tag := tags[0]
	out := AttachOut{Tag: in.Tag, TagID: tag.String(), OnTable: in.OnTable, OnID: in.OnID}
	have := tagsOf(r)
	if slices.Contains(have, tag.String()) {
		return out, nil
	}
	list, err := tagList(append(have, tag.String()))
	if err != nil {
		return AttachOut{}, err
	}
	if _, err := o.env.Update(ctx, ref, map[string]value.Value{schema.FieldTagIDs: list}); err != nil {
		return AttachOut{}, err
	}
	return out, nil
}

// detach removes the tag from the row's tag_ids; 0 when it was not there.
func (o ops) detach(ctx context.Context, in BindIn) (DetachOut, error) {
	ref, r, tags, err := o.bind(ctx, in, false)
	if err != nil {
		return DetachOut{}, err
	}
	have := tagsOf(r)
	keep := slices.DeleteFunc(slices.Clone(have), func(s string) bool {
		return slices.ContainsFunc(tags, func(t ids.AnyID) bool { return t.String() == s })
	})
	if len(keep) == len(have) {
		return DetachOut{}, nil
	}
	list, err := tagList(keep)
	if err != nil {
		return DetachOut{}, err
	}
	if _, err := o.env.Update(ctx, ref, map[string]value.Value{schema.FieldTagIDs: list}); err != nil {
		return DetachOut{}, err
	}
	return DetachOut{Detached: 1}, nil
}

// bind resolves an attach/detach: the target row (its table must carry
// tags), and the tag ids of the name in the current project, found by key
// (natural ids are per store): for attach the live tags, nearest store
// first, at least one (a typo never mints a tag); for detach every live
// tag of the name plus the v0.1.17 id (the tag may be deleted already).
func (o ops) bind(ctx context.Context, in BindIn, mustExist bool) (engine.Ref, row.Row, []ids.AnyID, error) {
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		return engine.Ref{}, row.Row{}, nil, errs.Wrap(errs.Internal, "tag: built-in schema", err)
	}
	table, err := row.ParseTable(strings.TrimSpace(in.OnTable))
	if err != nil {
		return engine.Ref{}, row.Row{}, nil, errs.Wrap(errs.InvalidInput, "tag: --on-table "+quote(in.OnTable)+" is not a table name", err)
	}
	def, ok := reg.Table(table)
	if !ok || !carriesTags(def) {
		return engine.Ref{}, row.Row{}, nil, errs.New(errs.Unsupported, "tag: rows of "+string(table)+" do not carry tags on the 0.1.18 core yet").
			WithHint("tables with tags: " + strings.Join(tagTables(reg), ", "))
	}
	id, err := ids.ParseAny(strings.TrimSpace(in.OnID))
	if err != nil || id.Prefix() != def.Prefix {
		return engine.Ref{}, row.Row{}, nil, lookup.BadID("tag: --on-id "+quote(in.OnID)+" is not an id of "+string(table)+" ("+string(def.Prefix)+"_…)", nil).
			WithHint("give the row's full " + string(def.Prefix) + "_ id, or the --on-table its prefix belongs to")
	}
	src, r, err := o.env.Get(ctx, engine.Ref{Table: table, ID: id})
	if err == nil && r.Deleted { // DELETION 3.2: E_IN_TRASH for a trashed target
		err = lookup.Gone(ctx, o.env, engine.Ref{Table: table, ID: id}, errs.New(errs.NotFound, "tag: "+id.String()+" is deleted"))
	}
	if err != nil {
		return engine.Ref{}, row.Row{}, nil, err
	}
	project, err := o.env.Project(ctx)
	if err != nil {
		return engine.Ref{}, row.Row{}, nil, err
	}
	name := strings.TrimSpace(in.Tag)
	var tags []ids.AnyID
	if mustExist {
		tags, err = tagref.Find(ctx, o.env, project.String(), name)
	} else {
		tags, err = tagref.Droppable(ctx, o.env, project.String(), name)
	}
	if err != nil {
		return engine.Ref{}, row.Row{}, nil, err
	}
	if mustExist && len(tags) == 0 {
		return engine.Ref{}, row.Row{}, nil, errs.New(errs.NotFound,
			fmt.Sprintf("tag %q not found in project; create with `lore tag add --name=%s`", name, name))
	}
	return engine.Ref{Store: src, Table: table, ID: id}, r, tags, nil
}

// carriesTags reports whether def declares the tags many-to-many.
func carriesTags(def schema.TableDef) bool {
	for _, rel := range def.Relations {
		if rel.Kind == schema.ManyToMany && rel.Name == schema.RelTags && rel.Field == schema.FieldTagIDs {
			return true
		}
	}
	return false
}

// tagTables lists the tables that carry tags (for an error hint).
func tagTables(reg *schema.Registry) []string {
	var out []string
	for _, def := range reg.Tables() {
		if carriesTags(def) {
			out = append(out, string(def.Name))
		}
	}
	slices.Sort(out)
	return out
}

// tagsOf is a row's tag ids.
func tagsOf(r row.Row) []string {
	// ItemStrings: the same ids whether the list was built in memory or
	// decoded from the row file.
	return value.ItemStrings(r.Fields[schema.FieldTagIDs])
}

// tagList builds a tag_ids value (Null when empty: the field is cleared).
func tagList(list []string) (value.Value, error) {
	if len(list) == 0 {
		return value.Null{}, nil
	}
	items := make([]value.Value, 0, len(list))
	for _, s := range list {
		id, err := ids.ParseAny(s)
		if err != nil {
			return nil, errs.Wrap(errs.InvalidID, "tag: "+quote(s)+" in tag_ids is not an id", err)
		}
		ref, err := value.RefOf(id)
		if err != nil {
			return nil, err
		}
		items = append(items, ref)
	}
	return value.ListOf(value.KindRef, items...)
}

// field returns the text form of a row field ("" when absent or null).
func field(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// maxQuoted bounds how much of a user value an error repeats.
const maxQuoted = 48

// quote renders a user value for an error, truncated.
func quote(s string) string {
	return `"` + textcut.Cut(s, maxQuoted, maxQuoted) + `"`
}

// timeText renders an envelope time as v0.1.17's JSON did (RFC 3339 with
// nanoseconds; "" when unset).
func timeText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// checkColor refuses a --color that is not the #RRGGBB the help promises
// (INTEG-KNOWLEDGE-2): v0.1.17 stored any text ("orange"), which renders
// nowhere. Only new writes are checked; stored colors are left as they are.
func checkColor(c string) error {
	ok := len(c) == len("#RRGGBB") && c[0] == '#'
	for i := 1; ok && i < len(c); i++ {
		ok = strings.ContainsRune("0123456789abcdefABCDEF", rune(c[i]))
	}
	if !ok {
		return errs.New(errs.InvalidInput, "tag: --color "+strconv.Quote(c)+" is not a hex color #RRGGBB").
			WithHint("for example --color '#1f6feb'")
	}
	return nil
}
