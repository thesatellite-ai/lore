// Package cookbookrecipe defines the `cookbookrecipe` noun's operations on the 0.1.18
// core: add, list, show, edit, delete and search, with v0.1.17's flag spellings, --json envelopes and human
// lines (saas/cmd/cli/generated_commands.go, edit_commands.go,
// archive_commands.go and search_entities.go at 4e6ed1c). Delete asks
// first and takes --yes (v0.1.17 --confirm is refused by the CLI mount
// with the replacement); the row's tombstone keeps old copies from coming
// back.
package cookbookrecipe

import (
	"context"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/creator"

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

// Operation names (cookbookrecipe.add → `lore cookbookrecipe add`, POST /v1/cookbookrecipes).
const (
	Noun     = "cookbookrecipe"
	OpAdd    = "cookbookrecipe.add"
	OpList   = "cookbookrecipe.list"
	OpShow   = "cookbookrecipe.show"
	OpEdit   = "cookbookrecipe.edit"
	OpDelete = "cookbookrecipe.delete"
	OpSearch = "cookbookrecipe.search"
)

// Cookbook recipe fields (schema/builtin_content2.go).
const (
	fieldTitle    = "title"
	fieldLanguage = "language"
)

// table is the cookbook recipe table.
const table row.Table = schema.TableCookbookRecipes

// Ops returns every cookbook recipe operation bound to env, for op.NewRegistry.
func Ops(env content.Env) []op.Entry {
	o := ops{c: content.Ops{Env: env, Table: table, Noun: Noun, Prefix: ids.PrefixCookbookRecipe}}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Add a new cookbookrecipe",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
			Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{
				{Title: "add a cookbook recipe", CLI: `lore cookbookrecipe add --title "First" --body "the body"`, Want: "cbr_"},
			},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List cookbookrecipes",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.ReadErrors(),
			Examples: []op.Example{{Title: "list as JSON", CLI: "lore cookbookrecipe list --json", Want: `"data"`}},
		}, o.list),
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show cookbookrecipe details",
			Examples:   []op.Example{{Title: "show one as JSON", CLI: "lore cookbookrecipe show --id cbr_<id> --json", Want: `"data"`}},
			Permission: op.PermRead, Access: op.AccessRead, Errors: expand.RevealErrors(content.ReadErrors()), Confirm: expand.RevealConfirm,
		}, o.showExpand),
		op.Define(op.Spec{
			Name: OpEdit, Summary: "Edit cookbook-recipe fields",
			Examples:   []op.Example{{Title: "change the body", CLI: `lore cookbookrecipe edit --id cbr_<id> --body "the new text"`, Want: "cbr_"}},
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
			Confirm: op.ConfirmRule{MayAsk: true},
		}, o.edit),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a cookbookrecipe (it goes to the trash; lore trash restore brings it back)",
			Examples:   []op.Example{{Title: "delete without a terminal", CLI: "lore cookbookrecipe delete --id cbr_<id> --yes", Want: "cbr_"}},
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.DeleteErrors(),
			Confirm: op.ConfirmRule{Always: true},
		}, func(ctx context.Context, in DeleteIn) (content.WriteOut, error) {
			return o.c.Delete(ctx, in.ID, in.Reason)
		}).Asks(func(ctx context.Context, in DeleteIn) (string, error) { return o.c.DeleteQuestion(ctx, in.ID) }),
		op.Define(op.Spec{
			Name: OpSearch, Summary: "Full-text search across cookbookrecipe title + body + language",
			Examples:   []op.Example{{Title: "search as JSON", CLI: `lore cookbookrecipe search --query "retry" --json`, Want: `"data"`}},
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.SearchErrors(),
		}, o.search),
	}
}

// ops binds the run functions to the shared content steps.
type ops struct{ c content.Ops }

// add creates one cookbook recipe in the repo's project (or --project).
func (o ops) add(ctx context.Context, in AddIn) (content.WriteOut, error) {
	if err := content.Required("title", in.Title); err != nil {
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
		fieldTitle: in.Title, content.FieldBody: body, fieldLanguage: in.Language,
	})
	if err != nil {
		return content.WriteOut{}, err
	}
	if fields[content.FieldProject], err = value.RefOf(project.Any()); err != nil {
		return content.WriteOut{}, err
	}
	if v := creator.Ref(ctx, o.c.Env); v != nil {
		fields[content.FieldCreatedBy] = v // the calling actor, never caller-set (INTEG-AUDIT-1)
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

// show returns one cookbook recipe.
func (o ops) show(ctx context.Context, in IDIn) (ShowOut, error) {
	_, src, r, err := o.c.Live(ctx, in.ID)
	if err != nil {
		return ShowOut{}, err
	}
	return ShowOut{SchemaVersion: content.SchemaVersion, Kind: KindShow, Data: withMeta(ctx, o.c.Env, recordOf(o.c.Labeler(), src, r), r)}, nil
}

// edit patches the fields whose flags were given.
func (o ops) edit(ctx context.Context, in EditIn) (content.WriteOut, error) {
	patch, err := content.Texts(Noun, map[string]string{fieldTitle: in.Title, content.FieldBody: in.Body})
	if err != nil {
		return content.WriteOut{}, err
	}
	if len(patch) == 0 && !engine.MetaBound(ctx) {
		return content.WriteOut{}, errs.New(errs.InvalidInput, "cookbookrecipe edit: nothing to change").
			WithHint("give at least one of --title, --body")
	}
	return o.c.Patch(ctx, in.ID, patch, func(id string) string { return content.Mark + " cookbook-recipe " + id + " updated\n" })
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
			Row: HitRow{Body: content.Field(f.Row, content.FieldBody), Language: content.Field(f.Row, fieldLanguage), Title: content.Field(f.Row, fieldTitle)},
		})
		hm := metashow.Row(ctx, o.c.Env, f.Row) // MQ13: metadata on every read
		out.Data[len(out.Data)-1].Meta, out.Data[len(out.Data)-1].MetaMasked = hm.Meta, hm.MetaMasked
	}
	out.Count = len(out.Data)
	return out, nil
}

// recordOf is the v0.1.17 --json form of a cookbook recipe row.
func recordOf(lab content.Labeler, src store.Source, r row.Row) Record {
	return Record{
		ID: r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, CreatedBy: auditrec.Of(r).CreatedBy, UpdatedBy: auditrec.Of(r).UpdatedBy,
		ProjectID:        content.Field(r, content.FieldProject),
		Title:            content.Field(r, fieldTitle),
		Body:             content.Field(r, content.FieldBody),
		Language:         content.Field(r, fieldLanguage),
		CreatedByActorID: content.CreatedBy(r),
		TagIDs:           content.TagIDs(r), Store: content.SourceOf(lab, src), Label: content.Label(lab, src),
	}
}
