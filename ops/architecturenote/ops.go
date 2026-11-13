// Package architecturenote defines the `architecturenote` noun's operations on the 0.1.18
// core: add, list, show, edit, delete and search, with v0.1.17's flag spellings, --json envelopes and human
// lines (saas/cmd/cli/generated_commands.go, edit_commands.go,
// archive_commands.go and search_entities.go at 4e6ed1c). Delete asks
// first and takes --yes (v0.1.17 --confirm is refused by the CLI mount
// with the replacement); the row's tombstone keeps old copies from coming
// back.
package architecturenote

import (
	"context"

	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"github.com/thesatellite-ai/lore/ops/internal/creator"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"

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

// Operation names (architecturenote.add → `lore architecturenote add`, POST /v1/architecturenotes).
const (
	Noun     = "architecturenote"
	OpAdd    = "architecturenote.add"
	OpList   = "architecturenote.list"
	OpShow   = "architecturenote.show"
	OpEdit   = "architecturenote.edit"
	OpDelete = "architecturenote.delete"
	OpSearch = "architecturenote.search"
)

// Architecture note fields (schema/builtin_content2.go).
const (
	fieldTitle = "title"
)

// table is the architecture note table.
const table row.Table = schema.TableArchitectureNotes

// Ops returns every architecture note operation bound to env, for op.NewRegistry.
func Ops(env content.Env) []op.Entry {
	o := ops{c: content.Ops{Env: env, Table: table, Noun: Noun, Prefix: ids.PrefixArchitectureNote}}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Add a new architecture note",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
			Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{
				{Title: "add a architecture note", CLI: `lore architecturenote add --title "First" --body "the body"`, Want: "anr_"},
			},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List architecturenotes",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.ReadErrors(),
			Examples: []op.Example{{Title: "list as JSON", CLI: "lore architecturenote list --json", Want: `"data"`}},
		}, o.list),
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show architecture note details",
			Examples:   []op.Example{{Title: "show one as JSON", CLI: "lore architecturenote show --id anr_<id> --json", Want: `"data"`}},
			Permission: op.PermRead, Access: op.AccessRead, Errors: expand.RevealErrors(content.ReadErrors()), Confirm: expand.RevealConfirm,
		}, o.showExpand),
		op.Define(op.Spec{
			Name: OpEdit, Summary: "Edit architecture-note fields",
			Examples:   []op.Example{{Title: "change the body", CLI: `lore architecturenote edit --id anr_<id> --body "the new text"`, Want: "anr_"}},
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
			Confirm: op.ConfirmRule{MayAsk: true},
		}, o.edit),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a architecture note (it goes to the trash; lore trash restore brings it back)",
			Examples:   []op.Example{{Title: "delete without a terminal", CLI: "lore architecturenote delete --id anr_<id> --yes", Want: "anr_"}},
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.DeleteErrors(),
			Confirm: op.ConfirmRule{Always: true},
		}, func(ctx context.Context, in DeleteIn) (content.WriteOut, error) {
			return o.c.Delete(ctx, in.ID, in.Reason)
		}).Asks(func(ctx context.Context, in DeleteIn) (string, error) { return o.c.DeleteQuestion(ctx, in.ID) }),
		op.Define(op.Spec{
			Name: OpSearch, Summary: "Full-text search across architecture note title + body",
			Examples:   []op.Example{{Title: "search as JSON", CLI: `lore architecturenote search --query "retry" --json`, Want: `"data"`}},
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.SearchErrors(),
		}, o.search),
	}
}

// ops binds the run functions to the shared content steps.
type ops struct{ c content.Ops }

// add creates one architecture note in the repo's project (or --project).
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
		fieldTitle: in.Title, content.FieldBody: body,
	})
	if err != nil {
		return content.WriteOut{}, err
	}
	if fields[content.FieldProject], err = value.RefOf(project.Any()); err != nil {
		return content.WriteOut{}, err
	}
	// 6.2.3 / CQ12: --scope picks the store, repo and folder.
	if err = scopeAdd(ctx, o.c.Env, in.Scope, &in.To, &in.Repo, fields); err != nil {
		return content.WriteOut{}, err
	}
	if in.Repo != "" {
		repo, rerr := o.c.Env.RepoID(ctx, in.Repo)
		if rerr != nil {
			return content.WriteOut{}, rerr
		}
		if fields[content.FieldRepo], err = value.RefOf(repo.Any()); err != nil {
			return content.WriteOut{}, err
		}
	}
	if v := creator.Ref(ctx, o.c.Env); v != nil {
		fields[content.FieldCreatedBy] = v // the calling actor, never caller-set (INTEG-AUDIT-1)
	}
	return o.c.Create(ctx, in.To, fields)
}

