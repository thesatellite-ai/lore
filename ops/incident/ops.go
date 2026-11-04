// Package incident defines the `incident` noun's operations on the 0.1.18
// core: add, list, show, delete and search, with v0.1.17's flag spellings, --json envelopes and human
// lines (saas/cmd/cli/generated_commands.go, edit_commands.go,
// archive_commands.go and search_entities.go at 4e6ed1c). Delete asks
// first and takes --yes (v0.1.17 --confirm is refused by the CLI mount
// with the replacement); the row's tombstone keeps old copies from coming
// back.
package incident

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

// Operation names (incident.add → `lore incident add`, POST /v1/incidents).
const (
	Noun     = "incident"
	OpAdd    = "incident.add"
	OpList   = "incident.list"
	OpShow   = "incident.show"
	OpDelete = "incident.delete"
	OpSearch = "incident.search"
)

// Incident fields (schema/builtin_content2.go).
const (
	fieldTitle       = "title"
	fieldSeverityStr = "severity_str"
	fieldResolvedAt  = "resolved_at"
)

// table is the incident table.
const table row.Table = schema.TableIncidents

// Ops returns every incident operation bound to env, for op.NewRegistry.
func Ops(env content.Env) []op.Entry {
	o := ops{c: content.Ops{Env: env, Table: table, Noun: Noun, Prefix: ids.PrefixIncident}}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Add a new incident",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
			Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{
				{Title: "add a incident", CLI: `lore incident add --title "First" --body "the body"`, Want: "inc_"},
			},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List incidents",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.ReadErrors(),
			Examples: []op.Example{{Title: "list as JSON", CLI: "lore incident list --json", Want: `"data"`}},
		}, o.list),
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show incident details",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.ReadErrors(),
		}, o.show),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a incident (a tombstone keeps old copies from returning)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.DeleteErrors(),
			Confirm: op.ConfirmRule{Always: true},
		}, func(ctx context.Context, in IDIn) (content.WriteOut, error) { return o.c.Delete(ctx, in.ID) }),
		op.Define(op.Spec{
			Name: OpSearch, Summary: "Full-text search across incident title + body",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.SearchErrors(),
		}, o.search),
	}
}

// ops binds the run functions to the shared content steps.
type ops struct{ c content.Ops }

// add creates one incident in the repo's project (or --project).
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
	if in.CreatedBy != "" {
		if fields[content.FieldCreatedBy], err = content.ActorRef(Noun, in.CreatedBy); err != nil {
			return content.WriteOut{}, err
		}
	}
	return o.c.Create(ctx, in.To, fields)
}

// list returns the live rows, every project unless --project.
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

// show returns one incident.
func (o ops) show(ctx context.Context, in IDIn) (ShowOut, error) {
	_, src, r, err := o.c.Live(ctx, in.ID)
	if err != nil {
		return ShowOut{}, err
	}
	return ShowOut{SchemaVersion: content.SchemaVersion, Kind: KindShow, Data: recordOf(src, r)}, nil
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
	out := SearchOut{SchemaVersion: content.SchemaVersion, Kind: KindSearch, Query: in.Query, Data: []SearchHit{}}
	for _, f := range found {
		out.Data = append(out.Data, SearchHit{
			ID: f.Row.ID.String(), Score: f.Score, Snippet: f.Snippet, Store: content.Source(f.Source),
			Row: HitRow{Body: content.Field(f.Row, content.FieldBody), Title: content.Field(f.Row, fieldTitle)},
		})
	}
	out.Count = len(out.Data)
	return out, nil
}

// recordOf is the v0.1.17 --json form of a incident row.
func recordOf(src store.Source, r row.Row) Record {
	return Record{
		ID: r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		ProjectID:        content.Field(r, content.FieldProject),
		Title:            content.Field(r, fieldTitle),
		Body:             content.Field(r, content.FieldBody),
		SeverityStr:      content.Field(r, fieldSeverityStr),
		ResolvedAt:       content.Time(r, fieldResolvedAt),
		CreatedByActorID: content.CreatedBy(r),
		TagIDs:           content.TagIDs(r), Store: content.Source(src),
	}
}
