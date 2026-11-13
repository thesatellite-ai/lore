// Package link defines the commit-link operations (layer L5): `lore link
// add|list|remove` and `lore commit show` (v0.1.17 `lore commit-show`),
// with v0.1.17's flag spellings.
//
// A commit link anchors "this work shipped as <sha>" on any row through the
// polymorphic (entity_table, entity_id) pair. Its id is the natural id of
// (entity_table, entity_id, sha), the same bytes v0.1.17 wrote, so linking
// one commit twice is refused rather than duplicated.
package link

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/ops/internal/auditrec"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"github.com/thesatellite-ai/lore/ops/internal/pointer"
	"github.com/thesatellite-ai/lore/ops/internal/textcut"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (`lore link …`, REST /v1/links; `lore commit show`,
// GET /v1/commits/{id}).
const (
	Noun       = "link"
	CommitNoun = "commit"
	OpAdd      = "link.add"
	OpList     = "link.list"
	OpRemove   = "link.remove"
	OpShow     = "commit.show"
)

// Envelope values of v0.1.17's --json.
const (
	SchemaVersion = 1
	KindList      = "link.list"
	KindShow      = "commit.show"
)

// --commit values that mean "the current HEAD, with its message, author
// and time filled in" (v0.1.17).
const (
	commitHEAD = "HEAD"
	commitAuto = "auto"
)

// Display limits (v0.1.17): short sha width, message excerpt 60 (cut at
// 57 + "...").
const (
	shortSHA  = 8
	msgMax    = 60
	msgCut    = 57
	minSHAHex = 7
	maxSHAHex = 40
	maxQuoted = 48
)

// Link field names.
const (
	fieldProject     = "project_id"
	fieldSHA         = "sha"
	fieldMessage     = "message"
	fieldRepoPath    = "repo_path"
	fieldAuthor      = "author"
	fieldCommittedAt = "committed_at"
	fieldCreatedBy   = "created_by_actor_id"
)

// table is the commit_links table.
const table row.Table = schema.TableCommitLinks

// Env is what the link operations use; *wire.App implements it.
//
// Invariant: every write goes through Create or Delete (the engine's one
// write path).
type Env interface {
	Create(ctx context.Context, target store.Source, table row.Table, fields map[string]value.Value) (engine.Written, error)
	Delete(ctx context.Context, ref engine.Ref) (engine.Written, error)
	Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error)
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
	Stores() []store.Source
	// Project is the repo's own project (links are per project).
	Project(ctx context.Context) (ids.ID[ids.Project], error)
}

// CommitResolver reads git for --commit (adapters/gitcommit runs the git
// program). dir is --repo-path ("" = the working directory).
type CommitResolver interface {
	// RevParse returns the full sha of ref.
	RevParse(ctx context.Context, dir, ref string) (string, error)
	// Describe returns a commit's subject, author and commit time.
	Describe(ctx context.Context, dir, sha string) (subject, author, committedAt string, err error)
}

// AddIn is the input of link.add. Entity and Commit are required; they
// are checked by add so the error keeps v0.1.17's wording.
type AddIn struct {
	Entity      string `json:"entity,omitempty" flag:"entity" desc:"entity to link to (opaque id: tsk_…, mem_…, dec_…)"`
	Commit      string `json:"commit,omitempty" flag:"commit" desc:"git commit sha (or HEAD / auto for current HEAD)"`
	Message     string `json:"message,omitempty" flag:"message" desc:"commit message (auto-filled from git log when --commit=HEAD)"`
	RepoPath    string `json:"repo_path,omitempty" flag:"repo-path" desc:"path to git repo on disk (default: cwd; distinct from --repo scope flag)"`
	Author      string `json:"author,omitempty" flag:"author" desc:"commit author (auto-filled from git)"`
	CommittedAt string `json:"committed_at,omitempty" flag:"committed-at" desc:"commit timestamp (auto-filled from git)"`
	To          string `json:"to,omitempty" flag:"to" alias:"store" desc:"store to write to (a name from .lore/stores.yaml, me, <store>/<folder>; default repo)"`
}

// AddOut is the output of link.add.
type AddOut struct {
	ID          string `json:"id"`
	Store       string `json:"store"`
	SHA         string `json:"sha"`
	EntityTable string `json:"entity_table"`
	EntityID    string `json:"entity_id"`
	Message     string `json:"message,omitempty"`
}

