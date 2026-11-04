// Package infra defines the operations of the infra nouns that live in the
// synced stores (layer L5): `lore actor list|show`, `lore project
// list|show|archive|unarchive|delete` and `lore repo
// add|list|archive|unarchive|delete`, with v0.1.17's spellings and output.
//
// Why one package: actors, projects and repos are the identity rows every
// other row hangs off; they share the archive/unarchive/delete verbs and
// the project lookup, so they share one Env and one set of helpers.
//
// What is NOT here, on purpose: the roster writes (`lore actor
// register|deactivate`, `lore team add|list`) write the org folder of the
// org's store, which the 0.1.18 core does not attach yet, so they stay on
// the v0.1.17 code; the machine-local tables (identity_profiles,
// mount_alias, pii_patterns, trusted_plugins, task_views, config,
// schema_migrations, users) never travel in a row and stay in lore.db.
//
// Invariant: every write goes through Env.Create / Update / Delete (the
// engine's one write path); nothing here writes a file.
package infra

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/store"
)

// Env is the engine + composition-root API the infra operations use; it
// is the memory operations' Env (wire.App implements it).
type Env = work.Env

// Nouns (CLI groups, REST collections).
const (
	NounActor   = "actor"
	NounProject = "project"
	NounRepo    = "repo"
)

// Operation names.
const (
	OpActorList        = "actor.list"
	OpActorShow        = "actor.show"
	OpProjectList      = "project.list"
	OpProjectShow      = "project.show"
	OpProjectArchive   = "project.archive"
	OpProjectUnarchive = "project.unarchive"
	OpProjectDelete    = "project.delete"
	OpRepoAdd          = "repo.add"
	OpRepoList         = "repo.list"
	OpRepoArchive      = "repo.archive"
	OpRepoUnarchive    = "repo.unarchive"
	OpRepoDelete       = "repo.delete"
	// OpRepoSetURL and OpRepoFork are in repo.go (the identity verbs).
)

// Envelope values of v0.1.17's --json (constants.Kind*).
const (
	SchemaVersion   = 1
	KindActorList   = "actor.list"
	KindActorShow   = "actor.show"
	KindProjectList = "project.list"
	KindProjectShow = "project.show"
	KindRepoList    = "repo.list"
)

// Field names of the actors, projects and repos tables (schema coreDefs).
const (
	fieldName        = "name"
	fieldKind        = "kind"
	fieldDisplayName = "display_name"
	fieldStableKey   = "stable_key"
	fieldHandle      = "handle"
	fieldLabel       = "label"
	fieldOwner       = "owner_actor_id"
	fieldActive      = "active"
	fieldLeftAt      = "left_at"
	fieldOrigin      = "origin_url"
	fieldArchivedAt  = "archived_at"
	fieldProject     = "project_id"
	fieldMountName   = "mount_name"
)

// showTimeLayout is v0.1.17's human time on `project show`.
const showTimeLayout = "2006-01-02 15:04:05Z"

// archivedMark is v0.1.17's list suffix of an archived row (no colour).
const archivedMark = " [archived]"

// writeErrors / readErrors are the codes the infra operations can return.
var (
	writeErrors  = append([]errs.Code{errs.InvalidIdentifier, errs.MountNameTaken}, work.WriteErrors...)
	deleteErrors = work.DeleteErrors
	readErrors   = work.ReadErrors
)

