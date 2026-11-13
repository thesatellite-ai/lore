package memory

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/creator"
	"github.com/thesatellite-ai/lore/ops/internal/wheredel"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/expand"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"github.com/thesatellite-ai/lore/ops/internal/metashow"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/tagref"
	"github.com/thesatellite-ai/lore/ops/internal/textcut"
	"github.com/thesatellite-ai/lore/ops/internal/trashline"
	opsscope "github.com/thesatellite-ai/lore/ops/scope"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names. The CLI path and REST route derive from them
// (op/route.go): memory.add → `lore memory add`, POST /v1/memories.
const (
	// Noun is the CLI noun of every memory operation (`lore memory …`).
	Noun         = "memory"
	OpAdd        = "memory.add"
	OpList       = "memory.list"
	OpShow       = "memory.show"
	OpEdit       = "memory.edit"
	OpDelete     = "memory.delete"
	OpSearch     = "memory.search"
	OpArchive    = "memory.archive"
	OpUnarchive  = "memory.unarchive"
	OpInvalidate = "memory.invalidate"
	OpMove       = "memory.move"
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
	fieldTrust      = "trust_score"
	fieldConfidence = "confidence"
	fieldValidAt    = "valid_at"
	fieldValidated  = "last_validated_at"
	fieldCreatedBy  = "created_by_actor_id"
	fieldValidBy    = "validated_by_actor_id"
	fieldName       = "name"
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
	// DefaultTrust is v0.1.17's trust_score default (dbent mixin_aicoder).
	DefaultTrust = 0.5
	// searchFetch is how many hits search asks the caches for before its
	// scope filter; --limit is applied after the filter.
	searchFetch = maxSearchLimit
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
			Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{
				{Title: "add a memory", CLI: `lore memory add --body "Retry the gateway twice on 502"`, Want: "id: mem_"},
				{Title: "to another store", CLI: `lore memory add --body="We use UTC for all timestamps" --to team_kb`, Want: "store: team_kb"},
			},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List memories of every attached store (active by default)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors,
			Examples: []op.Example{{Title: "list as JSON", CLI: "lore memory list --json", Want: `"data"`}},
		}, o.list),
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show one memory",
			Permission: op.PermRead, Access: op.AccessRead, Errors: expand.RevealErrors(readErrors), Confirm: expand.RevealConfirm,
			Examples: []op.Example{{Title: "read the body", CLI: "lore memory show --id mem_<id> --json | jq -r '.data.body'", Want: ""}},
		}, o.showExpand),
		op.Define(op.Spec{
			Name: OpEdit, Summary: "Edit memory fields (only the flags given change)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
			Confirm:  op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "change the body", CLI: `lore memory edit --id mem_<id> --body "Retry three times"`, Want: "id: mem_"}},
		}, o.edit),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a memory (it goes to the trash; lore trash restore brings it back)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(slices.Clone(writeErrors), errs.HasDependents),
			Confirm:  op.ConfirmRule{MayAsk: true}, // asked in delete: --where --dry-run never asks (BUGFIX-26; FA-LTASK-2 kept Always, which asked before --dry-run could list)
			Examples: []op.Example{{Title: "delete without a terminal", CLI: "lore memory delete --id mem_<id> --yes", Want: "id: mem_"}},
		}, o.delete),
		op.Define(op.Spec{
			Name: OpSearch, Summary: "Full-text search over memories of every attached store",
			Permission: op.PermRead, Access: op.AccessRead,
			Errors:   []errs.Code{errs.InvalidInput, errs.NotFound, errs.Unsupported, errs.DBCorrupt, errs.StoreUnreachable, errs.Internal},
			Examples: []op.Example{{Title: "exact phrase", CLI: `lore memory search --query '"connection pool"' --json`, Want: `"results"`}},
		}, o.search),
		op.Define(op.Spec{
			Name: OpArchive, Summary: "Archive a memory (hidden from list and search; kept)",
			Examples:   []op.Example{{Title: "archive one", CLI: "lore memory archive --id mem_<id>", Want: "archived"}},
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
		}, o.archive),
		op.Define(op.Spec{
			Name: OpUnarchive, Summary: "Bring an archived memory back",
			Examples:   []op.Example{{Title: "bring it back", CLI: "lore memory unarchive --id mem_<id>", Want: "unarchived"}},
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
		}, o.unarchive),
		op.Define(op.Spec{
			Name: OpMove, Summary: "Move a memory to another attached store (same id; the old copy is tombstoned)",
			Examples:   []op.Example{{Title: "move it to another attached store", CLI: "lore memory move --id mem_<id> --to team_kb", Want: "mem_"}},
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: append(slices.Clone(writeErrors), errs.Unsupported),
			Confirm: op.ConfirmRule{MayAsk: true},
		}, o.move),
		op.Define(op.Spec{
			Name: OpInvalidate, Summary: "Mark a memory invalid as of now (sets valid_until)",
			Examples:   []op.Example{{Title: "it stopped being true", CLI: "lore memory invalidate --id mem_<id>", Want: "invalidated"}},
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
		}, o.invalidate),
	}
}

