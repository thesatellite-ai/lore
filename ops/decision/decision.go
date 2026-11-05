// Package decision defines the `decision` noun's operations (architectural
// decision records), layer L5: add, list, show, edit, archive, unarchive,
// delete and search, with v0.1.17's flag spellings, --json envelope and
// human output. The row work is shared with rule and hotfix in
// ops/internal/knowledge.
package decision

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/refpoint"
	"github.com/thesatellite-ai/lore/ops/internal/expand"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"github.com/thesatellite-ai/lore/ops/internal/trashline"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	kn "github.com/thesatellite-ai/lore/ops/internal/knowledge"
	"github.com/thesatellite-ai/lore/ops/internal/knscope"
	"github.com/thesatellite-ai/lore/ops/internal/rowref"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (decision.add → `lore decision add`, POST /v1/decisions).
const (
	Noun        = "decision"
	OpAdd       = "decision.add"
	OpList      = "decision.list"
	OpShow      = "decision.show"
	OpEdit      = "decision.edit"
	OpArchive   = "decision.archive"
	OpUnarchive = "decision.unarchive"
	OpDelete    = "decision.delete"
	OpSearch    = "decision.search"
)

// Envelope kinds (v0.1.17 constants.KindDecision*).
const (
	KindList   = "decision.list"
	KindShow   = "decision.show"
	KindSearch = "decision.search"
)

// SchemaVersion is v0.1.17's envelope schema_version.
const SchemaVersion = 1

// fieldStatus is the decision status field.
const fieldStatus = "status"

// noun is the decisions table.
var noun = kn.Noun{Name: Noun, Table: schema.TableDecisions, Prefix: ids.PrefixDecision}

// Ops returns every decision operation bound to env, for op.NewRegistry.
func Ops(env kn.Env) []op.Entry {
	o := ops{env: env}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Add a new decision (context + chosen + alternatives + reasoning)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: kn.WriteErrors,
			Confirm:  op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "record a decision", CLI: `lore decision add --title="Use SQLite" --body="Local first; no server."`, Want: "dec_"}},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List decisions",
			Permission: op.PermRead, Access: op.AccessRead, Errors: kn.ReadErrors,
		}, o.list),
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show decision details",
			Permission: op.PermRead, Access: op.AccessRead, Errors: expand.RevealErrors(kn.ReadErrors), Confirm: expand.RevealConfirm,
		}, o.showExpand),
		op.Define(op.Spec{
			Name: OpEdit, Summary: "Edit decision fields (only the flags given change)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: kn.WriteErrors,
			Confirm: op.ConfirmRule{MayAsk: true},
		}, o.edit),
		op.Define(op.Spec{
			Name: OpArchive, Summary: "Archive a decision (hidden from render and search; kept)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: kn.WriteErrors,
		}, o.archive),
		op.Define(op.Spec{
			Name: OpUnarchive, Summary: "Bring an archived decision back",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: kn.WriteErrors,
		}, o.unarchive),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a decision (it goes to the trash; lore trash restore brings it back)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: kn.DeleteErrors(),
			Confirm: op.ConfirmRule{Always: true},
		}, o.delete),
		op.Define(op.Spec{
			Name: OpSearch, Summary: "Full-text search over decisions of every attached store",
			Permission: op.PermRead, Access: op.AccessRead, Errors: kn.SearchErrors,
		}, o.search),
	}
}

