// Package pattern defines the `pattern` noun's operations on the 0.1.18
// core: add, list, show, edit, archive, unarchive, delete and search, with
// v0.1.17's flag spellings, --json envelopes and human lines
// (saas/cmd/cli/generated_commands.go, edit_commands.go, archive_commands.go
// and search_entities.go at 4e6ed1c). Delete asks first and takes --yes
// (v0.1.17 --confirm is refused by the CLI mount with the replacement).
package pattern

import (
	"context"

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

// Operation names (pattern.add → `lore pattern add`, POST /v1/patterns).
const (
	Noun        = "pattern"
	OpAdd       = "pattern.add"
	OpList      = "pattern.list"
	OpShow      = "pattern.show"
	OpEdit      = "pattern.edit"
	OpArchive   = "pattern.archive"
	OpUnarchive = "pattern.unarchive"
	OpDelete    = "pattern.delete"
	OpSearch    = "pattern.search"
)

// Pattern fields (schema/builtin_content.go).
const (
	fieldTitle      = "title"
	fieldLanguage   = "language"
	fieldSuperseded = "superseded_by_id"
)

// table is the patterns table.
const table row.Table = schema.TablePatterns

// Ops returns every pattern operation bound to env, for op.NewRegistry.
func Ops(env content.Env) []op.Entry {
	o := ops{c: content.Ops{Env: env, Table: table, Noun: Noun, Prefix: ids.PrefixPattern}}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Add a new pattern",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
			Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{
				{Title: "add a pattern", CLI: `lore pattern add --title "Retry wrapper" --body "Wrap calls in retry.Do"`, Want: "pat_"},
			},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List patterns",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.ReadErrors(),
			Examples: []op.Example{{Title: "list as JSON", CLI: "lore pattern list --json", Want: `"data"`}},
		}, o.list),
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show pattern details",
			Permission: op.PermRead, Access: op.AccessRead, Errors: expand.RevealErrors(content.ReadErrors()), Confirm: expand.RevealConfirm,
		}, o.showExpand),
		op.Define(op.Spec{
			Name: OpEdit, Summary: "Edit pattern fields (use `--supersedes` on add for audited body changes)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
			Confirm: op.ConfirmRule{MayAsk: true},
		}, o.edit),
		op.Define(op.Spec{
			Name: OpArchive, Summary: "Archive a pattern (soft-delete via archived_at)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
		}, func(ctx context.Context, in IDIn) (content.WriteOut, error) { return o.c.Archive(ctx, in.ID) }),
		op.Define(op.Spec{
			Name: OpUnarchive, Summary: "Unarchive a pattern (clear archived_at)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.WriteErrors(),
		}, func(ctx context.Context, in IDIn) (content.WriteOut, error) { return o.c.Unarchive(ctx, in.ID) }),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a pattern (to the trash; archive is usually what you want)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: content.DeleteErrors(),
			Confirm: op.ConfirmRule{Always: true},
		}, func(ctx context.Context, in DeleteIn) (content.WriteOut, error) {
			return o.c.Delete(ctx, in.ID, in.Reason)
		}),
		op.Define(op.Spec{
			Name: OpSearch, Summary: "Full-text search across pattern title + body",
			Permission: op.PermRead, Access: op.AccessRead, Errors: content.SearchErrors(),
		}, o.search),
	}
}

// ops binds the run functions to the shared content steps.
type ops struct{ c content.Ops }

