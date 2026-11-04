package memory

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names. The CLI path and REST route derive from them
// (op/route.go): memory.add → `lore memory add`, POST /v1/memories.
const (
	OpAdd        = "memory.add"
	OpList       = "memory.list"
	OpShow       = "memory.show"
	OpEdit       = "memory.edit"
	OpDelete     = "memory.delete"
	OpSearch     = "memory.search"
	OpArchive    = "memory.archive"
	OpUnarchive  = "memory.unarchive"
	OpInvalidate = "memory.invalidate"
)

// Memory field names (schema.Builtin's memories table).
const (
	fieldProject    = "project_id"
	fieldRepo       = "repo_id"
	fieldKind       = "kind"
	fieldBody       = "body"
	fieldSourceKind = "source_kind"
	fieldSourceRef  = "source_ref"
	fieldArchivedAt = "archived_at"
	fieldValidUntil = "valid_until"
	fieldSuperseded = "superseded_by_id"
	fieldTxAt       = "tx_at"
)

// Defaults of memory.add (v0.1.17's flag defaults).
const (
	DefaultKind   = schema.MemoryKindRetrieved
	DefaultSource = "manual"
	// DefaultSearchLimit is memory.search's --limit default (v0.1.17).
	DefaultSearchLimit = 10
	// maxSearchLimit bounds one search so a REST caller cannot ask the
	// cache for an unbounded result.
	maxSearchLimit = 1000
	// allowSecretsField is AddIn/EditIn's json name for --allow-secrets,
	// the Confirm.When trigger.
	allowSecretsField = "allow_secrets"
)

// table is the memories table.
const table row.Table = schema.TableMemories

// writeErrors are the codes any memory write can return (engine write
// path: validation, reference check, secret scan, policy, backend).
var writeErrors = []errs.Code{
	errs.InvalidInput, errs.InvalidID, errs.EmptyBody, errs.NotFound, errs.AmbiguousID,
	errs.SecretDetected, errs.NeedsConfirm, errs.Cancelled, errs.PolicyDenied, errs.ConfigInvalid,
	errs.Stale, errs.StoreReadOnly, errs.StoreLocked, errs.StoreUnreachable, errs.OldLore, errs.Internal,
}

// readErrors are the codes a memory read can return.
var readErrors = []errs.Code{errs.InvalidID, errs.NotFound, errs.AmbiguousID, errs.StoreUnreachable, errs.OldLore, errs.Internal}

// Ops returns every memory operation bound to env, for op.NewRegistry.
func Ops(env Env) []op.Entry {
	o := ops{env: env}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Add a memory (free-form learned knowledge)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
			Confirm: op.ConfirmRule{When: allowSecretsField},
			Examples: []op.Example{
				{Title: "add a memory", CLI: `lore memory add "Retry the gateway twice on 502"`, Want: "id: mem_"},
				{Title: "v0.1.17 spelling, to another store", CLI: `lore memory add --body="We use UTC for all timestamps" --to team_kb`, Want: "store: team_kb"},
			},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List memories of every attached store (active by default)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors,
			Examples: []op.Example{{Title: "list as JSON", CLI: "lore memory list --json", Want: `"data"`}},
		}, o.list),
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show one memory",
			Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors,
			Examples: []op.Example{{Title: "read the body", CLI: "lore memory show mem_<id> --json | jq -r '.data.body'", Want: ""}},
		}, o.show),
		op.Define(op.Spec{
			Name: OpEdit, Summary: "Edit memory fields (only the flags given change)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
			Confirm:  op.ConfirmRule{When: allowSecretsField},
			Examples: []op.Example{{Title: "change the body", CLI: `lore memory edit mem_<id> --body "Retry three times"`, Want: "id: mem_"}},
		}, o.edit),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a memory (a tombstone is kept so old copies never return)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(slices.Clone(writeErrors), errs.HasDependents),
			Confirm:  op.ConfirmRule{Always: true},
			Examples: []op.Example{{Title: "delete without a terminal", CLI: "lore memory delete mem_<id> --yes", Want: "id: mem_"}},
		}, o.delete),
		op.Define(op.Spec{
			Name: OpSearch, Summary: "Full-text search over memories of every attached store",
			Permission: op.PermRead, Access: op.AccessRead,
			Errors:   []errs.Code{errs.InvalidInput, errs.Unsupported, errs.DBCorrupt, errs.StoreUnreachable, errs.Internal},
			Examples: []op.Example{{Title: "exact phrase", CLI: `lore memory search '"connection pool"' --json`, Want: `"results"`}},
		}, o.search),
		op.Define(op.Spec{
			Name: OpArchive, Summary: "Archive a memory (hidden from list and search; kept)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
		}, o.archive),
		op.Define(op.Spec{
			Name: OpUnarchive, Summary: "Bring an archived memory back",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
		}, o.unarchive),
		op.Define(op.Spec{
			Name: OpInvalidate, Summary: "Mark a memory invalid as of now (sets valid_until)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
		}, o.invalidate),
	}
}

