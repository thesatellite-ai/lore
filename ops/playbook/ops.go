// Package playbook defines the `playbook` noun's operations on the 0.1.18
// core: add, list, show, edit, archive, unarchive, delete and search, with
// v0.1.17's flag spellings, --json envelopes and human lines
// (saas/cmd/cli/generated_commands.go and friends at 4e6ed1c).
package playbook

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/content"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (playbook.add → `lore playbook add`, POST /v1/playbooks).
const (
	Noun        = "playbook"
	OpAdd       = "playbook.add"
	OpList      = "playbook.list"
	OpShow      = "playbook.show"
	OpEdit      = "playbook.edit"
	OpArchive   = "playbook.archive"
	OpUnarchive = "playbook.unarchive"
	OpDelete    = "playbook.delete"
	OpSearch    = "playbook.search"
)

// Playbook fields (schema/builtin_content.go).
const (
	fieldDescription = "description"
	fieldComposes    = "composes"
)

// table is the playbooks table.
const table row.Table = schema.TablePlaybooks

// Ops returns every playbook operation bound to env, for op.NewRegistry.
func Ops(env content.Env) []op.Entry {
	o := ops{c: content.Ops{Env: env, Table: table, Noun: Noun, Prefix: ids.PrefixPlaybook}}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Add a new playbook",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
			Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{
				{Title: "add a playbook", CLI: `lore playbook add --title "Release" --body "1. tag 2. push"`, Want: "pbk_"},
			},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List playbooks",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.ReadErrors(),
		}, o.list),
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show playbook details",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.ReadErrors(),
		}, o.show),
		op.Define(op.Spec{
			Name: OpEdit, Summary: "Edit playbook fields",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
			Confirm: op.ConfirmRule{MayAsk: true},
		}, o.edit),
		op.Define(op.Spec{
			Name: OpArchive, Summary: "Archive a playbook (soft-delete via archived_at)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
		}, func(ctx context.Context, in IDIn) (content.WriteOut, error) { return o.c.Archive(ctx, in.ID) }),
		op.Define(op.Spec{
			Name: OpUnarchive, Summary: "Unarchive a playbook (clear archived_at)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
		}, func(ctx context.Context, in IDIn) (content.WriteOut, error) { return o.c.Unarchive(ctx, in.ID) }),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a playbook (archive is usually what you want; a tombstone keeps old copies from returning)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.DeleteErrors(),
			Confirm: op.ConfirmRule{Always: true},
		}, func(ctx context.Context, in IDIn) (content.WriteOut, error) { return o.c.Delete(ctx, in.ID) }),
		op.Define(op.Spec{
			Name: OpSearch, Summary: "Full-text search across playbook name + body",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.SearchErrors(),
		}, o.search),
	}
}

// ops binds the run functions to the shared content steps.
type ops struct{ c content.Ops }

// description is v0.1.17's knowledgeDescription: --description, else the
// title; one of them must not be blank.
func description(desc, title string) (string, error) {
	d := strings.TrimSpace(desc)
	if d == "" {
		d = strings.TrimSpace(title)
	}
	if d == "" {
		return "", errs.New(errs.InvalidInput, "--description (or --title) must not be empty")
	}
	return d, nil
}

// add creates one playbook in the target store.
func (o ops) add(ctx context.Context, in AddIn) (content.WriteOut, error) {
	if err := content.Required("title", in.Title); err != nil {
		return content.WriteOut{}, err
	}
	desc, err := description(in.Description, in.Title)
	if err != nil {
		return content.WriteOut{}, err
	}
	body, err := content.Body(Noun, in.Body, in.Text)
	if err != nil {
		return content.WriteOut{}, err
	}
	project, err := o.c.ProjectOf(ctx, in.Project)
	if err != nil {
		return content.WriteOut{}, err
	}
	fields, err := content.Texts(Noun, map[string]string{
		content.FieldName: in.Title, fieldDescription: desc, content.FieldBody: body,
		content.FieldSourceKind: content.DefaultSource,
	})
	if err != nil {
		return content.WriteOut{}, err
	}
	if fields[content.FieldProject], err = value.RefOf(project.Any()); err != nil {
		return content.WriteOut{}, err
	}
	if in.CreatedBy != "" {
		if fields[content.FieldCreatedBy], err = content.ActorRef(Noun, in.CreatedBy); err != nil {
			return content.WriteOut{}, err
		}
	}
	if fields[content.FieldTrust], err = content.Trust(); err != nil {
		return content.WriteOut{}, err
	}
	return o.c.Create(ctx, in.To, fields)
}