// Text is v0.1.17's two lines.
func (o AddOut) Text() string {
	s := fmt.Sprintf("✓ linked %s → %s/%s  (%s)\n", short(o.SHA), o.EntityTable, o.EntityID, o.ID)
	if o.Message != "" {
		s += "  message: " + excerpt(o.Message) + "\n"
	}
	return s
}

// ListIn is the input of link.list.
type ListIn struct {
	Entity string `json:"entity,omitempty" flag:"entity" desc:"filter by entity (opaque id)"`
	Commit string `json:"commit,omitempty" flag:"commit" desc:"filter by sha"`
	// --created-by / --updated-by: the shared writer filters of every
	// noun list (INTEG-AUDIT-1; engine.List applies them).
	metalist.Audit
}

// ListOut is the output of link.list and commit.show: v0.1.17's envelope.
type ListOut struct {
	SchemaVersion int      `json:"schema_version"`
	Kind          string   `json:"kind"`
	Count         int      `json:"count,omitempty"`
	Data          []Record `json:"data"`
}

// Text is v0.1.17's link list.
func (o ListOut) Text() string {
	if len(o.Data) == 0 {
		return "(no links)\n"
	}
	var b strings.Builder
	for _, r := range o.Data {
		msg := ""
		if r.Message != "" {
			msg = " — " + excerpt(r.Message)
		}
		fmt.Fprintf(&b, "%s  %s → %s/%s%s%s\n", r.ID, short(r.SHA), r.EntityTable, r.EntityID, msg, opsmemory.LabelSuffix(r.Label))
	}
	return b.String()
}

// Record is one link in v0.1.17's --json form (the ent CommitLink struct).
type Record struct {
	ID               string          `json:"id"`
	CreatedAt        string          `json:"created_at,omitempty"`
	UpdatedAt        string          `json:"updated_at,omitempty"`
	ProjectID        string          `json:"project_id"`
	EntityTable      string          `json:"entity_table"`
	EntityID         string          `json:"entity_id"`
	SHA              string          `json:"sha"`
	Message          string          `json:"message,omitempty"`
	RepoPath         string          `json:"repo_path,omitempty"`
	Author           string          `json:"author,omitempty"`
	CommittedAt      string          `json:"committed_at,omitempty"`
	CreatedByActorID string          `json:"created_by_actor_id,omitempty"`
	Store            store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
	// CreatedBy / UpdatedBy are the row envelope's writers (INTEG-AUDIT-1,
	// auditrec), last so the older keys keep their order.
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// ShowIn is the input of commit.show (the sha is the path id in REST).
type ShowIn struct {
	ID string `json:"id" arg:"0" desc:"the commit sha"`
}

// ShowOut is the output of commit.show.
type ShowOut struct {
	SchemaVersion int      `json:"schema_version"`
	Kind          string   `json:"kind"`
	Count         int      `json:"count,omitempty"`
	Data          []Record `json:"data"`
	// SHA is the asked sha, for the human header.
	SHA string `json:"-"`
}

// Text is v0.1.17's commit-show output.
func (o ShowOut) Text() string {
	if len(o.Data) == 0 {
		return "(no entities linked to this commit)\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Commit %s — %d linked entities:\n", o.SHA, len(o.Data))
	for _, r := range o.Data {
		fmt.Fprintf(&b, "  %s/%s\n", r.EntityTable, r.EntityID)
	}
	return b.String()
}

// RemoveIn is the input of link.remove.
type RemoveIn struct {
	ID     string `json:"id" arg:"0" desc:"the link id (cml_…)"`
	Reason string `json:"reason,omitempty" flag:"reason" desc:"why it is deleted (stored as deleted_reason; required when policy delete.require_reason is true)"`
}

// RemoveOut is the output of link.remove.
type RemoveOut struct {
	ID    string `json:"id"`
	Store string `json:"store"`
}

// Text is v0.1.17's line.
func (o RemoveOut) Text() string { return "✓ link " + o.ID + " removed\n" }

// writeErrors are the codes a link write can return.
var writeErrors = []errs.Code{
	errs.InvalidInput, errs.InvalidID, errs.NotFound, errs.AmbiguousID,
	errs.SecretDetected, errs.NeedsConfirm, errs.Cancelled, errs.PolicyDenied, errs.ConfigInvalid,
	errs.Stale, errs.StoreReadOnly, errs.StoreLocked, errs.StoreUnreachable, errs.OldLore, errs.Internal,
}

// readErrors are the codes a link read can return.
var readErrors = []errs.Code{errs.InvalidInput, errs.InvalidID, errs.NotFound, errs.AmbiguousID, errs.StoreUnreachable, errs.OldLore, errs.Internal}

// Ops returns every link operation bound to env; git reads --commit.
func Ops(env Env, git CommitResolver) []op.Entry {
	o := ops{env: env, git: git}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Link a git commit to an entity (--entity=tsk_… --commit=<sha>)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
			Confirm:  op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "link the current commit", CLI: "lore link add --entity=tsk_<id> --commit=HEAD", Want: "✓ linked"}},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List commit links (filter by --entity or --commit)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors,
			Examples: []op.Example{{Title: "the commits linked to a task", CLI: "lore link list --entity tsk_31"}, {Title: "the rows linked to a commit", CLI: "lore link list --commit 3f2c9ab"}},
		}, o.list),
		op.Define(op.Spec{
			Name: OpRemove, Summary: "Remove a commit link by its ID (to the trash, restorable)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
			Confirm:  op.ConfirmRule{Always: true},
			Examples: []op.Example{{Title: "remove without a terminal", CLI: "lore link remove --id cml_<id> --yes", Want: "removed"}},
		}, o.remove),
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show every entity linked to a given git commit",
			Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors,
			Examples: []op.Example{{Title: "everything linked to a commit", CLI: "lore commit show --id 3f2c9ab"}},
		}, o.show),
	}
}