// AddIn is the input of decision.add.
type AddIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

	Title      string   `json:"title" flag:"title" desc:"short headline (required)"`
	Body       string   `json:"body,omitempty" flag:"body" desc:"body (required; or give it as the first argument)" ui:"widget=markdown"`
	Text       string   `json:"text,omitempty" arg:"0" desc:"the decision body, instead of --body"`
	Status     string   `json:"status,omitempty" flag:"status" desc:"proposed | accepted | superseded | deprecated (default accepted)"`
	Source     string   `json:"source,omitempty" flag:"source" desc:"source_kind (default manual)"`
	SourceRef  string   `json:"source_ref,omitempty" flag:"source-ref" desc:"free-form provenance"`
	Supersedes string   `json:"supersedes,omitempty" flag:"supersedes" desc:"decision_id (dec_*) this entry replaces"`
	NoMeta     bool     `json:"no_meta,omitempty" flag:"no-meta" desc:"with --supersedes: do not copy the replaced row's metadata (METADATA 3.19)"`
	CreatedBy  string   `json:"created_by,omitempty" flag:"created-by" desc:"actor_id (act_*); defaults to current identity"`
	To         string   `json:"to,omitempty" flag:"to" desc:"store to write to (a name from .lore/stores.yaml, me, <store>/<folder>; default repo)"`
	Repo       string   `json:"repo,omitempty" flag:"repo" desc:"scope to a specific repo (mount_name or rep_id)"`
	Project    string   `json:"project,omitempty" flag:"project" desc:"override project (id or name)"`
	Tags       []string `json:"tags,omitempty" flag:"tag" desc:"tag name (repeatable; the tag must exist)"`
	Scope      string   `json:"scope,omitempty" flag:"scope" desc:"where it attaches: global | project | repo | dir:<path> (default: today's project/repo)"`
	// Ref / Relation: knowledge_refs pointers (RES 3.11, RQ7, OQ22).
	Ref      []string `json:"ref,omitempty" flag:"ref" desc:"a pointer to another row (<id> or <project>:<id>) or a commit (commit:<sha>); repeatable"`
	Relation string   `json:"relation,omitempty" flag:"relation" desc:"the relation each --ref records (snake_case; default mentions)"`
}

// AddOut is the output of decision.add.
type AddOut struct {
	ID       string       `json:"id"`
	Store    string       `json:"store"`
	Title    string       `json:"title"`
	Warnings []kn.Warning `json:"warnings,omitempty"`
	// CarriedMeta is how many metadata keys --supersedes copied (3.19).
	CarriedMeta int `json:"carried_meta,omitempty"`
	// label is the store's `_label` for the add line (kn.Label; "" for
	// the repo): v0.1.17 printed route.Label, "[northwind/acme]".
	label string
}

// Text prints v0.1.17's add line.
func (o AddOut) Text() string {
	return fmt.Sprintf("✓ %s — %s %s\n", o.ID, o.Title, o.label) + carriedLine(o.CarriedMeta) + kn.WarnText(o.Warnings)
}

// carriedLine is the "carried over N keys" note of a --supersedes add.
func carriedLine(n int) string { return metalist.Carry{Count: n}.Line() }

// WriteOut is the output of edit, archive, unarchive and delete.
type WriteOut struct {
	ID       string       `json:"id"`
	Store    string       `json:"store"`
	Warnings []kn.Warning `json:"warnings,omitempty"`
	done     string
	// trashed marks a delete: the line says where the row went (DQ8).
	trashed bool
}

// Text prints v0.1.17's "✓ <id> <verb>"; a delete prints the trash line
// with its restore hint (DQ8, DELETION 3.1).
func (o WriteOut) Text() string {
	if o.trashed {
		return trashline.Moved(o.ID, o.ID)
	}
	return "✓ " + o.ID + " " + o.done + "\n" + kn.WarnText(o.Warnings)
}

// ListIn is the input of decision.list. v0.1.17 listed archived
// decisions too (no --archived flag); so does this.
type ListIn struct {
	Repo       string   `json:"repo,omitempty" flag:"repo" desc:"scope to a specific repo (mount_name or rep_id)"`
	NoInherit  bool     `json:"no_inherit,omitempty" flag:"no-inherit" desc:"strict scope: --repo only, no master inheritance"`
	MasterOnly bool     `json:"master_only,omitempty" flag:"master-only" desc:"only master-scoped rows (repo_id IS NULL)"`
	AllRepos   bool     `json:"all_repos,omitempty" flag:"all-repos" desc:"list across every repo (ignore --repo)"`
	Project    string   `json:"project,omitempty" flag:"project" desc:"override project (id or name)"`
	Tags       []string `json:"tags,omitempty" flag:"tag" desc:"only decisions with this tag (repeatable: all must match)"`
	Store      string   `json:"store,omitempty" flag:"store" desc:"only decisions of this store (default every attached store)"`
	// MetaFlags: --meta / --place / --sort meta.<key> (METADATA 3.7).
	kn.MetaFlags
}

