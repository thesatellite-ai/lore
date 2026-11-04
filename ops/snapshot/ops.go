// Package snapshot defines the `snapshot` noun's operations on the 0.1.18
// core: add, list, show, archive, unarchive, delete and search, with
// v0.1.17's flag spellings, --json envelopes and human lines
// (saas/cmd/cli/entity_commands.go, archive_commands.go and
// search_entities.go at 4e6ed1c). Delete asks first and takes --yes.
package snapshot

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/content"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (snapshot.add → `lore snapshot add`, POST /v1/snapshots).
const (
	Noun        = "snapshot"
	OpAdd       = "snapshot.add"
	OpList      = "snapshot.list"
	OpShow      = "snapshot.show"
	OpArchive   = "snapshot.archive"
	OpUnarchive = "snapshot.unarchive"
	OpDelete    = "snapshot.delete"
	OpSearch    = "snapshot.search"
)

// Snapshot fields (schema/builtin_content2.go).
const (
	fieldTitle   = "title"
	fieldTakenAt = "taken_at"
)

// SourceKinds is the closed set of --source values, in v0.1.17's order
// (saas/pkg/constants SourceKind): source_kind feeds trust and filtering,
// so a typo is refused rather than stored.
func SourceKinds() []string {
	return []string{content.DefaultSource, "learn-from", "agent-proposal", "plugin", "imported", "migrated"}
}

// table is the snapshots table.
const table row.Table = schema.TableSnapshots

// Actorer is the optional Env upgrade add uses to fill created_by_actor_id
// with the current identity (wire.App has it), as v0.1.17 did.
type Actorer interface {
	Actor(ctx context.Context) (ids.ID[ids.Actor], error)
}

// Ops returns every snapshot operation bound to env, for op.NewRegistry.
func Ops(env content.Env) []op.Entry {
	o := ops{c: content.Ops{Env: env, Table: table, Noun: Noun, Prefix: ids.PrefixSnapshot}}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Add a snapshot (--title and --body required)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
			Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{
				{Title: "add a snapshot", CLI: `lore snapshot add --title "Release 1" --body "state of things"`, Want: "snp_"},
			},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List snapshots",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.ReadErrors(),
			Examples: []op.Example{{Title: "list as JSON", CLI: "lore snapshot list --json", Want: `"data"`}},
		}, o.list),
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show snapshot details",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.ReadErrors(),
		}, o.show),
		op.Define(op.Spec{
			Name: OpArchive, Summary: "Archive a snapshot (soft-delete via archived_at)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
		}, func(ctx context.Context, in IDIn) (content.WriteOut, error) { return o.c.Archive(ctx, in.ID) }),
		op.Define(op.Spec{
			Name: OpUnarchive, Summary: "Unarchive a snapshot (clear archived_at)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
		}, func(ctx context.Context, in IDIn) (content.WriteOut, error) { return o.c.Unarchive(ctx, in.ID) }),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a snapshot (archive is usually what you want; a tombstone keeps old copies from returning)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.DeleteErrors(),
			Confirm: op.ConfirmRule{Always: true},
		}, func(ctx context.Context, in IDIn) (content.WriteOut, error) { return o.c.Delete(ctx, in.ID) }),
		op.Define(op.Spec{
			Name: OpSearch, Summary: "Full-text search across snapshot title + body",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.SearchErrors(),
		}, o.search),
	}
}

// ops binds the run functions to the shared content steps.
type ops struct{ c content.Ops }