// add creates one pattern. With --supersedes the old pattern is checked
// first and pointed at the new one after; if that fails the new pattern is
// deleted again (v0.1.17 supersede.go).
func (o ops) add(ctx context.Context, in AddIn) (content.WriteOut, error) {
	if err := content.Required(fieldTitle, in.Title); err != nil {
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
		content.FieldSourceKind: content.DefaultSource,
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
	if in.CreatedBy != "" {
		if fields[content.FieldCreatedBy], err = content.ActorRef(Noun, in.CreatedBy); err != nil {
			return content.WriteOut{}, err
		}
	}
	if fields[content.FieldTrust], err = content.Trust(); err != nil {
		return content.WriteOut{}, err
	}
	var old ids.AnyID
	var oldSrc store.Source
	if in.Supersedes != "" {
		if old, oldSrc, err = o.checkSupersedes(ctx, project, in.Supersedes); err != nil {
			return content.WriteOut{}, err
		}
	}
	out, err := o.c.Create(ctx, in.To, fields)
	if err != nil || old.IsZero() {
		return out, err
	}
	return out, o.finishSupersedes(ctx, oldSrc, old, store.Source(out.Store), out.ID)
}

// checkSupersedes validates --supersedes before anything is written.
func (o ops) checkSupersedes(ctx context.Context, project ids.ID[ids.Project], raw string) (ids.AnyID, store.Source, error) {
	id, src, r, err := o.c.Live(ctx, raw)
	// v0.1.17 parity: a malformed or other-noun id is E_NOT_FOUND here,
	// like a well-formed unknown one (plain id arguments keep E_INVALID_ID).
	if errs.Has(err, errs.NotFound) || errs.Has(err, errs.InvalidID) || (err == nil && content.Field(r, content.FieldProject) != project.String()) {
		return ids.AnyID{}, "", errs.New(errs.NotFound, "--supersedes "+content.QuoteID(raw)+": no pattern with that id in this project").
			WithHint("run `lore pattern list` to see the ids")
	}
	if err != nil {
		return ids.AnyID{}, "", err
	}
	if next := content.Field(r, fieldSuperseded); next != "" {
		return ids.AnyID{}, "", errs.New(errs.InvalidInput, "--supersedes "+id.String()+": that pattern was already replaced by "+next).
			WithHint("supersede " + next + " instead, so the chain stays one line")
	}
	return id, src, nil
}

// finishSupersedes points old at the new pattern; on failure the new
// pattern is deleted again and the marking error returned.
func (o ops) finishSupersedes(ctx context.Context, oldSrc store.Source, old ids.AnyID, newSrc store.Source, newRaw string) error {
	newID, err := ids.ParseAny(newRaw)
	if err != nil {
		return errs.Wrap(errs.Internal, "pattern add: new id", err)
	}
	ref, err := value.RefOf(newID)
	if err != nil {
		return err
	}
	_, err = o.c.Env.Update(ctx, engine.Ref{Store: oldSrc, Table: table, ID: old}, map[string]value.Value{fieldSuperseded: ref})
	if err == nil {
		return nil
	}
	// the rolled-back row is purged, not trashed: "nothing was kept" (later 498)
	if _, derr := o.c.Env.Delete(engine.WithPurge(ctx, &engine.PurgeRequest{Asked: true, Confirmed: true}), engine.Ref{Store: newSrc, Table: table, ID: newID}); derr != nil {
		return errs.Wrap(errs.CodeOf(err), "pattern add: could not mark "+old.String()+" superseded by "+newRaw, err).
			WithHint("the new pattern " + newRaw + " was written but could not be removed again; delete it with `lore pattern delete " + newRaw + " --yes`")
	}
	return errs.Wrap(errs.CodeOf(err), "pattern add: could not mark "+old.String()+" superseded by "+newRaw+"; nothing was kept", err)
}

// list returns the live patterns in scope (archived ones too, as v0.1.17).
func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	ctx = in.Deleted(ctx, o.c.Table) // DELETION 3.2: --include-deleted / --deleted-only
	sc, repo, err := o.c.ReadScope(ctx, in.Repo, in.NoInherit, in.MasterOnly, in.AllRepos)
	if err != nil {
		return ListOut{}, err
	}
	project, err := o.c.ProjectFilter(ctx, in.Project)
	if err != nil {
		return ListOut{}, err
	}
	rows, err := o.c.List(ctx, in.Store, func(r row.Row) bool {
		return sc.Keeps(content.Field(r, content.FieldRepo), repo) && (project == "" || content.Field(r, content.FieldProject) == project)
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

// show returns one pattern.
func (o ops) show(ctx context.Context, in IDIn) (ShowOut, error) {
	_, src, r, err := o.c.Live(ctx, in.ID)
	if err != nil {
		return ShowOut{}, err
	}
	return ShowOut{SchemaVersion: content.SchemaVersion, Kind: KindShow, Data: withMeta(ctx, o.c.Env, recordOf(o.c.Labeler(), src, r), r)}, nil
}

// edit patches the fields whose flags were given.
func (o ops) edit(ctx context.Context, in EditIn) (content.WriteOut, error) {
	patch, err := content.Texts(Noun, map[string]string{fieldTitle: in.Title, content.FieldBody: in.Body, fieldLanguage: in.Language})
	if err != nil {
		return content.WriteOut{}, err
	}
	if err = scopeEdit(ctx, o.c.Env, in.Scope, &in.RebindRepo, &in.RebindMaster, patch); err != nil {
		return content.WriteOut{}, err
	}
	switch {
	case in.RebindRepo != "" && in.RebindMaster:
		return content.WriteOut{}, errs.New(errs.InvalidInput, "pattern edit: give --rebind-repo or --rebind-master, not both")
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
		return content.WriteOut{}, errs.New(errs.InvalidInput, "pattern edit: nothing to change").
			WithHint("give at least one of --title, --body, --language, --scope, --rebind-repo, --rebind-master")
	}
	return o.c.Patch(ctx, in.ID, patch, func(id string) string { return content.Mark + " pattern " + id + " updated\n" })
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
			ID: f.Row.ID.String(), Score: f.Score, Snippet: f.Snippet, Store: content.SourceOf(o.c.Labeler(), f.Source), Label: content.Label(o.c.Labeler(), f.Source),
			Row: HitRow{Body: content.Field(f.Row, content.FieldBody), Title: content.Field(f.Row, fieldTitle)},
		})
		hm := metashow.Row(ctx, o.c.Env, f.Row) // MQ13: metadata on every read
		out.Data[len(out.Data)-1].Meta, out.Data[len(out.Data)-1].MetaMasked = hm.Meta, hm.MetaMasked
	}
	out.Count = len(out.Data)
	return out, nil
}

// recordOf is the v0.1.17 --json form of a pattern row.
func recordOf(lab content.Labeler, src store.Source, r row.Row) Record {
	return Record{
		ID: r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		TrustScore: content.Number(r, content.FieldTrust), Confidence: content.Number(r, content.FieldConfidence),
		LastValidatedAt: content.Time(r, content.FieldValidated), ArchivedAt: content.Time(r, content.FieldArchivedAt),
		SourceKind: content.Field(r, content.FieldSourceKind), SourceRef: content.Field(r, content.FieldSourceRef),
		ProjectID: content.Field(r, content.FieldProject), RepoID: content.Field(r, content.FieldRepo),
		Title: content.Field(r, fieldTitle), Body: content.Field(r, content.FieldBody), Language: content.Field(r, fieldLanguage),
		SupersededByID: content.Field(r, fieldSuperseded), ScopeID: content.Field(r, schema.FieldScope), CreatedByActorID: content.CreatedBy(r),
		TagIDs: content.TagIDs(r), Store: content.SourceOf(lab, src), Label: content.Label(lab, src),
	}
}