// list returns the live rows in scope, every project unless --project.
func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	ctx = in.Deleted(ctx, o.c.Table) // DELETION 3.2: --include-deleted / --deleted-only
	if in.All {
		in.AllRepos, in.Archived = true, true // --all: every repo's rows, the archived ones too
	}
	sc, repo, err := o.c.ReadScope(ctx, in.Repo, in.NoInherit, in.MasterOnly, in.AllRepos)
	if err != nil {
		return ListOut{}, err
	}
	project, err := o.c.ProjectFilter(ctx, in.Project)
	if err != nil {
		return ListOut{}, err
	}
	var hid content.Hidden
	dirs := metalist.NewDirs(o.c.Env)
	rows, err := o.c.List(ctx, in.Store, func(r row.Row) bool {
		return (project == "" || content.Field(r, content.FieldProject) == project) && content.KeepCounted(ctx, &hid, dirs, sc, repo, in.Archived, r)
	})
	if err != nil {
		return ListOut{}, err
	}
	rows, notes, err := o.c.Narrow(ctx, rows, in.MetaFlags)
	if err != nil {
		return ListOut{}, err
	}
	out := ListOut{SchemaVersion: content.SchemaVersion, Kind: KindList, Data: []Record{}, Notes: hid.Notes(notes)}
	for _, l := range rows {
		out.Data = append(out.Data, withMeta(ctx, o.c.Env, recordOf(o.c.Labeler(), l.Source, l.Row), l.Row))
	}
	out.Count = len(out.Data)
	return out, nil
}

// show returns one architecture note.
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
	if err = scopeEdit(ctx, o.c.Env, in.Scope, &in.RebindRepo, &in.RebindMaster, patch); err != nil {
		return content.WriteOut{}, err
	}
	switch {
	case in.RebindRepo != "" && in.RebindMaster:
		return content.WriteOut{}, errs.New(errs.InvalidInput, "architecturenote edit: give --rebind-repo or --rebind-master, not both")
	case in.RebindRepo != "":
		repo, rerr := o.c.Env.RepoID(ctx, in.RebindRepo)
		if rerr != nil {
			return content.WriteOut{}, rerr
		}
		if patch[content.FieldRepo], err = value.RefOf(repo.Any()); err != nil {
			return content.WriteOut{}, err
		}
	case in.RebindMaster:
		patch[content.FieldRepo] = value.Null{}
	}
	if len(patch) == 0 && !engine.MetaBound(ctx) {
		return content.WriteOut{}, errs.New(errs.InvalidInput, "architecturenote edit: nothing to change").
			WithHint("give at least one of --title, --body, --scope, --rebind-repo, --rebind-master")
	}
	return o.c.Patch(ctx, in.ID, patch, func(id string) string { return content.Mark + " architecture-note " + id + " updated\n" })
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
			Row: HitRow{Body: content.Field(f.Row, content.FieldBody), Title: content.Field(f.Row, fieldTitle)},
		})
		hm := metashow.Row(ctx, o.c.Env, f.Row) // MQ13: metadata on every read
		out.Data[len(out.Data)-1].Meta, out.Data[len(out.Data)-1].MetaMasked = hm.Meta, hm.MetaMasked
	}
	out.Count = len(out.Data)
	return out, nil
}

// recordOf is the v0.1.17 --json form of a architecture note row.
func recordOf(lab content.Labeler, src store.Source, r row.Row) Record {
	return Record{
		ID: r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, CreatedBy: auditrec.Of(r).CreatedBy, UpdatedBy: auditrec.Of(r).UpdatedBy,
		ProjectID:        content.Field(r, content.FieldProject),
		RepoID:           content.Field(r, content.FieldRepo),
		Title:            content.Field(r, fieldTitle),
		Body:             content.Field(r, content.FieldBody),
		ScopeID:          content.Field(r, schema.FieldScope),
		CreatedByActorID: content.CreatedBy(r),
		TagIDs:           content.TagIDs(r), Store: content.SourceOf(lab, src), Label: content.Label(lab, src),
	}
}