// ops binds the run functions to one Env.
type ops struct{ env Env }

// add creates one memory in the target store (--to, else the default).
func (o ops) add(ctx context.Context, in AddIn) (WriteOut, error) {
	body, err := oneBody(in.Body, in.Text)
	if err != nil {
		return WriteOut{}, err
	}
	project, err := o.env.Project(ctx)
	if err != nil {
		return WriteOut{}, err
	}
	fields, err := texts(map[string]string{
		fieldBody:       body,
		fieldKind:       orDefault(in.Kind, DefaultKind),
		fieldSourceKind: orDefault(in.Source, DefaultSource),
		fieldSourceRef:  in.SourceRef,
	})
	if err != nil {
		return WriteOut{}, err
	}
	if fields[fieldProject], err = value.RefOf(project.Any()); err != nil {
		return WriteOut{}, err
	}
	if in.Repo != "" {
		repo, rerr := o.env.RepoID(ctx, in.Repo)
		if rerr != nil {
			return WriteOut{}, rerr
		}
		if fields[fieldRepo], err = value.RefOf(repo.Any()); err != nil {
			return WriteOut{}, err
		}
	}
	// tx_at is v0.1.17's transaction time; renderers sort by it.
	fields[fieldTxAt] = value.DateTimeFrom(time.Now())
	w, err := o.env.Create(ctx, store.Source(in.To), table, fields)
	if err != nil {
		return WriteOut{}, err
	}
	return writeOut(o.storeOf(ctx, w.Row.ID, store.Source(in.To)), w), nil
}

// list returns the live memories of every attached store (or one).
func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	sc, repo, err := o.readScope(ctx, in.Repo, in.NoInherit, in.MasterOnly, in.AllRepos)
	if err != nil {
		return ListOut{}, err
	}
	sources := o.env.Stores()
	if in.Store != "" {
		sources = []store.Source{store.Source(in.Store)}
	}
	out := ListOut{Data: []Item{}}
	for _, src := range sources {
		rows, err := o.env.List(ctx, src, table)
		if err != nil {
			return ListOut{}, err
		}
		for _, r := range rows {
			it := itemOf(src, r)
			if (it.Archived && !in.Archived) || (in.Kind != "" && it.Kind != in.Kind) || !sc.keeps(repoOf(r), repo) {
				continue
			}
			out.Data = append(out.Data, it)
		}
	}
	out.Count = len(out.Data)
	return out, nil
}

// show returns one memory, from whichever attached store holds it.
func (o ops) show(ctx context.Context, in IDIn) (ShowOut, error) {
	id, err := memoryID(in.ID)
	if err != nil {
		return ShowOut{}, err
	}
	src, r, err := o.env.Get(ctx, engine.Ref{Table: table, ID: id.Any()})
	if err != nil {
		return ShowOut{}, err
	}
	if r.Deleted {
		return ShowOut{}, errs.New(errs.NotFound, "memory "+id.String()+" is deleted")
	}
	return ShowOut{Data: memoryOf(src, r)}, nil
}

// edit patches the fields whose flags were given.
func (o ops) edit(ctx context.Context, in EditIn) (WriteOut, error) {
	id, err := memoryID(in.ID)
	if err != nil {
		return WriteOut{}, err
	}
	patch, err := texts(map[string]string{
		fieldBody: in.Body, fieldKind: in.Kind, fieldSourceKind: in.Source, fieldSourceRef: in.SourceRef,
	})
	if err != nil {
		return WriteOut{}, err
	}
	if len(patch) == 0 {
		return WriteOut{}, errs.New(errs.InvalidInput, "memory edit: nothing to change").
			WithHint("give at least one of --body, --kind, --source, --source-ref")
	}
	return o.patch(ctx, id, patch)
}

