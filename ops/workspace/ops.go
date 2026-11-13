// Package workspace defines the `workspace` noun's operations on the 0.1.18
// core: add, list, show, edit, delete and search, with v0.1.17's flag spellings, --json envelopes and human
// lines (saas/cmd/cli/generated_commands.go, edit_commands.go,
// archive_commands.go and search_entities.go at 4e6ed1c). Delete asks
// first and takes --yes (v0.1.17 --confirm is refused by the CLI mount
// with the replacement); the row's tombstone keeps old copies from coming
// back.
package workspace

import (
	"context"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/content"
	"github.com/thesatellite-ai/lore/ops/internal/expand"
	"github.com/thesatellite-ai/lore/ops/internal/metashow"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (workspace.add → `lore workspace add`, POST /v1/workspaces).
const (
	Noun     = "workspace"
	OpAdd    = "workspace.add"
	OpList   = "workspace.list"
	OpShow   = "workspace.show"
	OpEdit   = "workspace.edit"
	OpDelete = "workspace.delete"
	OpSearch = "workspace.search"
)

// Workspace fields (schema/builtin_content2.go).
const (
	fieldTitle = "name"
)

// table is the workspace table.
const table row.Table = schema.TableWorkspaces

// Ops returns every workspace operation bound to env, for op.NewRegistry.
func Ops(env content.Env) []op.Entry {
	o := ops{c: content.Ops{Env: env, Table: table, Noun: Noun, Prefix: ids.PrefixWorkspace}}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Add a new workspace",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
			Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{
				{Title: "add a workspace", CLI: `lore workspace add --title "First" --body "the body"`, Want: "wsp_"},
			},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List workspaces",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.ReadErrors(),
			Examples: []op.Example{{Title: "list as JSON", CLI: "lore workspace list --json", Want: `"data"`}},
		}, o.list),
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show workspace details",
			Permission: op.PermRead, Access: op.AccessRead, Errors: expand.RevealErrors(content.ReadErrors()), Confirm: expand.RevealConfirm,
			Examples: []op.Example{{Title: "one workspace as JSON", CLI: "lore workspace show wsp_01jd9a00c001 --json", Want: `"data"`}},
		}, o.showExpand),
		op.Define(op.Spec{
			Name: OpEdit, Summary: "Edit workspace fields",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
			Confirm:  op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "rename a workspace", CLI: `lore workspace edit wsp_01jd9a00c001 --title "Site redesign"`, Want: "wsp_"}},
		}, o.edit),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a workspace (it goes to the trash; lore trash restore brings it back)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.DeleteErrors(),
			Confirm:  op.ConfirmRule{Always: true},
			Examples: []op.Example{{Title: "delete, restorable from the trash", CLI: `lore workspace delete wsp_01jd9a00c001 --reason "merged into billing" --yes`, Want: "wsp_"}},
		}, func(ctx context.Context, in DeleteIn) (content.WriteOut, error) {
			return o.c.Delete(ctx, in.ID, in.Reason)
		}).Asks(func(ctx context.Context, in DeleteIn) (string, error) { return o.c.DeleteQuestion(ctx, in.ID) }),
		op.Define(op.Spec{
			Name: OpSearch, Summary: "Full-text search across workspace name + body",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.SearchErrors(),
			Examples: []op.Example{{Title: "find a workspace by a word of its name or body", CLI: "lore workspace search refunds", Want: "wsp_"}},
		}, o.search),
	}
}

// ops binds the run functions to the shared content steps.
type ops struct{ c content.Ops }

// add creates one workspace in the repo's project (or --project).
func (o ops) add(ctx context.Context, in AddIn) (content.WriteOut, error) {
	if err := content.Required("title", in.Name); err != nil {
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
		fieldTitle: in.Name, content.FieldBody: body,
	})
	if err != nil {
		return content.WriteOut{}, err
	}
	if fields[content.FieldProject], err = value.RefOf(project.Any()); err != nil {
		return content.WriteOut{}, err
	}
	return o.c.Create(ctx, in.To, fields)
}