// list returns the live playbooks (archived ones too, as v0.1.17).
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
		out.Data = append(out.Data, recordOf(l.Source, l.Row))
	}
	out.Count = len(out.Data)
	return out, nil
}

// show returns one playbook.
func (o ops) show(ctx context.Context, in IDIn) (ShowOut, error) {
	_, src, r, err := o.c.Live(ctx, in.ID)
	if err != nil {
		return ShowOut{}, err
	}
	return ShowOut{SchemaVersion: content.SchemaVersion, Kind: KindShow, Data: recordOf(src, r)}, nil
}

// edit patches the fields whose flags were given.
func (o ops) edit(ctx context.Context, in EditIn) (content.WriteOut, error) {
	patch, err := content.Texts(Noun, map[string]string{content.FieldName: in.Name, content.FieldBody: in.Body, fieldDescription: in.Description})
	if err != nil {
		return content.WriteOut{}, err
	}
	if len(patch) == 0 {
		return content.WriteOut{}, errs.New(errs.InvalidInput, "playbook edit: nothing to change").
			WithHint("give at least one of --name, --body, --description")
	}
	return o.c.Patch(ctx, in.ID, patch, func(id string) string { return content.Mark + " playbook " + id + " updated\n" })
}

// search matches the query over name + body in every attached store.
func (o ops) search(ctx context.Context, in SearchIn) (SearchOut, error) {
	project, err := o.c.ProjectFilter(ctx, in.Project)
	if err != nil {
		return SearchOut{}, err
	}
	found, err := o.c.Search(ctx, content.SearchArgs{Query: in.Query, Limit: in.Limit, Columns: in.Columns, IncludeArchived: in.IncludeArchived},
		func(r row.Row) bool { return project == "" || content.Field(r, content.FieldProject) == project })
	if err != nil {
		return SearchOut{}, err
	}
	out := SearchOut{SchemaVersion: content.SchemaVersion, Kind: KindSearch, Query: in.Query}
	for _, f := range found {
		out.Data = append(out.Data, SearchHit{
			ID: f.Row.ID.String(), Score: f.Score, Snippet: f.Snippet, Store: content.Source(f.Source),
			Row: HitRow{Body: content.Field(f.Row, content.FieldBody), Name: content.Field(f.Row, content.FieldName)},
		})
	}
	out.Count = len(out.Data)
	return out, nil
}

// recordOf is the v0.1.17 --json form of a playbook row.
func recordOf(src store.Source, r row.Row) Record {
	return Record{
		ID: r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		TrustScore: content.Number(r, content.FieldTrust), Confidence: content.Number(r, content.FieldConfidence),
		LastValidatedAt: content.Time(r, content.FieldValidated), ArchivedAt: content.Time(r, content.FieldArchivedAt),
		SourceKind: content.Field(r, content.FieldSourceKind), SourceRef: content.Field(r, content.FieldSourceRef),
		ProjectID: content.Field(r, content.FieldProject), Name: content.Field(r, content.FieldName),
		Description: content.Field(r, fieldDescription), Body: content.Field(r, content.FieldBody),
		Composes: content.Field(r, fieldComposes), CreatedByActorID: content.CreatedBy(r),
		TagIDs: content.TagIDs(r), Store: content.Source(src),
	}
}