// ops binds the run functions to one Env.
type ops struct{ env Env }

// add creates one memory in the target store (--to, else the default).
// With --supersedes the old memory is checked first (live, same project,
// no successor yet: nothing is written otherwise) and pointed at the new
// one after; if that fails the new memory is deleted again, so a half
// chain is never left (v0.1.17 supersede.go).
//
// With --key (CLEANUP Qs) the id is natural over (project, key): a memory
// already stored under it is returned as is (Existing) and nothing is
// written; else the row is created with that id and the key stored.
func (o ops) add(ctx context.Context, in AddIn) (WriteOut, error) {
	body, err := oneBody(in.Body, in.Text)
	if err != nil {
		return WriteOut{}, err
	}
	project, err := o.project(ctx, in.Project)
	if err != nil {
		return WriteOut{}, err
	}
	keyID, err := addKeyID(project, in.Key)
	if err != nil {
		return WriteOut{}, err
	}
	if !keyID.IsZero() {
		if out, found, ferr := o.existingByKey(ctx, keyID, in.Repo); found || ferr != nil {
			return out, ferr
		}
	}
	if err := checkKind(in.Kind); err != nil {
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
	// 6.2.3 / CQ12: --scope picks the store, repo and folder.
	st, err := opsscope.ForWrite(ctx, o.env, in.Scope, in.To, in.Repo, false)
	if err != nil {
		return WriteOut{}, err
	}
	if st.To != "" {
		in.To = st.To
	}
	if st.Repo != "" {
		in.Repo = st.Repo
	}
	if st.ScopeID != "" {
		if fields[schema.FieldScope], err = scopeRef(st.ScopeID); err != nil {
			return WriteOut{}, err
		}
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
	for f, raw := range map[string]string{fieldValidBy: in.ValidatedBy} {
		if raw == "" {
			continue
		}
		a, aerr := ids.Parse[ids.Actor](strings.TrimSpace(raw))
		if aerr != nil {
			return WriteOut{}, lookup.BadID("memory add: "+quoteID(raw)+" is not an actor id (act_…)", aerr)
		}
		if fields[f], err = value.RefOf(a.Any()); err != nil {
			return WriteOut{}, err
		}
	}
	if v := creator.Ref(ctx, o.env); v != nil {
		fields[fieldCreatedBy] = v // the calling actor, never caller-set (INTEG-AUDIT-1)
	}
	if len(in.Tags) > 0 {
		tags, terr := o.tagRefs(ctx, project.String(), in.Tags)
		if terr != nil {
			return WriteOut{}, terr
		}
		if fields[schema.FieldTagIDs], err = tagList(tags); err != nil {
			return WriteOut{}, err
		}
	}
	trust, err := value.Float(DefaultTrust)
	if err != nil {
		return WriteOut{}, errs.Wrap(errs.Internal, "memory add: trust score", err)
	}
	fields[fieldTrust] = trust
	// tx_at is v0.1.17's transaction time; renderers sort by it.
	fields[fieldTxAt] = value.DateTimeFrom(opclock.Now(o.env))
	var old ids.ID[ids.Memory]
	var oldSrc store.Source
	var carry metalist.Carry // 3.19: --supersedes carries the metadata over unless --no-meta
	if in.Supersedes != "" {
		var oldRow row.Row
		if old, oldSrc, oldRow, err = o.checkSupersedes(ctx, project, in.Supersedes); err != nil {
			return WriteOut{}, err
		}
		if carry, err = metalist.PlanCarry(oldRow, table, in.MetaFlags, in.NoMeta); err != nil {
			return WriteOut{}, err
		}
	}
	var w engine.Written
	if keyID.IsZero() {
		w, err = o.env.Create(carry.Before(ctx), store.Source(in.To), table, fields)
	} else {
		if fields[schema.FieldIdempotencyKey], err = value.TextOf(strings.TrimSpace(in.Key)); err != nil {
			return WriteOut{}, err
		}
		w, err = o.createKeyed(carry.Before(ctx), store.Source(in.To), keyID, fields)
		if errors.Is(err, store.ErrStale) {
			// another run with the same key stored it first (a race)
			if out, found, ferr := o.existingByKey(ctx, keyID, in.Repo); found || ferr != nil {
				return out, ferr
			}
		}
	}
	if err != nil {
		return WriteOut{}, carry.Hint(err)
	}
	src := o.storeOf(ctx, w.Row.ID, store.Source(in.To))
	if !old.IsZero() {
		newRef := engine.Ref{Store: src, Table: table, ID: w.Row.ID}
		if err := carry.After(ctx, o.env, newRef, w.Row); err != nil {
			if _, derr := o.env.Delete(ctx, newRef); derr != nil {
				return WriteOut{}, errs.Wrap(errs.CodeOf(err), "memory add: could not carry the metadata of "+old.String()+"; the new memory "+w.Row.ID.String()+" is still there", carry.Hint(err))
			}
			return WriteOut{}, carry.Hint(err)
		}
		if err := o.finishSupersedes(ctx, oldSrc, old, src, w.Row.ID); err != nil {
			return WriteOut{}, err
		}
	}
	out := writeOut(src, w)
	out.CarriedMeta = carry.Count
	scope := badgeMaster
	if in.Repo != "" {
		scope = "repo:" + in.Repo
	}
	// v0.1.17: "✓ <id> <label>" ("" in the repo, keeping its space), then
	// the scope line.
	out.Human = strings.TrimSpace(mark+" "+out.ID+" "+Label(LabelerOf(o.env), src)) + "\n  scope: [" + scope + "]\n" + carry.Line()
	return out, nil
}

// maxAddKey bounds an idempotency key (it is hashed into the id and
// stored on the row; real keys are a uuid or a short tag).
const maxAddKey = 200

// addKeyPart separates the natural-id input of a keyed add from every
// other natural id (CLEANUP Qs).
const addKeyPart = "memory.add.key"

// addKeyID is the natural id of a keyed add: the zero id without --key.
// Scope: the project (the same key in another project is another memory).
func addKeyID(project ids.ID[ids.Project], raw string) (ids.AnyID, error) {
	if raw == "" {
		return ids.AnyID{}, nil
	}
	key := strings.TrimSpace(raw)
	if key == "" || len(key) > maxAddKey || strings.ContainsAny(key, "\n\r") {
		return ids.AnyID{}, errs.New(errs.InvalidInput, "memory add: --key must be one line of 1 to "+strconv.Itoa(maxAddKey)+" characters")
	}
	return engine.NaturalID(ids.PrefixMemory, addKeyPart, project.String(), key)
}

// existingByKey returns the memory a previous keyed add stored (found),
// with nothing written. A copy in the trash is E_IN_TRASH from Get, so a
// trashed memory is never silently re-added under its key.
func (o ops) existingByKey(ctx context.Context, id ids.AnyID, repo string) (WriteOut, bool, error) {
	src, r, err := o.env.Get(ctx, engine.Ref{Table: table, ID: id})
	if errs.Has(err, errs.NotFound) {
		return WriteOut{}, false, nil
	}
	if err != nil {
		return WriteOut{}, false, err
	}
	if r.Deleted {
		return WriteOut{}, false, errs.New(errs.InvalidInput, "memory add: the memory stored with this --key ("+id.String()+") was deleted").
			WithHint("use another --key to add it again")
	}
	out := WriteOut{ID: id.String(), Store: string(src), Existing: true}
	scope := badgeMaster
	if repo != "" {
		scope = "repo:" + repo
	}
	out.Human = strings.TrimSpace(mark+" "+out.ID+" "+Label(LabelerOf(o.env), src)) + "\n  scope: [" + scope + "]\n  already added with this --key; nothing written\n"
	return out, true, nil
}

// createKeyed creates the memory with its key's natural id (IDCreator).
func (o ops) createKeyed(ctx context.Context, to store.Source, id ids.AnyID, fields map[string]value.Value) (engine.Written, error) {
	c, ok := o.env.(IDCreator)
	if !ok {
		return engine.Written{}, errs.New(errs.Unsupported, "memory add: --key is not supported here")
	}
	return c.CreateWithID(ctx, to, table, id, fields)
}

// checkSupersedes validates --supersedes before anything is written.
func (o ops) checkSupersedes(ctx context.Context, project ids.ID[ids.Project], raw string) (ids.ID[ids.Memory], store.Source, row.Row, error) {
	// B-ERRS-1 (owner BATCH2 #11, CLI_REFERENCE 5 names --supersedes): a
	// malformed value or another noun's prefix is E_INVALID_ID; a
	// well-formed id naming no live memory of this project is E_NOT_FOUND.
	notFound := errs.New(errs.NotFound, "--supersedes "+textcut.Cut(strings.TrimSpace(raw), maxQuotedID, maxQuotedID)+": no memory with that id in this project").
		WithHint("run `lore memory list` to see the ids")
	id, err := memoryID(raw)
	if err != nil {
		return ids.ID[ids.Memory]{}, "", row.Row{}, lookup.BadID("--supersedes "+textcut.Cut(strings.TrimSpace(raw), maxQuotedID, maxQuotedID)+": not a memory id (mem_…)", err)
	}
	src, r, err := o.env.Get(ctx, engine.Ref{Table: table, ID: id.Any()})
	if errs.Has(err, errs.NotFound) || (err == nil && (r.Deleted || field(r, fieldProject) != project.String())) {
		return ids.ID[ids.Memory]{}, "", row.Row{}, notFound
	}
	if err != nil {
		return ids.ID[ids.Memory]{}, "", row.Row{}, err
	}
	if next := field(r, fieldSuperseded); next != "" {
		return ids.ID[ids.Memory]{}, "", row.Row{}, errs.New(errs.InvalidInput, "--supersedes "+id.String()+": that memory was already replaced by "+next).
			WithHint("supersede " + next + " instead, so the chain stays one line")
	}
	return id, src, r, nil
}

// finishSupersedes points old at the new memory; on failure the new memory
// is deleted again and the marking error returned.
func (o ops) finishSupersedes(ctx context.Context, oldSrc store.Source, old ids.ID[ids.Memory], newSrc store.Source, newID ids.AnyID) error {
	ref, err := value.RefOf(newID)
	if err != nil {
		return err
	}
	_, err = o.env.Update(ctx, engine.Ref{Store: oldSrc, Table: table, ID: old.Any()}, map[string]value.Value{fieldSuperseded: ref})
	if err == nil {
		return nil
	}
	// the rolled-back row is purged, not trashed: "nothing was kept" (later 498)
	if _, derr := o.env.Delete(engine.WithPurge(ctx, &engine.PurgeRequest{Asked: true, Confirmed: true}), engine.Ref{Store: newSrc, Table: table, ID: newID}); derr != nil {
		return errs.Prefix("memory add: could not mark "+old.String()+" superseded by "+newID.String(), err).
			WithHint("the new memory " + newID.String() + " was written but could not be removed again; delete it with `lore memory delete --id " + newID.String() + "`")
	}
	return errs.Prefix("memory add: could not mark "+old.String()+" superseded by "+newID.String()+"; nothing was kept", err)
}

// project resolves --project (a prj_ id or a project name among the repo
// store's projects); "" is the repo's one project (Env.Project).
func (o ops) project(ctx context.Context, flag string) (ids.ID[ids.Project], error) {
	if flag == "" {
		return o.env.Project(ctx)
	}
	rows, err := o.env.List(ctx, store.SourceRepo, schema.TableProjects)
	if err != nil {
		return ids.ID[ids.Project]{}, err
	}
	var hits []row.Row
	for _, r := range rows {
		if r.ID.String() == flag || field(r, fieldName) == flag {
			hits = append(hits, r)
		}
	}
	switch len(hits) {
	case 0:
		return ids.ID[ids.Project]{}, errs.New(errs.NotFound, "memory: no project "+quoteID(flag)).
			WithHint("run `lore project list` to see the projects")
	case 1:
		return ids.As[ids.Project](hits[0].ID)
	}
	return ids.ID[ids.Project]{}, errs.New(errs.ProjectNameCollision, "memory: several projects are named "+quoteID(flag)).
		WithHint("name it by its prj_ id")
}

// projectFilter is the project a read keeps ("" = every project: without
// --project the repo store holds one project and other stores' rows belong
// to their own folders' projects, as v0.1.17 appended them unfiltered).
func (o ops) projectFilter(ctx context.Context, flag string) (string, error) {
	if flag == "" {
		return "", nil
	}
	p, err := o.project(ctx, flag)
	if err != nil {
		return "", err
	}
	return p.String(), nil
}

// list returns the live memories of every attached store (or one).
func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	ctx = in.Deleted(ctx, table) // DELETION 3.2: --include-deleted / --deleted-only
	if in.All {
		in.AllRepos, in.Archived = true, true // --all: every repo's memories, the archived ones too
	}
	sc, repo, err := o.readScope(ctx, in.Repo, in.NoInherit, in.MasterOnly, in.AllRepos)
	if err != nil {
		return ListOut{}, err
	}
	project, err := o.projectFilter(ctx, in.Project)
	if err != nil {
		return ListOut{}, err
	}
	sources := engine.ReadStoresOf(o.env)
	if in.Store != "" {
		sources = engine.NamedSources(o.env, store.Source(in.Store))
	}
	out := ListOut{SchemaVersion: SchemaVersion, Kind: KindList, Data: []Record{}}
	var idx tagref.Index
	if len(in.Tags) > 0 {
		if idx, err = tagref.Load(ctx, o.env); err != nil {
			return ListOut{}, err
		}
	}
	var kept []row.Row
	var keptSrc []store.Source
	var hid metalist.Hidden
	dirs := metalist.NewDirs(o.env)
	for _, src := range sources {
		rows, lerr := o.env.List(ctx, src, table)
		if lerr != nil {
			return ListOut{}, lerr
		}
		for _, r := range rows {
			if (in.Kind != "" && field(r, fieldKind) != in.Kind) || (project != "" && field(r, fieldProject) != project) || !hasTags(idx, r, in.Tags) {
				continue
			}
			// what the scope and the archive filter leave out is counted
			// for the footer (metalist.Hidden)
			if !sc.keeps(repoOf(r), repo) {
				hid.OutOfScope(dirs.Of(ctx, src, r))
				continue
			}
			if field(r, fieldArchivedAt) != "" && !in.Archived {
				hid.Archived++
				continue
			}
			kept, keptSrc = append(kept, r), append(keptSrc, src)
		}
	}
	sel, err := metalist.Select(ctx, o.env, in.Flags, kept)
	if err != nil {
		return ListOut{}, err
	}
	listed := make([]row.Row, 0, len(sel.Order))
	for _, i := range sel.Order {
		out.Data = append(out.Data, withMeta(ctx, o.env, recordOf(LabelerOf(o.env), keptSrc[i], kept[i]), kept[i]))
		listed = append(listed, kept[i])
	}
	out.Notes = hid.Notes(sel.Notes)
	out.Notes = append(out.Notes, in.TrashNotes(listed)...)
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
	if r.Deleted { // DELETION 3.2: E_IN_TRASH for a trashed memory
		return ShowOut{}, lookup.Gone(ctx, o.env, engine.Ref{Store: src, Table: table, ID: id.Any()}, errs.New(errs.NotFound, "memory "+id.String()+" is deleted"))
	}
	return ShowOut{SchemaVersion: SchemaVersion, Kind: KindShow, Data: withMeta(ctx, o.env, recordOf(LabelerOf(o.env), src, r), r)}, nil
}

// edit patches the fields whose flags were given.
func (o ops) edit(ctx context.Context, in EditIn) (WriteOut, error) {
	id, err := memoryID(in.ID)
	if err != nil {
		return WriteOut{}, err
	}
	if err := checkKind(in.Kind); err != nil {
		return WriteOut{}, err
	}
	patch, err := texts(map[string]string{
		fieldBody: in.Body, fieldKind: in.Kind, fieldSourceKind: in.Source, fieldSourceRef: in.SourceRef,
	})
	if err != nil {
		return WriteOut{}, err
	}
	st, err := opsscope.ForWrite(ctx, o.env, in.Scope, "", "", true)
	if err != nil {
		return WriteOut{}, err
	}
	if st.Given() {
		if in.RebindRepo != "" || in.RebindMaster {
			return WriteOut{}, errs.New(errs.InvalidInput, "memory edit: give --scope or --rebind-repo/--rebind-master, not both")
		}
		in.RebindRepo, in.RebindMaster = st.Repo, st.Master
		if st.ClearScope {
			patch[schema.FieldScope] = value.Null{}
		}
		if st.ScopeID != "" {
			if patch[schema.FieldScope], err = scopeRef(st.ScopeID); err != nil {
				return WriteOut{}, err
			}
		}
	}
	switch {
	case in.RebindRepo != "" && in.RebindMaster:
		return WriteOut{}, errs.New(errs.InvalidInput, "memory edit: give --rebind-repo or --rebind-master, not both")
	case in.RebindRepo != "":
		repo, rerr := o.env.RepoID(ctx, in.RebindRepo)
		if rerr != nil {
			return WriteOut{}, rerr
		}
		if patch[fieldRepo], err = value.RefOf(repo.Any()); err != nil {
			return WriteOut{}, err
		}
	case in.RebindMaster:
		patch[fieldRepo] = value.Null{}
	}
	if len(in.Tags) > 0 || len(in.Untags) > 0 {
		v, terr := o.retagPatch(ctx, id, in.Tags, in.Untags)
		if terr != nil {
			return WriteOut{}, terr
		}
		patch[schema.FieldTagIDs] = v
	}
	if len(patch) == 0 && !engine.MetaBound(ctx) {
		return WriteOut{}, errs.New(errs.InvalidInput, "memory edit: nothing to change").
			WithHint("give at least one of --body, --kind, --source, --source-ref, --scope, --rebind-repo, --rebind-master, --tag, --untag")
	}
	return withLine(o.patch(ctx, id, patch))("updated")
}

// retagPatch is the new tag_ids of memory id: its tags plus add, minus
// drop. A tag to remove only has to be named (it may be deleted already).
func (o ops) retagPatch(ctx context.Context, id ids.ID[ids.Memory], add, drop []string) (value.Value, error) {
	_, r, err := o.env.Get(ctx, engine.Ref{Table: table, ID: id.Any()})
	if err != nil {
		return nil, err
	}
	project := field(r, fieldProject)
	addIDs, err := o.tagRefs(ctx, project, add)
	if err != nil {
		return nil, err
	}
	dropIDs := make([]ids.AnyID, 0, len(drop))
	for _, n := range drop {
		t, terr := tagref.Droppable(ctx, o.env, project, strings.TrimSpace(n))
		if terr != nil {
			return nil, terr
		}
		dropIDs = append(dropIDs, t...)
	}
	return tagList(retag(r, addIDs, dropIDs))
}

// delete writes the memory's tombstone.
func (o ops) delete(ctx context.Context, in DeleteIn) (WriteOut, error) {
	ctx = engine.WithDeleteReason(ctx, in.Reason) // DELETION 2.1 --reason; E_REASON_REQUIRED under delete.require_reason
	bulkForm, err := wheredel.Form("memory", in.ID, in.Where, in.DryRun)
	if err != nil {
		return WriteOut{}, err
	}
	if bulkForm {
		res, werr := wheredel.Run(ctx, o.env, wheredel.Spec{Noun: "memory", Table: table}, in.Where, in.DryRun)
		if werr != nil {
			return WriteOut{}, werr
		}
		return WriteOut{ID: res.Batch, Store: res.Store, Rows: res.Rows, Human: res.Human}, nil
	}
	id, err := memoryID(in.ID)
	if err != nil {
		return WriteOut{}, err
	}
	// The one-row form asks as the engine did under Confirm Always, and
	// only about a live row (G-ENGINE-4): a missing id answers its lookup
	// error first, and a trashed one is left to Delete's E_IN_TRASH.
	_, cur, err := o.env.Get(ctx, engine.Ref{Table: table, ID: id.Any()})
	if err != nil {
		return WriteOut{}, err
	}
	if !cur.Deleted {
		if err = bulk.Ask(ctx, "memory delete "+id.String()+"?"); err != nil {
			return WriteOut{}, err
		}
	}
	src := o.storeOf(ctx, id.Any(), "")
	w, err := o.env.Delete(ctx, engine.Ref{Table: table, ID: id.Any()})
	if err != nil {
		return WriteOut{}, err
	}
	out := writeOut(src, w)
	out.Human = trashline.Moved(out.ID, out.ID) // DQ8: every delete is soft
	return out, nil
}

// move moves the memory to another attached store, keeping its id.
func (o ops) move(ctx context.Context, in MoveIn) (WriteOut, error) {
	id, err := memoryID(in.ID)
	if err != nil {
		return WriteOut{}, err
	}
	mv, ok := o.env.(Mover)
	if !ok {
		return WriteOut{}, errs.New(errs.Unsupported, "memory move: this build's store wiring cannot move rows yet")
	}
	// Moving to the store that already holds it: say so, instead of the
	// engine's "no row … outside <store>" with a landed-on-main hint, which
	// sent the person looking on another branch (campaign knowledge
	// 2026-11-08).
	// A trashed row falls through: the move answers E_IN_TRASH for it.
	if src, r, gerr := o.env.Get(ctx, engine.Ref{Table: table, ID: id.Any()}); gerr == nil && !r.Deleted && src == store.Source(in.To) {
		return WriteOut{}, errs.New(errs.InvalidInput, "memory move: "+id.String()+" is already in store "+in.To).
			WithHint("name another attached store with --to (`lore store list` shows them)")
	}
	w, err := mv.Move(ctx, engine.Ref{Table: table, ID: id.Any()}, store.Source(in.To))
	if err != nil {
		return WriteOut{}, err
	}
	return withLine(writeOut(store.Source(in.To), w), nil)("moved to " + in.To)
}

// search matches the query against every attached store's cache, keeps the
// live hits in scope, best first, and cuts them at --limit AFTER the scope
// filter (v0.1.17 filtered in SQL before its LIMIT).
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
	project, err := o.projectFilter(ctx, in.Project)
	if err != nil {
		return SearchOut{}, err
	}
	hits, err := SearchFields(ctx, o.env, Noun, table, in.Query, in.Columns, in.Store, searchFetch)
	if err != nil {
		return SearchOut{}, err
	}
	// Data stays nil when nothing matched: v0.1.17 printed "data": null
	// (entitySearchEnvelope), and --json is byte-compatible with it.
	out := SearchOut{SchemaVersion: SchemaVersion, Kind: KindSearch, Query: in.Query}
	for _, h := range hits {
		if len(out.Data) == limit {
			break
		}
		src, r, err := o.env.Get(ctx, engine.Ref{Store: h.Source, Table: table, ID: h.ID})
		if errs.Has(err, errs.NotFound) {
			continue // deleted since the cache was refreshed
		}
		if err != nil {
			return SearchOut{}, err
		}
		archived := field(r, fieldArchivedAt) != ""
		if r.Deleted || (archived && !in.IncludeArchived) || !sc.keeps(repoOf(r), repo) ||
			(project != "" && field(r, fieldProject) != project) {
			continue
		}
		out.Data = append(out.Data, hitOf(LabelerOf(o.env), src, h, r))
		hm := metashow.Row(ctx, o.env, r) // MQ13: metadata on every read
		out.Data[len(out.Data)-1].Meta, out.Data[len(out.Data)-1].MetaMasked = hm.Meta, hm.MetaMasked
	}
	out.Count = len(out.Data)
	return out, nil
}