// ops binds the run functions to one Env and resolver.
type ops struct {
	env Env
	git CommitResolver
}

// add resolves the entity and the commit, then writes the link.
func (o ops) add(ctx context.Context, in AddIn) (AddOut, error) {
	if in.Entity == "" || in.Commit == "" {
		return AddOut{}, errs.New(errs.InvalidInput, "--entity and --commit are required").
			WithHint("example: lore link add --entity=tsk_<id> --commit=HEAD")
	}
	tbl, id, core, err := o.entity(ctx, in.Entity)
	if err != nil {
		return AddOut{}, err
	}
	if core {
		if perr := pointer.Check(ctx, o.env, store.Source(in.To), engine.Ref{Table: tbl, ID: id}); perr != nil {
			return AddOut{}, perr
		}
	}
	sha, msg, author, when, err := o.resolve(ctx, in.Commit, in.RepoPath)
	if err != nil {
		return AddOut{}, err
	}
	msg, author, when = orElse(in.Message, msg), orElse(in.Author, author), orElse(in.CommittedAt, when)
	project, err := o.env.Project(ctx)
	if err != nil {
		return AddOut{}, err
	}
	fields := map[string]value.Value{}
	if fields[fieldProject], err = value.RefOf(project.Any()); err != nil {
		return AddOut{}, err
	}
	if fields[schema.FieldEntityID], err = value.RefOf(id); err != nil {
		return AddOut{}, err
	}
	for k, v := range map[string]string{
		schema.FieldEntityTable: string(tbl), fieldSHA: sha, fieldMessage: msg,
		fieldRepoPath: in.RepoPath, fieldAuthor: author, fieldCommittedAt: when,
	} {
		if v == "" {
			continue
		}
		if fields[k], err = value.TextOf(v); err != nil {
			return AddOut{}, errs.Prefix("link add: field "+k, err)
		}
	}
	// Natural ids are per store: an existing link is found by its key.
	have, err := engine.FindByKey(ctx, o.env, table, map[string]string{
		schema.FieldEntityTable: string(tbl), schema.FieldEntityID: id.String(), fieldSHA: sha,
	})
	if err != nil {
		return AddOut{}, err
	}
	if len(have) > 0 {
		return AddOut{}, errs.New(errs.InvalidInput, fmt.Sprintf("commit %s already linked to %s/%s", short(sha), tbl, id)).
			WithHint("the link is " + have[0].Row.ID.String() + "; nothing written (lore link list --entity " + id.String() + ")")
	}
	w, err := o.env.Create(ctx, store.Source(in.To), table, fields)
	if err != nil {
		return AddOut{}, err
	}
	return AddOut{
		ID: w.Row.ID.String(), Store: o.storeOf(ctx, w.Row.ID, store.Source(in.To)),
		SHA: sha, EntityTable: string(tbl), EntityID: id.String(), Message: msg,
	}, nil
}