// add creates one snapshot taken now ("✓ <id> — <title>").
func (o ops) add(ctx context.Context, in AddIn) (content.WriteOut, error) {
	if err := content.Required(fieldTitle, in.Title); err != nil {
		return content.WriteOut{}, err
	}
	body, err := content.Body(Noun, in.Body, in.Text)
	if err != nil {
		return content.WriteOut{}, err
	}
	source := strings.TrimSpace(in.Source)
	if source == "" {
		source = content.DefaultSource
	}
	if !slices.Contains(SourceKinds(), source) {
		return content.WriteOut{}, errs.New(errs.InvalidInput, "--source "+content.QuoteID(in.Source)+": want one of "+strings.Join(SourceKinds(), ", "))
	}
	project, err := o.c.ProjectOf(ctx, in.Project)
	if err != nil {
		return content.WriteOut{}, err
	}
	fields, err := content.Texts(Noun, map[string]string{fieldTitle: in.Title, content.FieldBody: body, content.FieldSourceKind: source})
	if err != nil {
		return content.WriteOut{}, err
	}
	if fields[content.FieldProject], err = value.RefOf(project.Any()); err != nil {
		return content.WriteOut{}, err
	}
	if fields[content.FieldTrust], err = content.Trust(); err != nil {
		return content.WriteOut{}, err
	}
	fields[fieldTakenAt] = value.DateTimeFrom(opclock.Now(o.c.Env))
	if a, ok := o.c.Env.(Actorer); ok {
		if me, aerr := a.Actor(ctx); aerr == nil && !me.IsZero() {
			if _, r, gerr := o.c.Env.Get(ctx, actorRef(me)); gerr == nil && !r.Deleted {
				if fields[content.FieldCreatedBy], err = value.RefOf(me.Any()); err != nil {
					return content.WriteOut{}, err
				}
			}
		}
	}
	out, err := o.c.Create(ctx, in.To, fields)
	if err != nil {
		return out, err
	}
	out.Human = content.Mark + " " + out.ID + " — " + in.Title + "\n"
	return out, nil
}

// list returns the live, unarchived snapshots, every project unless
// --project.
func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	project, err := o.c.ProjectFilter(ctx, in.Project)
	if err != nil {
		return ListOut{}, err
	}
	rows, err := o.c.List(ctx, in.Store, func(r row.Row) bool {
		return content.Field(r, content.FieldArchivedAt) == "" && (project == "" || content.Field(r, content.FieldProject) == project)
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

// show returns one snapshot.
func (o ops) show(ctx context.Context, in IDIn) (ShowOut, error) {
	_, src, r, err := o.c.Live(ctx, in.ID)
	if err != nil {
		return ShowOut{}, err
	}
	return ShowOut{SchemaVersion: content.SchemaVersion, Kind: KindShow, Data: recordOf(o.c.Labeler(), src, r)}, nil
}

// search matches the query over title + body in every attached store.
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
			ID: f.Row.ID.String(), Score: f.Score, Snippet: f.Snippet, Store: content.Source(f.Source), Label: content.Label(o.c.Labeler(), f.Source),
			Row: HitRow{Body: content.Field(f.Row, content.FieldBody), Title: content.Field(f.Row, fieldTitle)},
		})
	}
	out.Count = len(out.Data)
	return out, nil
}

// recordOf is the v0.1.17 --json form of a snapshot row.
func recordOf(lab content.Labeler, src store.Source, r row.Row) Record {
	return Record{
		ID: r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		TrustScore: content.Number(r, content.FieldTrust), Confidence: content.Number(r, content.FieldConfidence),
		LastValidatedAt: content.Time(r, content.FieldValidated), ArchivedAt: content.Time(r, content.FieldArchivedAt),
		SourceKind: content.Field(r, content.FieldSourceKind), SourceRef: content.Field(r, content.FieldSourceRef),
		ProjectID: content.Field(r, content.FieldProject), RepoID: content.Field(r, content.FieldRepo),
		Title: content.Field(r, fieldTitle), Body: content.Field(r, content.FieldBody), TakenAt: content.Time(r, fieldTakenAt),
		CreatedByActorID: content.CreatedBy(r), TagIDs: content.TagIDs(r), Store: content.Source(src), Label: content.Label(lab, src),
	}
}

// actorRef is the engine ref of an actor row (any store).
func actorRef(a ids.ID[ids.Actor]) engine.Ref {
	return engine.Ref{Table: schema.TableActors, ID: a.Any()}
}