// ListOut is v0.1.17's list envelope.
type ListOut struct {
	SchemaVersion int      `json:"schema_version"`
	Kind          string   `json:"kind"`
	Count         int      `json:"count,omitempty"`
	Data          []Record `json:"data"`
	// Notes are the metadata filter's lines (unknown key, skipped, not
	// checked; METADATA 3.7, 3.13), printed after the rows.
	Notes []string `json:"notes,omitempty"`
}

// Text prints one line per decision, or "(no decisions)".
func (o ListOut) Text() string { return o.rowsText() + kn.NotesText(o.Notes) }

// rowsText is the v0.1.17 lines.
func (o ListOut) rowsText() string {
	if len(o.Data) == 0 {
		return "(no decisions)\n"
	}
	var b strings.Builder
	for _, r := range o.Data {
		fmt.Fprintf(&b, "%s [%s] %s%s\n", r.ID, r.Status, kn.Cut(r.Title, kn.ListCut), kn.LabelSuffix(r.Label))
	}
	return b.String()
}

// Record is one decision in v0.1.17's --json form (ent Decision JSON:
// same keys, same order).
type Record struct {
	ID               string          `json:"id,omitempty"`
	CreatedAt        time.Time       `json:"created_at,omitempty"`
	UpdatedAt        time.Time       `json:"updated_at,omitempty"`
	TrustScore       float64         `json:"trust_score,omitempty"`
	Confidence       float64         `json:"confidence,omitempty"`
	LastValidatedAt  time.Time       `json:"last_validated_at,omitempty,omitzero"`
	ArchivedAt       time.Time       `json:"archived_at,omitempty,omitzero"`
	SourceKind       string          `json:"source_kind,omitempty"`
	SourceRef        string          `json:"source_ref,omitempty"`
	ProjectID        string          `json:"project_id,omitempty"`
	RepoID           string          `json:"repo_id,omitempty"`
	Title            string          `json:"title,omitempty"`
	Body             string          `json:"body,omitempty"`
	Status           string          `json:"status,omitempty"`
	SupersededByID   string          `json:"superseded_by_id,omitempty"`
	ScopeID          string          `json:"scope_id,omitempty"`
	CreatedByActorID string          `json:"created_by_actor_id,omitempty"`
	TagIDs           []string        `json:"tag_ids,omitempty"`
	Store            store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
}

// IDIn is the input of the operations that take one decision id.
type IDIn struct {
	ID      string `json:"id" arg:"0" desc:"the decision id (dec_…)"`
	Project string `json:"project,omitempty" flag:"project" desc:"accepted for v0.1.17 scripts; the id already names its project"`
}

// DeleteIn is the input of decision.delete.
type DeleteIn struct {
	ID      string `json:"id" arg:"0" desc:"the decision id (dec_…)"`
	Project string `json:"project,omitempty" flag:"project" desc:"accepted for v0.1.17 scripts; the id already names its project"`
	Reason  string `json:"reason,omitempty" flag:"reason" desc:"why it is deleted (stored as deleted_reason; required when policy delete.require_reason is true)"`
}

// ShowOut is v0.1.17's show envelope.
type ShowOut struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Data          Record `json:"data"`
	// Refs are the decision's --ref pointers (0.1.18; absent when none).
	Refs []rowref.Out `json:"refs,omitempty"`
	// Expanded / Refs are --expand's additions (SECRETS 12: "--json adds
	// expanded and refs"); empty without --expand.
	Expanded     map[string]string `json:"expanded,omitempty"`
	ExpandedRefs []refpoint.Used   `json:"expanded_refs,omitempty"`
	// Revealed, Forced and Warning: --reveal (SECRETS 2.7, SQ10).
	Revealed int    `json:"revealed,omitempty"`
	Forced   bool   `json:"forced,omitempty"`
	Warning  string `json:"warning,omitempty"`
}