// archive sets archived_at to now.
func (o ops) archive(ctx context.Context, in IDIn) (WriteOut, error) {
	return withLine(o.stamp(ctx, in.ID, fieldArchivedAt, value.DateTimeFrom(opclock.Now(o.env))))("archived")
}

// unarchive clears archived_at.
func (o ops) unarchive(ctx context.Context, in IDIn) (WriteOut, error) {
	return withLine(o.stamp(ctx, in.ID, fieldArchivedAt, value.Null{}))("unarchived")
}

// invalidate sets valid_until to now (v0.1.17 bitemporal invalidation).
func (o ops) invalidate(ctx context.Context, in IDIn) (WriteOut, error) {
	id, err := memoryID(in.ID)
	if err != nil {
		return WriteOut{}, err
	}
	// Run twice: a memory already invalid keeps the moment it stopped being
	// true; a second invalidate used to move valid_until to the later
	// time, so the record of when it went stale drifted with every retry
	// (campaign knowledge 2026-11-08). A valid_until still in the future
	// (set by hand) is pulled in to now as before.
	if src, r, gerr := o.env.Get(ctx, engine.Ref{Table: table, ID: id.Any()}); gerr == nil {
		if until := timePtr(r, fieldValidUntil); !until.IsZero() && !until.After(opclock.Now(o.env)) {
			out := WriteOut{ID: id.String(), Store: string(src)}
			out.Human = mark + " " + out.ID + " already invalid since " + until.UTC().Format(time.RFC3339) + " (nothing written)\n"
			return out, nil
		}
	}
	return withLine(o.stamp(ctx, in.ID, fieldValidUntil, value.DateTimeFrom(opclock.Now(o.env))))("invalidated (valid_until=now)")
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

// mark is v0.1.17's success mark (style.Success("✓"), uncoloured here).
const mark = "✓"

// badgeMaster is v0.1.17's scope badge text for a project-wide memory.
const badgeMaster = "master"

// withLine sets out's human form to v0.1.17's "✓ <id> <verb>" line when
// the write succeeded; an error passes through untouched.
func withLine(out WriteOut, err error) func(verb string) (WriteOut, error) {
	return func(verb string) (WriteOut, error) {
		if err != nil {
			return out, err
		}
		out.Human = mark + " " + out.ID + " " + verb + "\n"
		return out, nil
	}
}

// scopeRef is a scp_ id as a ref value.
func scopeRef(id string) (value.Value, error) {
	sid, err := ids.ParseAny(id)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "memory: scope id", err)
	}
	return value.RefOf(sid)
}

// checkKind refuses a --kind outside schema.MemoryKinds as E_INVALID_INPUT
// naming the flag, the way rule --severity does (INTEG-KNOWLEDGE-5); the
// schema check behind it answered E_RESOURCE_INVALID with the field name.
// "" is "not given".
func checkKind(k string) error {
	if k == "" || slices.Contains(schema.MemoryKinds(), k) {
		return nil
	}
	return errs.New(errs.InvalidInput, "memory: --kind "+strconv.Quote(k)+" is not one of "+strings.Join(schema.MemoryKinds(), " | "))
}