// resolve handles --commit=<sha> | HEAD | auto as v0.1.17 did: HEAD/auto
// resolve and fill message, author and time; another ref resolves to its
// sha only; a ref git cannot resolve is kept when it looks like a sha.
func (o ops) resolve(ctx context.Context, arg, dir string) (sha, msg, author, when string, err error) {
	ref, fill := arg, false
	if arg == commitHEAD || arg == commitAuto {
		ref, fill = commitHEAD, true
	}
	sha, gerr := o.git.RevParse(ctx, dir, ref)
	if gerr != nil {
		if likelySHA(arg) {
			return arg, "", "", "", nil
		}
		return "", "", "", "", errs.New(errs.InvalidInput, fmt.Sprintf("could not resolve %q via git rev-parse — use a full sha or run from inside a git repo", arg)).
			WithHint("give --commit=HEAD or a sha from git log, run inside the repo")
	}
	if !fill {
		return sha, "", "", "", nil
	}
	msg, author, when, derr := o.git.Describe(ctx, dir, sha)
	if derr != nil {
		return sha, "", "", "", nil // v0.1.17 ignored a failed git log
	}
	return sha, msg, author, when, nil
}

// entity resolves --entity to its table (by id prefix) and id. A table on
// the core must hold the id as a live row; a table not on the core yet is
// accepted, as v0.1.17 accepted it (core reports which).
func (o ops) entity(ctx context.Context, raw string) (row.Table, ids.AnyID, bool, error) {
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		return "", ids.AnyID{}, false, errs.Wrap(errs.Internal, "link: built-in schema", err)
	}
	name, id, core, err := tableForID(reg, raw)
	if err != nil {
		return "", ids.AnyID{}, false, err
	}
	if core {
		_, r, gerr := o.env.Get(ctx, engine.Ref{Table: name, ID: id})
		if gerr == nil && r.Deleted { // DELETION 3.2: E_IN_TRASH for a trashed end
			gerr = lookup.Gone(ctx, o.env, engine.Ref{Table: name, ID: id}, errs.New(errs.NotFound, "entity "+id.String()+" is deleted"))
		}
		if gerr != nil {
			return "", ids.AnyID{}, false, gerr
		}
	}
	return name, id, core, nil
}

// tableForID is v0.1.17's tableForOpaqueID over the core: the table an
// opaque id's prefix names (core is true when the table is in reg, so the
// row can be checked). A prefix no table uses is E_INVALID_INPUT "unknown
// ID prefix" (v0.1.17's refusal, also for text with no prefix at all); a
// known prefix with a malformed id is E_NOT_FOUND.
func tableForID(reg *schema.Registry, raw string) (row.Table, ids.AnyID, bool, error) {
	raw = strings.TrimSpace(raw)
	p, _, _ := strings.Cut(raw, "_")
	def, core := reg.ByPrefix(ids.Prefix(p))
	name := def.Name
	if !core {
		var ok bool
		if name, ok = legacyTables[ids.Prefix(p)]; !ok {
			return "", ids.AnyID{}, false, errs.New(errs.InvalidInput, fmt.Sprintf("unknown ID prefix %q — cannot derive entity table", p))
		}
	}
	id, err := ids.ParseAny(raw)
	if err != nil {
		return "", ids.AnyID{}, false, errs.New(errs.NotFound, "entity "+quote(raw)+" not found").
			WithHint("use the opaque id (tsk_…, mem_…); `lore <kind> list` shows them")
	}
	return name, id, core, nil
}

// LegacyTables returns a copy of v0.1.17's prefix → table map for the
// tables not on the core yet (the v0.1.17 binary's test checks every one
// is a real lore.db table).
func LegacyTables() map[ids.Prefix]row.Table { return maps.Clone(legacyTables) }