// Text prints v0.1.17's show block.
func (o ShowOut) baseText() string {
	s := fmt.Sprintf("%s\n  title:  %s\n  status: %s\n", o.Data.ID, o.Data.Title, o.Data.Status)
	for _, r := range o.Refs {
		s += "  " + r.Relation + " " + r.Where() + "\n"
	}
	if o.Data.Body != "" {
		s += "\n" + o.Data.Body + "\n"
	}
	return s
}

// EditIn is the input of decision.edit; an empty flag leaves its field.
type EditIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

	ID           string   `json:"id" arg:"0" desc:"the decision id (dec_…)"`
	Title        string   `json:"title,omitempty" flag:"title" desc:"new title"`
	Body         string   `json:"body,omitempty" flag:"body" desc:"new body" ui:"widget=markdown"`
	Status       string   `json:"status,omitempty" flag:"status" desc:"proposed | accepted | superseded | deprecated"`
	SourceRef    string   `json:"source_ref,omitempty" flag:"source-ref" desc:"new source-ref"`
	RebindRepo   string   `json:"rebind_repo,omitempty" flag:"rebind-repo" desc:"move this row to repo <mount> (rebinds repo_id)"`
	RebindMaster bool     `json:"rebind_master,omitempty" flag:"rebind-master" desc:"move this row to master scope (clears repo_id)"`
	Tags         []string `json:"tags,omitempty" flag:"tag" desc:"add this tag (repeatable; the tag must exist)"`
	Untags       []string `json:"untags,omitempty" flag:"untag" desc:"remove this tag (repeatable)"`
	Project      string   `json:"project,omitempty" flag:"project" desc:"accepted for v0.1.17 scripts; the id already names its project"`
	Scope        string   `json:"scope,omitempty" flag:"scope" desc:"move to another scope: project | repo | dir:<path>"`
	// Ref / Relation: knowledge_refs pointers (RES 3.11, RQ7, OQ22).
	Ref      []string `json:"ref,omitempty" flag:"ref" desc:"add a pointer to another row (<id> or <project>:<id>) or a commit (commit:<sha>); repeatable"`
	Relation string   `json:"relation,omitempty" flag:"relation" desc:"the relation each --ref records (snake_case; default mentions)"`
}

// SearchIn is the input of decision.search.
type SearchIn struct {
	Query           string   `json:"query" arg:"0" desc:"full-text query: words, \"exact phrase\", prefix*, AND / OR / NOT"`
	Columns         []string `json:"columns,omitempty" flag:"column" desc:"restrict MATCH to these full-text fields (repeatable)"`
	Repo            string   `json:"repo,omitempty" flag:"repo" desc:"scope to a specific repo (mount_name or rep_id)"`
	NoInherit       bool     `json:"no_inherit,omitempty" flag:"no-inherit" desc:"strict scope: --repo only, no master inheritance"`
	MasterOnly      bool     `json:"master_only,omitempty" flag:"master-only" desc:"only master-scoped rows (repo_id IS NULL)"`
	AllRepos        bool     `json:"all_repos,omitempty" flag:"all-repos" desc:"search across every repo (ignore --repo)"`
	Project         string   `json:"project,omitempty" flag:"project" desc:"override project (id or name)"`
	Limit           int      `json:"limit,omitempty" flag:"limit" desc:"max hits to return (default 20)"`
	IncludeArchived bool     `json:"include_archived,omitempty" flag:"include-archived" desc:"include archived rows"`
}

// SearchOut is v0.1.17's search envelope.
type SearchOut struct {
	SchemaVersion int         `json:"schema_version"`
	Kind          string      `json:"kind"`
	Query         string      `json:"query"`
	Count         int         `json:"count"`
	Data          []SearchHit `json:"data"`
}