// delete writes the memory's tombstone.
func (o ops) delete(ctx context.Context, in DeleteIn) (WriteOut, error) {
	id, err := memoryID(in.ID)
	if err != nil {
		return WriteOut{}, err
	}
	w, err := o.env.Delete(ctx, engine.Ref{Table: table, ID: id.Any()})
	if err != nil {
		return WriteOut{}, err
	}
	return writeOut(o.storeOf(ctx, id.Any(), ""), w), nil
}

// search matches the query against every attached store's cache and
// returns the live hits, best first per store.
func (o ops) search(ctx context.Context, in SearchIn) (SearchOut, error) {
	if strings.TrimSpace(in.Query) == "" {
		return SearchOut{}, errs.New(errs.InvalidInput, "memory search: empty query")
	}
	limit := in.Limit
	switch {
	case limit == 0:
		limit = DefaultSearchLimit
	case limit < 0 || limit > maxSearchLimit:
		return SearchOut{}, errs.New(errs.InvalidInput, "memory search: --limit must be between 1 and 1000")
	}
	sc, repo, err := o.readScope(ctx, in.Repo, in.NoInherit, in.MasterOnly, in.AllRepos)
	if err != nil {
		return SearchOut{}, err
	}
	hits, err := o.env.Search(ctx, table, in.Query, limit)
	if err != nil {
		return SearchOut{}, err
	}
	out := SearchOut{Results: []Item{}}
	for _, h := range hits {
		src, r, err := o.env.Get(ctx, engine.Ref{Store: h.Source, Table: table, ID: h.ID})
		if errs.Has(err, errs.NotFound) {
			continue // deleted since the cache was refreshed
		}
		if err != nil {
			return SearchOut{}, err
		}
		it := itemOf(src, r)
		if r.Deleted || (it.Archived && !in.IncludeArchived) || !sc.keeps(repoOf(r), repo) {
			continue
		}
		out.Results = append(out.Results, it)
	}
	out.Count = len(out.Results)
	return out, nil
}

// archive sets archived_at to now.
func (o ops) archive(ctx context.Context, in IDIn) (WriteOut, error) {
	return o.stamp(ctx, in.ID, fieldArchivedAt, value.DateTimeFrom(time.Now()))
}

// unarchive clears archived_at.
func (o ops) unarchive(ctx context.Context, in IDIn) (WriteOut, error) {
	return o.stamp(ctx, in.ID, fieldArchivedAt, value.Null{})
}

// invalidate sets valid_until to now (v0.1.17 bitemporal invalidation).
func (o ops) invalidate(ctx context.Context, in IDIn) (WriteOut, error) {
	return o.stamp(ctx, in.ID, fieldValidUntil, value.DateTimeFrom(time.Now()))
}

// stamp sets one field of the memory whose id is raw.
func (o ops) stamp(ctx context.Context, raw, field string, v value.Value) (WriteOut, error) {
	id, err := memoryID(raw)
	if err != nil {
		return WriteOut{}, err
	}
	return o.patch(ctx, id, map[string]value.Value{field: v})
}

// patch updates one memory through the engine.
func (o ops) patch(ctx context.Context, id ids.ID[ids.Memory], patch map[string]value.Value) (WriteOut, error) {
	src, _, err := o.env.Get(ctx, engine.Ref{Table: table, ID: id.Any()})
	if err != nil {
		return WriteOut{}, err
	}
	w, err := o.env.Update(ctx, engine.Ref{Store: src, Table: table, ID: id.Any()}, patch)
	if err != nil {
		return WriteOut{}, err
	}
	return writeOut(src, w), nil
}

// storeOf names the store that holds id after a write: the requested one,
// else the one Get finds (the default target).
func (o ops) storeOf(ctx context.Context, id ids.AnyID, requested store.Source) store.Source {
	if requested != "" {
		return requested
	}
	src, _, err := o.env.Get(ctx, engine.Ref{Table: table, ID: id})
	if err != nil {
		return ""
	}
	return src
}
