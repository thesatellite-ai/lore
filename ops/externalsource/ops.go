// Package externalsource defines the `external-source` noun's operations on
// the 0.1.18 core: add, list, enable and disable of the sources learn-from
// reads, with v0.1.17's flag spellings, --json envelope and human lines
// (saas/cmd/cli/entity_commands.go at 4e6ed1c). v0.1.17 had no show,
// delete or search for this noun, and neither does the core.
package externalsource

import (
	"context"
	"encoding/json"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/content"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (external_source.add → `lore external-source add`,
// POST /v1/external_sources).
const (
	Noun      = "external_source"
	OpAdd     = "external_source.add"
	OpList    = "external_source.list"
	OpEnable  = "external_source.enable"
	OpDisable = "external_source.disable"
)

// External-source fields (schema/builtin_content2.go).
const (
	fieldKind    = "kind"
	fieldConfig  = "config_json"
	fieldEnabled = "enabled"
)

// label is the noun as v0.1.17's lines print it.
const label = "external-source"

// table is the external_sources table.
const table row.Table = schema.TableExternalSources

// Ops returns every external-source operation bound to env.
func Ops(env content.Env) []op.Entry {
	o := ops{c: content.Ops{Env: env, Table: table, Noun: label, Prefix: ids.PrefixExternalSource}}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Register a new external source (disabled by default)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
			Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{
				{Title: "register a source", CLI: `lore external-source add --name issues --kind github`, Want: "added"},
			},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List registered external sources",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.ReadErrors(),
		}, o.list),
		op.Define(op.Spec{
			Name: OpEnable, Summary: "enable an external source",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
		}, func(ctx context.Context, in IDIn) (content.WriteOut, error) { return o.enable(ctx, in.ID, true) }),
		op.Define(op.Spec{
			Name: OpDisable, Summary: "disable an external source",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
		}, func(ctx context.Context, in IDIn) (content.WriteOut, error) { return o.enable(ctx, in.ID, false) }),
	}
}

// ops binds the run functions to the shared content steps.
type ops struct{ c content.Ops }

// add registers one disabled source ("✓ external-source <name> added").
// --config must be JSON (v0.1.17 stored any text; a blob that is not JSON
// could never be read by learn-from).
func (o ops) add(ctx context.Context, in AddIn) (content.WriteOut, error) {
	if err := content.Required(content.FieldName, in.Name); err != nil {
		return content.WriteOut{}, err
	}
	if err := content.Required(fieldKind, in.Kind); err != nil {
		return content.WriteOut{}, err
	}
	if in.Config != "" && !json.Valid([]byte(in.Config)) {
		return content.WriteOut{}, errs.New(errs.InvalidInput, label+" add: --config is not valid JSON")
	}
	project, err := o.c.ProjectOf(ctx, in.Project)
	if err != nil {
		return content.WriteOut{}, err
	}
	fields, err := content.Texts(label, map[string]string{content.FieldName: in.Name, fieldKind: in.Kind, fieldConfig: in.Config})
	if err != nil {
		return content.WriteOut{}, err
	}
	if fields[content.FieldProject], err = value.RefOf(project.Any()); err != nil {
		return content.WriteOut{}, err
	}
	fields[fieldEnabled] = value.BoolOf(false)
	out, err := o.c.Create(ctx, in.To, fields)
	if err != nil {
		return out, err
	}
	out.Human = content.Mark + " " + label + " " + in.Name + " added\n"
	return out, nil
}

// enable sets enabled ("✓ external-source <id> enabled|disabled").
func (o ops) enable(ctx context.Context, raw string, on bool) (content.WriteOut, error) {
	verb := "disabled"
	if on {
		verb = "enabled"
	}
	return o.c.Patch(ctx, raw, map[string]value.Value{fieldEnabled: value.BoolOf(on)},
		func(id string) string { return content.Mark + " " + label + " " + id + " " + verb + "\n" })
}

// list returns the live sources, every project unless --project.
func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	project, err := o.c.ProjectFilter(ctx, in.Project)
	if err != nil {
		return ListOut{}, err
	}
	rows, err := o.c.List(ctx, in.Store, func(r row.Row) bool {
		return project == "" || content.Field(r, content.FieldProject) == project
	})
	if err != nil {
		return ListOut{}, err
	}
	out := ListOut{SchemaVersion: content.SchemaVersion, Kind: KindList, Data: []Record{}}
	for _, l := range rows {
		out.Data = append(out.Data, recordOf(o.c.Labeler(), l.Source, l.Row))
	}
	out.Count = len(out.Data)
	return out, nil
}

// recordOf is the v0.1.17 --json form of an external-source row.
func recordOf(lab content.Labeler, src store.Source, r row.Row) Record {
	on, _ := r.Fields[fieldEnabled].(value.Bool)
	return Record{
		ID: r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		ProjectID: content.Field(r, content.FieldProject), Kind: content.Field(r, fieldKind),
		Name: content.Field(r, content.FieldName), ConfigJSON: content.Field(r, fieldConfig),
		Enabled: on.Bool(), TagIDs: content.TagIDs(r), Store: content.Source(src), Label: content.Label(lab, src),
	}
}