// list returns the live rows, every project unless --project.
func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	ctx = in.Deleted(ctx, o.c.Table) // DELETION 3.2: --include-deleted / --deleted-only
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
	rows, notes, err := o.c.Narrow(ctx, rows, in.MetaFlags)
	if err != nil {
		return ListOut{}, err
	}
	out := ListOut{SchemaVersion: content.SchemaVersion, Kind: KindList, Data: []Record{}, Notes: notes}
	for _, l := range rows {
		out.Data = append(out.Data, withMeta(ctx, o.c.Env, recordOf(o.c.Labeler(), l.Source, l.Row), l.Row))
	}
	out.Count = len(out.Data)
	return out, nil
}

// show returns one workspace.
func (o ops) show(ctx context.Context, in IDIn) (ShowOut, error) {
	_, src, r, err := o.c.Live(ctx, in.ID)
	if err != nil {
		return ShowOut{}, err
	}
	return ShowOut{SchemaVersion: content.SchemaVersion, Kind: KindShow, Data: withMeta(ctx, o.c.Env, recordOf(o.c.Labeler(), src, r), r)}, nil
}

// edit patches the fields whose flags were given.
func (o ops) edit(ctx context.Context, in EditIn) (content.WriteOut, error) {
	name, err := content.EditName(Noun, in.Name, in.Title)
	if err != nil {
		return content.WriteOut{}, err
	}
	patch, err := content.Texts(Noun, map[string]string{fieldTitle: name, content.FieldBody: in.Body})
	if err != nil {
		return content.WriteOut{}, err
	}
	if len(patch) == 0 && !engine.MetaBound(ctx) {
		return content.WriteOut{}, errs.New(errs.InvalidInput, "workspace edit: nothing to change").
			WithHint("give at least one of --name (or --title), --body")
	}
	return o.c.Patch(ctx, in.ID, patch, func(id string) string { return content.Mark + " workspace " + id + " updated\n" })
}

// search matches the query in every attached store.
func (o ops) search(ctx context.Context, in SearchIn) (SearchOut, error) {
	sc, repo, err := o.c.ReadScope(ctx, in.Repo, in.NoInherit, in.MasterOnly, in.AllRepos)
	if err != nil {
		return SearchOut{}, err
	}
	project, err := o.c.ProjectFilter(ctx, in.Project)
	if err != nil {
		return SearchOut{}, err
	}
	found, err := o.c.Search(ctx, content.SearchArgs{Query: in.Query, Limit: in.Limit, Columns: in.Columns, IncludeArchived: in.IncludeArchived, Store: in.Store},
		func(r row.Row) bool {
			return sc.Keeps(content.Field(r, content.FieldRepo), repo) && (project == "" || content.Field(r, content.FieldProject) == project)
		})
	if err != nil {
		return SearchOut{}, err
	}
	out := SearchOut{SchemaVersion: content.SchemaVersion, Kind: KindSearch, Query: in.Query}
	for _, f := range found {
		out.Data = append(out.Data, SearchHit{
			ID: f.Row.ID.String(), Score: f.Score, Snippet: f.Snippet, Store: content.SourceOf(o.c.Labeler(), f.Source), Label: content.Label(o.c.Labeler(), f.Source),
			Row: HitRow{Body: content.Field(f.Row, content.FieldBody), Name: content.Field(f.Row, fieldTitle)},
		})
		hm := metashow.Row(ctx, o.c.Env, f.Row) // MQ13: metadata on every read
		out.Data[len(out.Data)-1].Meta, out.Data[len(out.Data)-1].MetaMasked = hm.Meta, hm.MetaMasked
	}
	out.Count = len(out.Data)
	return out, nil
}

// recordOf is the v0.1.17 --json form of a workspace row.
func recordOf(lab content.Labeler, src store.Source, r row.Row) Record {
	return Record{
		ID: r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, CreatedBy: auditrec.Of(r).CreatedBy, UpdatedBy: auditrec.Of(r).UpdatedBy,
		ProjectID: content.Field(r, content.FieldProject),
		Name:      content.Field(r, fieldTitle),
		Body:      content.Field(r, content.FieldBody),
		// INTEG-AUDIT-1: the creator column, else the envelope created_by
		CreatedByActorID: content.CreatedBy(r),
		TagIDs:           content.TagIDs(r), Store: content.SourceOf(lab, src), Label: content.Label(lab, src),
	}
}