// legacyTables is v0.1.17's prefix → table map (tableForOpaqueID) for the
// tables not on the core yet; a table on the core is found in the schema.
// Keys are the real id prefixes (saas/pkg/aicoder/ids/registry.go):
// v0.1.17's map had ann/ckr/sgg/tpr/rem, which no id ever carried.
var legacyTables = map[ids.Prefix]row.Table{
	"tsk": "tasks", "mem": "memories", "msn": "missions", "tlt": "task_lists", "pln": "plans",
	"dec": "decisions", "rul": "rules", "hfx": "hotfixes", "pat": "patterns", "pbk": "playbooks",
	"prm": "prompts", "anr": "architecture_notes", "bhv": "behaviours", "cbr": "cookbook_recipes",
	"inc": "incidents", "sug": "suggestions", "tst": "taste_prefs", "snp": "snapshots",
	"wfl": "workflows", "wsp": "workspaces", "hnd": "handoffs", "rmd": "reminders", "run": "runs",
	"tdc": "tech_docs", "cmt": "comments",
}

// list returns the project's live links, filtered.
func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	if in.Entity != "" {
		if _, err := ids.ParseAny(strings.TrimSpace(in.Entity)); err != nil {
			return ListOut{}, errs.New(errs.NotFound, "entity "+quote(in.Entity)+" not found")
		}
	}
	rows, err := o.links(ctx, func(r row.Row) bool {
		return (in.Entity == "" || field(r, schema.FieldEntityID) == strings.TrimSpace(in.Entity)) &&
			(in.Commit == "" || sameCommit(field(r, fieldSHA), in.Commit))
	})
	if err != nil {
		return ListOut{}, err
	}
	return ListOut{SchemaVersion: SchemaVersion, Kind: KindList, Count: len(rows), Data: rows}, nil
}

// show returns every link of one commit. The argument is a full sha, a
// unique hex prefix of minSHAHex+ characters (any case), or any revision
// git resolves in the working directory (HEAD, a branch, HEAD~2). A hex
// prefix shared by two linked shas is E_AMBIGUOUS_ID naming them; a hex
// prefix that matches no link and that git cannot expand shows nothing
// linked (it is a well-formed sha); anything else is E_INVALID_INPUT.
func (o ops) show(ctx context.Context, in ShowIn) (ShowOut, error) {
	arg := strings.TrimSpace(in.ID)
	if arg == "" {
		return ShowOut{}, errs.New(errs.InvalidInput, "commit show: the sha is empty").
			WithHint("give a sha (7+ hex characters) or a git revision such as HEAD")
	}
	all, err := o.links(ctx, func(row.Row) bool { return true })
	if err != nil {
		return ShowOut{}, err
	}
	sha, err := o.showSHA(ctx, arg, all)
	if err != nil {
		return ShowOut{}, err
	}
	rows := []Record{}
	for _, r := range all {
		if sameCommit(strings.ToLower(r.SHA), sha) {
			rows = append(rows, r)
		}
	}
	return ShowOut{SchemaVersion: SchemaVersion, Kind: KindShow, Count: len(rows), Data: rows, SHA: sha}, nil
}

// showSHA turns commit.show's argument into the one sha to list: a hex
// prefix is matched against the linked shas first (so a commit that is
// not in this clone's git history is still found), then git expands it.
func (o ops) showSHA(ctx context.Context, arg string, all []Record) (string, error) {
	lower := strings.ToLower(arg)
	if likelySHA(lower) {
		matches := []string{}
		for _, r := range all {
			if s := strings.ToLower(r.SHA); sameCommit(s, lower) && !slices.Contains(matches, s) {
				matches = append(matches, s)
			}
		}
		switch len(matches) {
		case 1:
			return matches[0], nil
		case 0:
			if full, gerr := o.git.RevParse(ctx, "", lower); gerr == nil {
				return strings.ToLower(full), nil
			}
			return lower, nil
		default:
			slices.Sort(matches)
			return "", errs.New(errs.AmbiguousID, fmt.Sprintf("commit show: %q matches %d linked commits: %s", arg, len(matches), strings.Join(matches, ", "))).
				WithHint("give more characters of the sha")
		}
	}
	full, gerr := o.git.RevParse(ctx, "", arg)
	if gerr != nil {
		return "", errs.New(errs.InvalidInput, fmt.Sprintf("commit show: %s is not a sha (%d+ hex characters) and git cannot resolve it here", quote(arg), minSHAHex)).
			WithHint("give a sha such as 3f2c9ab, or a revision such as HEAD; run inside the git repo the commit belongs to")
	}
	return strings.ToLower(strings.TrimSpace(full)), nil
}

