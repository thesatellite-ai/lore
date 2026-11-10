// Package incident defines the `incident` noun's operations on the 0.1.18
// core: add, edit (new, CLI_REFERENCE F2), list, show, delete and search, with v0.1.17's flag spellings, --json envelopes and human
// lines (saas/cmd/cli/generated_commands.go, edit_commands.go,
// archive_commands.go and search_entities.go at 4e6ed1c). Delete asks
// first and takes --yes (v0.1.17 --confirm is refused by the CLI mount
// with the replacement); the row's tombstone keeps old copies from coming
// back.
package incident

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
	"github.com/thesatellite-ai/lore/ops/internal/rowref"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// refErrors are add's and edit's: the write errors plus
// E_RESOURCE_AMBIGUOUS, since --ref takes a resource name (RES 3.11,
// rowref.Parse) and two live resources of that name refuse.
func refErrors() []errs.Code { return append(content.WriteErrors(), errs.ResourceAmbiguous) }

// Operation names (incident.add → `lore incident add`, POST /v1/incidents).
const (
	Noun     = "incident"
	OpAdd    = "incident.add"
	OpList   = "incident.list"
	OpShow   = "incident.show"
	OpEdit   = "incident.edit"
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
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: refErrors(),
			Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{
				{Title: "add a incident", CLI: `lore incident add --title "First" --body "the body"`, Want: "inc_"},
			},
		}, o.add),
		op.Define(op.Spec{
			Name: OpEdit, Summary: "Edit incident fields or add --ref pointers (only the flags given change)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: refErrors(),
			Confirm:  op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "link an incident to a row", CLI: "lore incident edit inc_… --ref res_… --relation affects", Want: "updated"}},
		}, o.edit),
		op.Define(op.Spec{
			Name: OpList, Summary: "List incidents",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.ReadErrors(),
			Examples: []op.Example{{Title: "list as JSON", CLI: "lore incident list --json", Want: `"data"`}},
		}, o.list),
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show incident details",
			Examples:   []op.Example{{Title: "show one as JSON", CLI: "lore incident show inc_<id> --json", Want: `"data"`}},
			Permission: op.PermRead, Access: op.AccessRead, Errors: expand.RevealErrors(content.ReadErrors()), Confirm: expand.RevealConfirm,
		}, o.showExpand),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a incident (it goes to the trash; lore trash restore brings it back)",
			Examples:   []op.Example{{Title: "delete without a terminal", CLI: "lore incident delete inc_<id> --yes", Want: "inc_"}},
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.DeleteErrors(),
			Confirm: op.ConfirmRule{Always: true},
		}, func(ctx context.Context, in DeleteIn) (content.WriteOut, error) {
			return o.c.Delete(ctx, in.ID, in.Reason)
		}).Asks(func(ctx context.Context, in DeleteIn) (string, error) { return o.c.DeleteQuestion(ctx, in.ID) }),
		op.Define(op.Spec{
			Name: OpSearch, Summary: "Full-text search across incident title + body",
			Examples:   []op.Example{{Title: "search as JSON", CLI: `lore incident search "retry" --json`, Want: `"data"`}},
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
	if v := creator.Ref(ctx, o.c.Env); v != nil {
		fields[content.FieldCreatedBy] = v // the calling actor, never caller-set (INTEG-AUDIT-1)
	}
	// --ref: parsed and F15-checked before anything is written (RES 3.11).
	rel, err := rowref.Relation(in.Relation, nil)
	if err != nil {
		return content.WriteOut{}, err
	}
	refs, err := rowref.Parse(ctx, o.c.Env, in.Ref)
	if err != nil {
		return content.WriteOut{}, err
	}
	if err = rowref.Check(ctx, o.c.Env, store.Source(in.To), refs); err != nil {
		return content.WriteOut{}, err
	}
	out, err := o.c.Create(ctx, in.To, fields)
	if err != nil {
		return out, err
	}
	id, err := ids.ParseAny(out.ID)
	if err != nil {
		return out, err
	}
	return out, rowref.Save(ctx, o.c.Env, store.Source(out.Store), table, project.String(), id, rel, refs)
}

// edit patches the given fields of one incident and adds --ref pointers.
// Flow: live row → refs parsed + F15-checked against its store → patch
// (when any field flag) → refs saved. Nothing given: E_INVALID_INPUT.
func (o ops) edit(ctx context.Context, in EditIn) (content.WriteOut, error) {
	id, src, cur, err := o.c.Live(ctx, in.ID)
	if err != nil {
		return content.WriteOut{}, err
	}
	rel, err := rowref.Relation(in.Relation, nil)
	if err != nil {
		return content.WriteOut{}, err
	}
	refs, err := rowref.Parse(ctx, o.c.Env, in.Ref)
	if err != nil {
		return content.WriteOut{}, err
	}
	if err = rowref.Check(ctx, o.c.Env, src, refs); err != nil {
		return content.WriteOut{}, err
	}
	patch, err := content.Texts(Noun, map[string]string{fieldTitle: in.Title, content.FieldBody: in.Body, fieldSeverityStr: in.Severity})
	if err != nil {
		return content.WriteOut{}, err
	}
	hasMeta := len(in.Meta) > 0 || len(in.MetaJSON) > 0 || in.MetaStdin != ""
	if len(patch) == 0 && len(refs) == 0 && !hasMeta {
		return content.WriteOut{}, errs.New(errs.InvalidInput, "incident edit: nothing to change").
			WithHint("give at least one of --title, --body, --severity, --ref, --meta")
	}
	line := content.Mark + " " + id.String() + " updated\n"
	out := content.WriteOut{ID: id.String(), Store: string(src), Human: line}
	if len(patch) > 0 || hasMeta {
		w, uerr := o.c.Env.Update(ctx, engine.Ref{Store: src, Table: table, ID: id}, patch)
		if uerr != nil {
			return content.WriteOut{}, uerr
		}
		out = content.Written(src, w, line)
	}
	return out, rowref.Save(ctx, o.c.Env, src, table, content.Field(cur, content.FieldProject), id, rel, refs)
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

// show returns one incident.
func (o ops) show(ctx context.Context, in IDIn) (ShowOut, error) {
	_, src, r, err := o.c.Live(ctx, in.ID)
	if err != nil {
		return ShowOut{}, err
	}
	refs, err := rowref.List(ctx, o.c.Env, table, r.ID, nil)
	if err != nil {
		return ShowOut{}, err
	}
	return ShowOut{SchemaVersion: content.SchemaVersion, Kind: KindShow, Data: withMeta(ctx, o.c.Env, recordOf(o.c.Labeler(), src, r), r), Refs: refs}, nil
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

// recordOf is the v0.1.17 --json form of a incident row.
func recordOf(lab content.Labeler, src store.Source, r row.Row) Record {
	return Record{
		ID: r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, CreatedBy: auditrec.Of(r).CreatedBy, UpdatedBy: auditrec.Of(r).UpdatedBy,
		ProjectID:        content.Field(r, content.FieldProject),
		Title:            content.Field(r, fieldTitle),
		Body:             content.Field(r, content.FieldBody),
		SeverityStr:      content.Field(r, fieldSeverityStr),
		ResolvedAt:       content.Time(r, fieldResolvedAt),
		CreatedByActorID: content.CreatedBy(r),
		TagIDs:           content.TagIDs(r), Store: content.SourceOf(lab, src), Label: content.Label(lab, src),
	}
}