// Text prints one line per hit, or "(no matches)".
func (o SearchOut) Text() string {
	if len(o.Data) == 0 {
		return "(no matches)\n"
	}
	var b strings.Builder
	for _, h := range o.Data {
		fmt.Fprintf(&b, "%s [%s] %s\n", h.ID, h.Row.Status, h.Row.Title)
	}
	return b.String()
}

// SearchHit is one v0.1.17 decision search hit.
type SearchHit struct {
	ID      string          `json:"id"`
	Score   float64         `json:"score"`
	Snippet string          `json:"snippet,omitempty"`
	Row     HitRow          `json:"row"`
	Store   store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
}

// HitRow is the row map v0.1.17 printed (keys sorted).
type HitRow struct {
	Body       string `json:"body"`
	SourceKind string `json:"source_kind"`
	Status     string `json:"status"`
	Title      string `json:"title"`
}

// ops binds the run functions to one Env.
type ops struct{ env kn.Env }

// add creates one decision. With --supersedes the replaced decision's
// status becomes superseded (v0.1.17 setSupersededBy).
func (o ops) add(ctx context.Context, in AddIn) (AddOut, error) {
	if err := kn.Required(noun, "title", in.Title); err != nil {
		return AddOut{}, err
	}
	body, err := kn.Body(noun, in.Body, in.Text)
	if err != nil {
		return AddOut{}, err
	}
	status := kn.Or(in.Status, schema.DecisionStatusAccepted)
	if err = kn.OneOf(noun, "status", status, schema.DecisionStatuses()); err != nil {
		return AddOut{}, err
	}
	superseded, err := value.TextOf(schema.DecisionStatusSuperseded)
	if err != nil {
		return AddOut{}, err
	}
	// --ref: parsed and F15-checked before anything is written (RES 3.11).
	rel, err := rowref.Relation(in.Relation, nil)
	if err != nil {
		return AddOut{}, err
	}
	refs, err := rowref.Parse(ctx, o.env, in.Ref)
	if err != nil {
		return AddOut{}, err
	}
	if err = rowref.Check(ctx, o.env, store.Source(in.To), refs); err != nil {
		return AddOut{}, err
	}
	c := kn.Create{
		To: in.To, Project: in.Project, Repo: in.Repo, Supersedes: in.Supersedes, CreatedBy: in.CreatedBy, Tags: in.Tags,
		Meta: in.MetaFlags, NoMeta: in.NoMeta,
		Texts: map[string]string{
			kn.FieldTitle: in.Title, kn.FieldBody: body, fieldStatus: status,
			kn.FieldSourceKind: in.Source, kn.FieldSourceRef: in.SourceRef,
		},
		SupersededPatch: map[string]value.Value{fieldStatus: superseded},
	}
	// 6.2.3 / D10: --scope picks the store, repo and folder.
	_, sw, err := knscope.ForAdd(ctx, o.env, noun, in.Scope, "", &c)
	if err != nil {
		return AddOut{}, err
	}
	src, w, carried, err := kn.Add(ctx, o.env, noun, c)
	if err != nil {
		return AddOut{}, err
	}
	if err = rowref.Save(ctx, o.env, src, noun.Table, kn.Field(w.Row, kn.FieldProject), w.Row.ID, rel, refs); err != nil {
		return AddOut{}, err
	}
	return AddOut{ID: w.Row.ID.String(), Store: string(src), CarriedMeta: carried, Title: in.Title, Warnings: append(kn.Warnings(w), sw...), label: kn.Label(kn.LabelerOf(o.env), src)}, nil
}

// addRefs writes --ref pointers of an existing decision it (parse, F15
// check against its store, save).
func addRefs(ctx context.Context, env kn.Env, it kn.Item, raw []string, relation string) error {
	rel, err := rowref.Relation(relation, nil)
	if err != nil {
		return err
	}
	refs, err := rowref.Parse(ctx, env, raw)
	if err != nil {
		return err
	}
	if err = rowref.Check(ctx, env, it.Source, refs); err != nil {
		return err
	}
	return rowref.Save(ctx, env, it.Source, noun.Table, kn.Field(it.Row, kn.FieldProject), it.Row.ID, rel, refs)
}