// links lists the live links of the current project in every attached
// store that keep says to keep, oldest first.
func (o ops) links(ctx context.Context, keep func(row.Row) bool) ([]Record, error) {
	project, err := o.env.Project(ctx)
	if err != nil {
		return nil, err
	}
	out := []Record{}
	for _, src := range engine.ReadStoresOf(o.env) {
		rows, err := o.env.List(ctx, src, table)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if r.Deleted || field(r, fieldProject) != project.String() || !keep(r) {
				continue
			}
			out = append(out, recordOf(opsmemory.LabelerOf(o.env), src, r))
		}
	}
	slices.SortStableFunc(out, func(a, b Record) int { return strings.Compare(a.CreatedAt, b.CreatedAt) })
	return out, nil
}

// remove writes the link's tombstone.
func (o ops) remove(ctx context.Context, in RemoveIn) (RemoveOut, error) {
	ctx = engine.WithDeleteReason(ctx, in.Reason) // INTEG-TRASH-3; E_REASON_REQUIRED under delete.require_reason
	id, err := ids.ParseAny(strings.TrimSpace(in.ID))
	if err != nil || id.Prefix() != ids.PrefixCommitLink {
		return RemoveOut{}, lookup.BadID("link: "+quote(in.ID)+" is not a link id (cml_…)", nil)
	}
	src := o.storeOf(ctx, id, "")
	w, err := o.env.Delete(ctx, engine.Ref{Table: table, ID: id})
	if err != nil {
		return RemoveOut{}, err
	}
	return RemoveOut{ID: w.Row.ID.String(), Store: src}, nil
}

// storeOf names the store holding id after a write.
func (o ops) storeOf(ctx context.Context, id ids.AnyID, requested store.Source) string {
	if requested != "" {
		return string(requested)
	}
	src, _, err := o.env.Get(ctx, engine.Ref{Table: table, ID: id})
	if err != nil {
		return ""
	}
	return string(src)
}

// recordOf is the v0.1.17 --json form of a link row.
func recordOf(lab opsmemory.Labeler, src store.Source, r row.Row) Record {
	rec := Record{
		ID: r.ID.String(), CreatedAt: timeText(r.CreatedAt), UpdatedAt: timeText(r.UpdatedAt), CreatedBy: auditrec.Of(r).CreatedBy, UpdatedBy: auditrec.Of(r).UpdatedBy,
		ProjectID: field(r, fieldProject), EntityTable: field(r, schema.FieldEntityTable), EntityID: field(r, schema.FieldEntityID),
		SHA: field(r, fieldSHA), Message: field(r, fieldMessage), RepoPath: field(r, fieldRepoPath),
		Author: field(r, fieldAuthor), CommittedAt: field(r, fieldCommittedAt), CreatedByActorID: field(r, fieldCreatedBy),
	}
	if rec.CreatedByActorID == "" && !r.CreatedBy.IsZero() {
		rec.CreatedByActorID = r.CreatedBy.String()
	}
	if src != store.SourceRepo {
		rec.Store = opsmemory.SourceOf(lab, src)
		rec.Label = opsmemory.Label(lab, src)
	}
	return rec
}

// likelySHA reports whether s looks like a (short or full) hex sha.
func likelySHA(s string) bool {
	if len(s) < minSHAHex || len(s) > maxSHAHex {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// sameCommit reports whether a stored link sha and a --commit filter name
// the same commit: equal, or one a hex prefix of the other (a person types
// the 7 or 8 characters git prints; add stores the full sha it resolved,
// or the short sha as given when git could not resolve it).
func sameCommit(stored, filter string) bool {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if stored == filter {
		return true
	}
	if !likelySHA(filter) || !likelySHA(stored) {
		return false
	}
	return strings.HasPrefix(stored, filter) || strings.HasPrefix(filter, stored)
}

// short is the first 8 characters of a sha (the whole of a shorter one).
func short(sha string) string {
	if len(sha) > shortSHA {
		return sha[:shortSHA]
	}
	return sha
}

// excerpt cuts a message for display (v0.1.17: 60, cut at 57 + "...").
func excerpt(m string) string {
	return textcut.Cut(m, msgMax, msgCut)
}

// orElse returns a, or b when a is empty.
func orElse(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// field returns the text form of a row field ("" when absent or null).
func field(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// timeText renders an envelope time as v0.1.17's JSON did.
func timeText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// quote renders a user value for an error, truncated.
func quote(s string) string {
	return `"` + textcut.Cut(s, maxQuoted, maxQuoted) + `"`
}