// Ops returns every infra operation bound to env.
func Ops(env Env) []op.Entry {
	o := ops{env: env}
	entries := []op.Entry{
		op.Define(op.Spec{Name: OpActorList, Summary: "List actors", Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors,
			Examples: []op.Example{{Title: "list as JSON", CLI: "lore actor list --json", Want: `"data"`}}}, o.actorList),
		op.Define(op.Spec{Name: OpActorShow, Summary: "Show actor details", Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors}, o.actorShow),
		op.Define(op.Spec{Name: OpProjectList, Summary: "List projects in the current DB", Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors,
			Examples: []op.Example{{Title: "list as JSON", CLI: "lore project list --json", Want: `"data"`}}}, o.projectList),
		op.Define(op.Spec{Name: OpProjectShow, Summary: "Show project details (with attached repos)", Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors}, o.projectShow),
		op.Define(op.Spec{Name: OpProjectArchive, Summary: "Archive a project (soft-delete via archived_at)", Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors}, o.projectArchive),
		op.Define(op.Spec{Name: OpProjectUnarchive, Summary: "Unarchive a project (clear archived_at)", Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors}, o.projectUnarchive),
		op.Define(op.Spec{Name: OpProjectDelete, Summary: "Delete a project (a tombstone; archive is usually what you want)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: deleteErrors, Confirm: op.ConfirmRule{Always: true}}, o.projectDelete),
		op.Define(op.Spec{Name: OpRepoAdd, Summary: "Register a repo within the current project", Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
			Confirm:  op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "register a repo", CLI: "lore repo add web", Want: "✓ repo web registered"}}}, o.repoAdd),
		op.Define(op.Spec{Name: OpRepoList, Summary: "List repos in the current project", Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors}, o.repoList),
		op.Define(op.Spec{Name: OpRepoArchive, Summary: "Archive a repo (soft-delete via archived_at)", Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors}, o.repoArchive),
		op.Define(op.Spec{Name: OpRepoUnarchive, Summary: "Unarchive a repo (clear archived_at)", Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors}, o.repoUnarchive),
		op.Define(op.Spec{Name: OpRepoDelete, Summary: "Delete a repo (a tombstone; archive is usually what you want)", Permission: op.PermWrite, Access: op.AccessWrite,
			Errors: deleteErrors, Confirm: op.ConfirmRule{Always: true}}, o.repoDelete),
	}
	// PROV1/PROV2: the repo identity verbs need the checkout's files and
	// remotes, an upgrade only a checkout-backed Env (wire.App) offers.
	if files, ok := upgrade.As[RepoFiles](env); ok {
		entries = append(entries,
			op.Define(op.Spec{Name: OpRepoSetURL, Summary: "Confirm this repo's address: the primary (moved or renamed), a --mirror, or --remove one", Permission: op.PermWrite, Access: op.AccessWrite,
				Errors:   append([]errs.Code{errs.URLUserinfo}, writeErrors...),
				Examples: []op.Example{{Title: "the repo was renamed", CLI: "lore repo set-url git@github.com:agency/acme-hub-api.git", Want: "primary address is now"}}},
				func(ctx context.Context, in RepoSetURLIn) (RepoSetURLOut, error) {
					return repoSetURL(ctx, env, files, in)
				}),
			op.Define(op.Spec{Name: OpRepoFork, Summary: "This clone is a fork (or --split): give it its own permanent repo id", Permission: op.PermWrite, Access: op.AccessWrite,
				Errors: writeErrors},
				func(ctx context.Context, in RepoForkIn) (RepoForkOut, error) { return repoFork(ctx, env, files, in) }),
		)
		if rw, ok := upgrade.As[OriginRewriter](env); ok {
			entries = append(entries, op.Define(op.Spec{Name: OpRepoMergeIDs, Summary: "Rewrite _o.repo of rows stamped with an alias repo id to the winning id, then clear the aliases (PROVENANCE 11.9)",
				Permission: op.PermWrite, Access: op.AccessWrite, Confirm: op.ConfirmRule{MayAsk: true},
				Errors:   append([]errs.Code{errs.NeedsConfirm}, writeErrors...),
				Examples: []op.Example{{Title: "see what would change", CLI: "lore repo merge-ids --dry-run", Want: "_o.repo"}}},
				func(ctx context.Context, in RepoMergeIDsIn) (RepoMergeIDsOut, error) {
					return repoMergeIDs(ctx, env, files, rw, in)
				}))
		}
	}
	return entries
}

// ops binds the run functions to one Env.
type ops struct{ env Env }

// IDIn is the input of the verbs that take one row id.
type IDIn struct {
	ID string `json:"id" arg:"0" desc:"the row id"`
}

// stamp sets (archive) or clears (unarchive) archived_at of the row id in
// table, printing v0.1.17's "✓ <label> archived|unarchived" line.
func (o ops) stamp(ctx context.Context, noun string, p ids.Prefix, table row.Table, raw string, archive bool, label func(row.Row) string) (work.WriteOut, error) {
	id, err := work.ParseID(noun, p, raw)
	if err != nil {
		return work.WriteOut{}, err
	}
	_, r, err := work.Get(ctx, o.env, noun, table, id)
	if err != nil {
		return work.WriteOut{}, err
	}
	var v value.Value = value.Null{}
	verb := "unarchived"
	if archive {
		v, verb = work.Now(o.env), "archived"
	}
	return work.Patch(ctx, o.env, noun, table, id, map[string]value.Value{fieldArchivedAt: v},
		work.Success+" "+label(r)+" "+verb+"\n")
}

// remove tombstones the row id in table ("✓ <label> deleted").
func (o ops) remove(ctx context.Context, noun string, p ids.Prefix, table row.Table, raw string, label func(row.Row) string) (work.WriteOut, error) {
	id, err := work.ParseID(noun, p, raw)
	if err != nil {
		return work.WriteOut{}, err
	}
	_, r, err := work.Get(ctx, o.env, noun, table, id)
	if err != nil {
		return work.WriteOut{}, err
	}
	return work.Delete(ctx, o.env, noun, table, id, work.Success+" "+label(r)+" deleted\n")
}

// archived reports whether r carries an archived_at stamp.
func archived(r row.Row) bool { return work.Field(r, fieldArchivedAt) != "" }

// listLine is v0.1.17's list line: "<id>  <name>[  <origin>][ [archived]]".
func listLine(r row.Row, name string) string {
	var b strings.Builder
	b.WriteString(r.ID.String() + "  " + name)
	if o := work.Field(r, fieldOrigin); o != "" {
		b.WriteString("  " + o)
	}
	if archived(r) {
		b.WriteString(archivedMark)
	}
	return b.String() + "\n"
}

// timeOf is an envelope time in v0.1.17's JSON form ("" when unset).
func timeOf(r row.Row, created bool) string {
	t := r.UpdatedAt
	if created {
		t = r.CreatedAt
	}
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
}

// sourceOf is the `_source` object of a row of src (zero, so omitted, for the repo store;
// FLAG1, opsmemory.SourceOf through work.SourceOf).
func sourceOf(l work.Labeler, src store.Source) store.SourceRef { return work.SourceOf(l, src) }