// list returns the decisions in scope (archived included, as v0.1.17).
func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	ctx = in.Deleted(ctx, noun.Table) // DELETION 3.2: --include-deleted / --deleted-only
	keep, err := kn.HasTags(ctx, o.env, in.Tags)
	if err != nil {
		return ListOut{}, err
	}
	items, err := kn.List(ctx, o.env, noun, kn.Scope{Repo: in.Repo, NoInherit: in.NoInherit, MasterOnly: in.MasterOnly, AllRepos: in.AllRepos, Project: in.Project},
		true, store.Source(in.Store), keep)
	if err != nil {
		return ListOut{}, err
	}
	items, notes, err := kn.Narrow(ctx, o.env, items, in.MetaFlags)
	if err != nil {
		return ListOut{}, err
	}
	out := ListOut{SchemaVersion: SchemaVersion, Kind: KindList, Data: []Record{}, Notes: notes}
	for _, it := range items {
		out.Data = append(out.Data, recordOf(kn.LabelerOf(o.env), it.Source, it.Row))
	}
	out.Count = len(out.Data)
	return out, nil
}

// show returns one decision.
func (o ops) show(ctx context.Context, in IDIn) (ShowOut, error) {
	it, err := kn.Show(ctx, o.env, noun, in.ID)
	if err != nil {
		return ShowOut{}, err
	}
	refs, err := rowref.List(ctx, o.env, noun.Table, it.Row.ID, nil)
	if err != nil {
		return ShowOut{}, err
	}
	return ShowOut{SchemaVersion: SchemaVersion, Kind: KindShow, Data: recordOf(kn.LabelerOf(o.env), it.Source, it.Row), Refs: refs}, nil
}

// edit patches the given fields.
func (o ops) edit(ctx context.Context, in EditIn) (WriteOut, error) {
	if err := kn.OneOf(noun, "status", in.Status, schema.DecisionStatuses()); err != nil {
		return WriteOut{}, err
	}
	// --ref (RES 3.11): checked against the row's store, then written; an
	// edit that only adds refs patches nothing else.
	if len(in.Ref) > 0 {
		it, err := kn.Show(ctx, o.env, noun, in.ID)
		if err != nil {
			return WriteOut{}, err
		}
		if err = addRefs(ctx, o.env, it, in.Ref, in.Relation); err != nil {
			return WriteOut{}, err
		}
		if in.Title == "" && in.Body == "" && in.Status == "" && in.SourceRef == "" && in.RebindRepo == "" && !in.RebindMaster &&
			len(in.Tags) == 0 && len(in.Untags) == 0 && in.Scope == "" && len(in.Meta) == 0 && len(in.MetaJSON) == 0 && in.MetaStdin == "" {
			return WriteOut{ID: it.Row.ID.String(), Store: string(it.Source), done: "updated"}, nil
		}
	}
	e := kn.Edit{
		Texts: map[string]string{
			kn.FieldTitle: in.Title, kn.FieldBody: in.Body, fieldStatus: in.Status, kn.FieldSourceRef: in.SourceRef,
		},
		RebindRepo: in.RebindRepo, RebindMaster: in.RebindMaster, Tags: in.Tags, Untags: in.Untags,
		Hint: "give at least one of --title, --body, --status, --source-ref, --scope, --rebind-repo, --rebind-master, --tag, --untag",
	}
	plan, err := knscope.ForEdit(ctx, o.env, noun, in.ID, in.Scope, "", &e)
	if err != nil {
		return WriteOut{}, err
	}
	src, w, err := plan.Run(ctx, o.env, noun, in.ID, func() (store.Source, engine.Written, error) { return kn.Apply(ctx, o.env, noun, in.ID, e) })
	if err != nil {
		return WriteOut{}, err
	}
	return WriteOut{ID: w.Row.ID.String(), Store: string(src), Warnings: append(kn.Warnings(w), plan.Warnings...), done: "updated"}, nil
}

