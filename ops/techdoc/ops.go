// Package techdoc defines the `techdoc` noun's operations on the 0.1.18
// core: add, list, show, delete and search of registered external
// documentation, with v0.1.17's flag spellings, --json envelopes and human
// lines (saas/cmd/cli/entity_commands.go and search_entities.go at
// 4e6ed1c). The fetched pages (tech_doc_pages) are machine-local in v0.1.17
// (lsync localTables) and stay on lore.db.
package techdoc

import (
	"context"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/content"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (techdoc.add → `lore techdoc add`, POST /v1/techdocs).
const (
	Noun     = "techdoc"
	OpAdd    = "techdoc.add"
	OpList   = "techdoc.list"
	OpShow   = "techdoc.show"
	OpDelete = "techdoc.delete"
	OpSearch = "techdoc.search"
)

// Techdoc fields (schema/builtin_content2.go).
const (
	fieldBaseURL     = "base_url"
	fieldDescription = "description"
)

// table is the tech_docs table.
const table row.Table = schema.TableTechDocs

// Ops returns every techdoc operation bound to env, for op.NewRegistry.
func Ops(env content.Env) []op.Entry {
	o := ops{c: content.Ops{Env: env, Table: table, Noun: Noun, Prefix: ids.PrefixTechDoc}}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Register external documentation (name + base URL)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
			Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{
				{Title: "register docs", CLI: `lore techdoc add --name "Go stdlib" --base-url https://pkg.go.dev/std`, Want: "tdc_"},
			},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List techdoc registrations",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.ReadErrors(),
			Examples: []op.Example{{Title: "list as JSON", CLI: "lore techdoc list --json", Want: `"data"`}},
		}, o.list),
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show techdoc details",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.ReadErrors(),
		}, o.show),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a techdoc (a tombstone keeps old copies from returning)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.DeleteErrors(),
			Confirm: op.ConfirmRule{Always: true},
		}, func(ctx context.Context, in DeleteIn) (content.WriteOut, error) {
			return o.c.Delete(ctx, in.ID, in.Reason)
		}),
		op.Define(op.Spec{
			Name: OpSearch, Summary: "Full-text search across techdoc name + description + base_url",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.SearchErrors(),
		}, o.search),
	}
}

// ops binds the run functions to the shared content steps.
type ops struct{ c content.Ops }

// add registers one techdoc ("✓ <id> <name>").
func (o ops) add(ctx context.Context, in AddIn) (content.WriteOut, error) {
	if err := content.Required(content.FieldName, in.Name); err != nil {
		return content.WriteOut{}, err
	}
	project, err := o.c.ProjectOf(ctx, in.Project)
	if err != nil {
		return content.WriteOut{}, err
	}
	fields, err := content.Texts(Noun, map[string]string{content.FieldName: in.Name, fieldBaseURL: in.BaseURL, fieldDescription: in.Description})
	if err != nil {
		return content.WriteOut{}, err
	}
	if fields[content.FieldProject], err = value.RefOf(project.Any()); err != nil {
		return content.WriteOut{}, err
	}
	out, err := o.c.Create(ctx, in.To, fields)
	if err != nil {
		return out, err
	}
	out.Human = content.Mark + " " + out.ID + " " + in.Name + "\n"
	return out, nil
}

// list returns the live techdocs, every project unless --project.
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
	rows, notes, err := o.c.Narrow(ctx, rows, in.MetaFlags)
	if err != nil {
		return ListOut{}, err
	}
	out := ListOut{SchemaVersion: content.SchemaVersion, Kind: KindList, Data: []Record{}, Notes: notes}
	for _, l := range rows {
		out.Data = append(out.Data, recordOf(o.c.Labeler(), l.Source, l.Row))
	}
	out.Count = len(out.Data)
	return out, nil
}

// show returns one techdoc.
func (o ops) show(ctx context.Context, in IDIn) (ShowOut, error) {
	_, src, r, err := o.c.Live(ctx, in.ID)
	if err != nil {
		return ShowOut{}, err
	}
	return ShowOut{SchemaVersion: content.SchemaVersion, Kind: KindShow, Data: recordOf(o.c.Labeler(), src, r)}, nil
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
	found, err := o.c.Search(ctx, content.SearchArgs{Query: in.Query, Limit: in.Limit, Columns: in.Columns, IncludeArchived: in.IncludeArchived},
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
			Row: HitRow{BaseURL: content.Field(f.Row, fieldBaseURL), Description: content.Field(f.Row, fieldDescription), Name: content.Field(f.Row, content.FieldName)},
		})
	}
	out.Count = len(out.Data)
	return out, nil
}

// recordOf is the v0.1.17 --json form of a techdoc row.
func recordOf(lab content.Labeler, src store.Source, r row.Row) Record {
	return Record{
		ID: r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		ProjectID: content.Field(r, content.FieldProject), Name: content.Field(r, content.FieldName),
		BaseURL: content.Field(r, fieldBaseURL), Description: content.Field(r, fieldDescription),
		TagIDs: content.TagIDs(r), Store: content.SourceOf(lab, src), Label: content.Label(lab, src),
	}
}