// archive sets archived_at.
func (o ops) archive(ctx context.Context, in IDIn) (WriteOut, error) {
	return o.stamp(ctx, in.ID, kn.Now(o.env), "archived")
}

// unarchive clears archived_at.
func (o ops) unarchive(ctx context.Context, in IDIn) (WriteOut, error) {
	return o.stamp(ctx, in.ID, value.Null{}, "unarchived")
}

// stamp sets archived_at to v.
func (o ops) stamp(ctx context.Context, id string, v value.Value, done string) (WriteOut, error) {
	w, err := kn.Stamp(ctx, o.env, noun, id, kn.FieldArchivedAt, v)
	if err != nil {
		return WriteOut{}, err
	}
	return WriteOut{ID: w.Row.ID.String(), Warnings: kn.Warnings(w), done: done}, nil
}

// delete writes the decision's tombstone.
func (o ops) delete(ctx context.Context, in DeleteIn) (WriteOut, error) {
	ctx = engine.WithDeleteReason(ctx, in.Reason) // DELETION 2.1 --reason; E_REASON_REQUIRED under delete.require_reason
	w, err := kn.Delete(ctx, o.env, noun, in.ID)
	if err != nil {
		return WriteOut{}, err
	}
	return WriteOut{ID: w.Row.ID.String(), trashed: true}, nil
}

// search runs the full-text search.
func (o ops) search(ctx context.Context, in SearchIn) (SearchOut, error) {
	found, err := kn.Search(ctx, o.env, noun, in.Query, in.Columns, in.Limit,
		kn.Scope{Repo: in.Repo, NoInherit: in.NoInherit, MasterOnly: in.MasterOnly, AllRepos: in.AllRepos, Project: in.Project}, in.IncludeArchived)
	if err != nil {
		return SearchOut{}, err
	}
	out := SearchOut{SchemaVersion: SchemaVersion, Kind: KindSearch, Query: in.Query}
	for _, f := range found {
		r := f.Row
		out.Data = append(out.Data, SearchHit{
			ID: r.ID.String(), Score: f.Hit.Score, Snippet: f.Hit.Snippet, Store: kn.SourceOf(kn.LabelerOf(o.env), f.Source), Label: kn.Label(kn.LabelerOf(o.env), f.Source),
			Row: HitRow{Body: kn.Field(r, kn.FieldBody), SourceKind: kn.Field(r, kn.FieldSourceKind), Status: kn.Field(r, fieldStatus), Title: kn.Field(r, kn.FieldTitle)},
		})
	}
	out.Count = len(out.Data)
	return out, nil
}

// recordOf is the v0.1.17 --json form of a decision row.
func recordOf(lab kn.Labeler, src store.Source, r row.Row) Record {
	return Record{
		ID: r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		TrustScore: kn.Number(r, kn.FieldTrust), Confidence: kn.Number(r, kn.FieldConfidence),
		LastValidatedAt: kn.Time(r, kn.FieldValidated), ArchivedAt: kn.Time(r, kn.FieldArchivedAt),
		SourceKind: kn.Field(r, kn.FieldSourceKind), SourceRef: kn.Field(r, kn.FieldSourceRef),
		ProjectID: kn.Field(r, kn.FieldProject), RepoID: kn.Field(r, kn.FieldRepo),
		Title: kn.Field(r, kn.FieldTitle), Body: kn.Field(r, kn.FieldBody), Status: kn.Field(r, fieldStatus),
		SupersededByID: kn.Field(r, kn.FieldSuperseded), ScopeID: kn.Field(r, schema.FieldScope), CreatedByActorID: kn.CreatedBy(r),
		TagIDs: kn.TagsOf(r), Store: kn.SourceOf(lab, src), Label: kn.Label(lab, src),
	}
}
