// Package hotfix defines the `hotfix` noun's operations (loud recurring
// warnings, pinned in render and never truncated), layer L5: add, list, show, edit, archive, unarchive,
// delete and search, with v0.1.17's flag spellings, --json envelope and
// human output. The row work is shared with rule and hotfix in
// ops/internal/knowledge.
package hotfix

import (
	"context"
	"fmt"
	"github.com/thesatellite-ai/lore/ops/internal/auditrec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/contextfit"
	"github.com/thesatellite-ai/lore/logic/refpoint"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/internal/expand"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"github.com/thesatellite-ai/lore/ops/internal/trashline"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	kn "github.com/thesatellite-ai/lore/ops/internal/knowledge"
	"github.com/thesatellite-ai/lore/ops/internal/knscope"
	"github.com/thesatellite-ai/lore/ops/internal/metashow"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (hotfix.add → `lore hotfix add`, POST /v1/hotfixes).
const (
	Noun        = "hotfix"
	OpAdd       = "hotfix.add"
	OpList      = "hotfix.list"
	OpShow      = "hotfix.show"
	OpEdit      = "hotfix.edit"
	OpArchive   = "hotfix.archive"
	OpUnarchive = "hotfix.unarchive"
	OpDelete    = "hotfix.delete"
	OpSearch    = "hotfix.search"
)

// Envelope kinds (v0.1.17 constants.KindHotfix*).
const (
	KindList   = "hotfix.list"
	KindShow   = "hotfix.show"
	KindSearch = "hotfix.search"
)

// SchemaVersion is v0.1.17's envelope schema_version.
const SchemaVersion = 1

// fieldSeverity is the hotfix severity field.
const fieldSeverity = "severity"

// noun is the hotfixes table.
var noun = kn.Noun{Name: Noun, Table: schema.TableHotfixes, Prefix: ids.PrefixHotfix}

// Ops returns every hotfix operation bound to env, for op.NewRegistry.
func Ops(env kn.Env) []op.Entry {
	o := ops{env: env}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Add a new hotfix (a loud recurring warning: we keep hitting this, beware)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: kn.WriteErrors,
			Confirm:  op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "warn about a trap", CLI: `lore hotfix add --title "Flaky CI" --body "Rerun the job once" --severity high`, Want: "hfx_"}},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List hotfixes",
			Examples:   []op.Example{{Title: "list as JSON", CLI: "lore hotfix list --json", Want: `"data"`}},
			Permission: op.PermRead, Access: op.AccessRead, Errors: kn.ReadErrors,
		}, o.list),
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show hotfix details",
			Examples:   []op.Example{{Title: "show one as JSON", CLI: "lore hotfix show --id hfx_<id> --json", Want: `"data"`}},
			Permission: op.PermRead, Access: op.AccessRead, Errors: expand.RevealErrors(kn.ReadErrors), Confirm: expand.RevealConfirm,
		}, o.showExpand),
		op.Define(op.Spec{
			Name: OpEdit, Summary: "Edit hotfix fields (only the flags given change)",
			Examples:   []op.Example{{Title: "change the body", CLI: `lore hotfix edit --id hfx_<id> --body "the new text"`, Want: "hfx_"}},
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: kn.WriteErrors,
			Confirm: op.ConfirmRule{MayAsk: true},
		}, o.edit),
		op.Define(op.Spec{
			Name: OpArchive, Summary: "Archive a hotfix (hidden from render and search; kept)",
			Examples:   []op.Example{{Title: "archive one", CLI: "lore hotfix archive --id hfx_<id>", Want: "archived"}},
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: kn.WriteErrors,
		}, o.archive),
		op.Define(op.Spec{
			Name: OpUnarchive, Summary: "Bring an archived hotfix back",
			Examples:   []op.Example{{Title: "bring it back", CLI: "lore hotfix unarchive --id hfx_<id>", Want: "unarchived"}},
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: kn.WriteErrors,
		}, o.unarchive),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a hotfix (it goes to the trash; lore trash restore brings it back)",
			Examples:   []op.Example{{Title: "delete without a terminal", CLI: "lore hotfix delete --id hfx_<id> --yes", Want: "hfx_"}},
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: kn.DeleteErrors(),
			Confirm: op.ConfirmRule{Always: true},
		}, o.delete).Asks(func(ctx context.Context, in DeleteIn) (string, error) {
			return kn.DeleteQuestion(ctx, o.env, noun, in.ID)
		}),
		op.Define(op.Spec{
			Name: OpSearch, Summary: "Full-text search over hotfixes of every attached store",
			Examples:   []op.Example{{Title: "search as JSON", CLI: `lore hotfix search --query "retry" --json`, Want: `"data"`}},
			Permission: op.PermRead, Access: op.AccessRead, Errors: kn.SearchErrors,
		}, o.search),
	}
}

// AddIn is the input of hotfix.add.
type AddIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

	Title      string   `json:"title" flag:"title" desc:"short headline (required)"`
	Body       string   `json:"body,omitempty" flag:"body" desc:"body (required)" ui:"widget=markdown"`
	Summary    string   `json:"summary,omitempty" flag:"summary" desc:"one-line short form used when the full text does not fit LORE.md (up to 200 characters)"`
	Text       string   `json:"text,omitempty" arg:"0" argof:"body" desc:"the hotfix body, instead of --body"`
	Severity   string   `json:"severity,omitempty" flag:"severity" desc:"low | medium | high | critical (default high)"`
	Source     string   `json:"source,omitempty" flag:"source" desc:"source_kind (default manual)"`
	Supersedes string   `json:"supersedes,omitempty" flag:"supersedes" desc:"hotfix_id (hfx_*) this entry replaces"`
	NoMeta     bool     `json:"no_meta,omitempty" flag:"no-meta" desc:"with --supersedes: do not copy the replaced row's metadata"`
	To         string   `json:"to,omitempty" flag:"to" alias:"store" desc:"store to write to (a name from .lore/stores.yaml, me, <store>/<folder>; default repo)"`
	Repo       string   `json:"repo,omitempty" flag:"repo" desc:"scope to a specific repo (mount_name or rep_id)"`
	Project    string   `json:"project,omitempty" flag:"project" desc:"override project (id or name)"`
	Tags       []string `json:"tags,omitempty" flag:"tag" desc:"tag name (repeatable; the tag must exist)"`
	Scope      string   `json:"scope,omitempty" flag:"scope" desc:"where it attaches: global | project | repo | dir:<path> (default: today's project/repo)"`
	Overrides  string   `json:"overrides,omitempty" flag:"overrides" desc:"hotfix_id (hfx_*) of a wider scope this one replaces here"`
}

// AddOut is the output of hotfix.add.
type AddOut struct {
	ID       string       `json:"id"`
	Store    string       `json:"store"`
	Title    string       `json:"title"`
	Severity string       `json:"severity"`
	Warnings []kn.Warning `json:"warnings,omitempty"`
	// CarriedMeta is how many metadata keys --supersedes copied (3.19).
	CarriedMeta int `json:"carried_meta,omitempty"`
	// label is the store's `_label` for the add line (kn.Label; "" for
	// the repo): v0.1.17 printed route.Label, "[northwind/acme]".
	label string
}

// Text prints v0.1.17's add line.
func (o AddOut) Text() string {
	return fmt.Sprintf("⚠ %s — %s [%s]%s\n", o.ID, o.Title, o.Severity, kn.LabelSuffix(o.label)) + carriedLine(o.CarriedMeta) + kn.WarnText(o.Warnings)
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

// ListIn is the input of hotfix.list. v0.1.17 listed archived
// hotfixes too (no --archived flag); so does this.
type ListIn struct {
	Repo       string   `json:"repo,omitempty" flag:"repo" desc:"scope to a specific repo (mount_name or rep_id)"`
	NoInherit  bool     `json:"no_inherit,omitempty" flag:"no-inherit" desc:"strict scope: --repo only, no master inheritance"`
	MasterOnly bool     `json:"master_only,omitempty" flag:"master-only" desc:"only master-scoped rows (repo_id IS NULL)"`
	AllRepos   bool     `json:"all_repos,omitempty" flag:"all-repos" desc:"list across every repo (ignore --repo)"`
	All        bool     `json:"all,omitempty" flag:"all" desc:"every row: every repo's and the archived ones (the default list says how many it hides)"`
	Project    string   `json:"project,omitempty" flag:"project" desc:"override project (id or name)"`
	Tags       []string `json:"tags,omitempty" flag:"tag" desc:"only hotfixes with this tag (repeatable: all must match)"`
	Store      string   `json:"store,omitempty" flag:"store" alias:"to" desc:"only hotfixes of this store (default every attached store)"`
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

// Text prints one line per hotfix, or "(no hotfixes)".
func (o ListOut) Text() string { return o.rowsText() + kn.NotesText(o.Notes) }

// rowsText is the v0.1.17 lines.
func (o ListOut) rowsText() string {
	if len(o.Data) == 0 {
		return "(no hotfixes)\n"
	}
	var b strings.Builder
	for _, r := range o.Data {
		fmt.Fprintf(&b, "%s [%s] %s%s\n", r.ID, r.Severity, kn.Cut(r.Body, kn.ListCut), kn.LabelSuffix(r.Label))
	}
	return b.String()
}

// Record is one hotfix in v0.1.17's --json form (ent Hotfix JSON:
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
	Severity         string          `json:"severity,omitempty"`
	SupersededByID   string          `json:"superseded_by_id,omitempty"`
	ScopeID          string          `json:"scope_id,omitempty"`
	OverridesID      string          `json:"overrides_id,omitempty"`
	CreatedByActorID string          `json:"created_by_actor_id,omitempty"`
	TagIDs           []string        `json:"tag_ids,omitempty"`
	Store            store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
	// Meta / MetaMasked are the row's metadata by name, sensitive values
	// masked (MQ13, METADATA 3.17/3.20; absent when the row has none, so
	// v0.1.17's bytes stay for such rows).
	Meta       map[string]string `json:"meta,omitempty"`
	MetaMasked []string          `json:"meta_masked,omitempty"`
	// CreatedBy / UpdatedBy are the row envelope's writers (INTEG-AUDIT-1,
	// auditrec), last so the v0.1.17 keys keep their order.
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// IDIn is the input of the operations that take one hotfix id.
type IDIn struct {
	ID      string `json:"id" arg:"0" desc:"the hotfix id (hfx_…)"`
	Project string `json:"project,omitempty" flag:"project" desc:"accepted for v0.1.17 scripts; the id already names its project"`
}

// DeleteIn is the input of hotfix.delete.
type DeleteIn struct {
	ID      string `json:"id" arg:"0" desc:"the hotfix id (hfx_…)"`
	Project string `json:"project,omitempty" flag:"project" desc:"accepted for v0.1.17 scripts; the id already names its project"`
	Reason  string `json:"reason,omitempty" flag:"reason" desc:"why it is deleted (stored as deleted_reason; required when policy delete.require_reason is true)"`
	// PurgeFlags: --purge / --confirm (engine binds them, P3-DEL-1).
	op.PurgeFlags
}

// ShowOut is v0.1.17's show envelope.
type ShowOut struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Data          Record `json:"data"`
	// Expanded / Refs are --expand's additions (SECRETS 12: "--json adds
	// expanded and refs"); empty without --expand.
	Expanded map[string]string `json:"expanded,omitempty"`
	Refs     []refpoint.Used   `json:"refs,omitempty"`
	// Revealed, Forced and Warning: --reveal (SECRETS 2.7, SQ10).
	Revealed int    `json:"revealed,omitempty"`
	Forced   bool   `json:"forced,omitempty"`
	Warning  string `json:"warning,omitempty"`
}

// Text prints v0.1.17's show block.
func (o ShowOut) baseText() string {
	return fmt.Sprintf("%s\n  title:    %s\n  severity: %s\n\n%s\n", o.Data.ID, o.Data.Title, o.Data.Severity, o.Data.Body)
}

// EditIn is the input of hotfix.edit; an empty flag leaves its field.
type EditIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

	ID           string   `json:"id" arg:"0" desc:"the hotfix id (hfx_…)"`
	Title        string   `json:"title,omitempty" flag:"title" desc:"new title"`
	Body         string   `json:"body,omitempty" flag:"body" desc:"new body" ui:"widget=markdown"`
	Severity     string   `json:"severity,omitempty" flag:"severity" desc:"low | medium | high | critical"`
	Summary      string   `json:"summary,omitempty" flag:"summary" desc:"one-line short form used when the full text does not fit LORE.md (up to 200 characters)"`
	RebindRepo   string   `json:"rebind_repo,omitempty" flag:"rebind-repo" desc:"move this row to repo <mount> (rebinds repo_id)"`
	RebindMaster bool     `json:"rebind_master,omitempty" flag:"rebind-master" desc:"move this row to master scope (clears repo_id)"`
	Tags         []string `json:"tags,omitempty" flag:"tag" desc:"add this tag (repeatable; the tag must exist)"`
	Untags       []string `json:"untags,omitempty" flag:"untag" desc:"remove this tag (repeatable)"`
	Project      string   `json:"project,omitempty" flag:"project" desc:"accepted for v0.1.17 scripts; the id already names its project"`
	Scope        string   `json:"scope,omitempty" flag:"scope" desc:"move to another scope: project | repo | dir:<path>"`
	Overrides    string   `json:"overrides,omitempty" flag:"overrides" desc:"hotfix_id (hfx_*) of a wider scope this one replaces here (none = clear)"`
}

// SearchIn is the input of hotfix.search.
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
	Store           string   `json:"store,omitempty" flag:"store" alias:"to" desc:"only hotfixes of this store (default every attached store)"`
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
		fmt.Fprintf(&b, "%s [%s] %s\n", h.ID, h.Row.Severity, h.Row.Title)
	}
	return b.String()
}

// SearchHit is one v0.1.17 hotfix search hit.
type SearchHit struct {
	ID      string          `json:"id"`
	Score   float64         `json:"score"`
	Snippet string          `json:"snippet,omitempty"`
	Row     HitRow          `json:"row"`
	Store   store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
	// Meta / MetaMasked are the row's metadata by name, sensitive values
	// masked (MQ13, METADATA 3.17/3.20; absent when the row has none, so
	// v0.1.17's bytes stay for such rows).
	Meta       map[string]string `json:"meta,omitempty"`
	MetaMasked []string          `json:"meta_masked,omitempty"`
}

// HitRow is the row map v0.1.17 printed (keys sorted).
type HitRow struct {
	Body     string `json:"body"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
}

// ops binds the run functions to one Env.
type ops struct{ env kn.Env }

// add creates one hotfix.
func (o ops) add(ctx context.Context, in AddIn) (AddOut, error) {
	if err := kn.Required(noun, "title", in.Title); err != nil {
		return AddOut{}, err
	}
	body, err := kn.Body(noun, in.Body, in.Text)
	if err != nil {
		return AddOut{}, err
	}
	severity := kn.Or(in.Severity, schema.HotfixSeverityHigh)
	if err = kn.OneOf(noun, "severity", severity, schema.HotfixSeverities()); err != nil {
		return AddOut{}, err
	}
	summary, err := checkSummary(in.Summary)
	if err != nil {
		return AddOut{}, err
	}
	c := kn.Create{
		To: in.To, Project: in.Project, Repo: in.Repo, Supersedes: in.Supersedes, Tags: in.Tags,
		Meta: in.MetaFlags, NoMeta: in.NoMeta,
		Texts: map[string]string{
			kn.FieldTitle: in.Title, kn.FieldBody: body, fieldSeverity: severity, kn.FieldSourceKind: in.Source, schema.FieldSummary: summary,
		},
	}
	// 6.2.3 / D10: --scope picks the store, repo and folder.
	_, sw, err := knscope.ForAdd(ctx, o.env, noun, in.Scope, in.Overrides, &c)
	if err != nil {
		return AddOut{}, err
	}
	src, w, carried, err := kn.Add(ctx, o.env, noun, c)
	if err != nil {
		return AddOut{}, err
	}
	return AddOut{ID: w.Row.ID.String(), Store: string(src), CarriedMeta: carried, Title: in.Title, Severity: severity, Warnings: append(kn.Warnings(w), sw...), label: kn.Label(kn.LabelerOf(o.env), src)}, nil
}

// list returns the hotfixes in scope (archived included, as v0.1.17).
func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	ctx = in.Deleted(ctx, noun.Table) // DELETION 3.2: --include-deleted / --deleted-only
	if in.All {
		in.Archived = true // --all: the metadata narrowing keeps them too
	}
	keep, err := kn.HasTags(ctx, o.env, in.Tags)
	if err != nil {
		return ListOut{}, err
	}
	items, hid, err := kn.List(ctx, o.env, noun, kn.Scope{Repo: in.Repo, NoInherit: in.NoInherit, MasterOnly: in.MasterOnly, AllRepos: in.AllRepos, Project: in.Project, All: in.All},
		true, store.Source(in.Store), keep)
	if err != nil {
		return ListOut{}, err
	}
	items, notes, err := kn.Narrow(ctx, o.env, items, in.MetaFlags)
	if err != nil {
		return ListOut{}, err
	}
	out := ListOut{SchemaVersion: SchemaVersion, Kind: KindList, Data: []Record{}, Notes: kn.WithHidden(notes, hid)}
	for _, it := range items {
		out.Data = append(out.Data, withMeta(ctx, o.env, recordOf(kn.LabelerOf(o.env), it.Source, it.Row), it.Row))
	}
	out.Count = len(out.Data)
	return out, nil
}

// show returns one hotfix.
func (o ops) show(ctx context.Context, in IDIn) (ShowOut, error) {
	it, err := kn.Show(ctx, o.env, noun, in.ID)
	if err != nil {
		return ShowOut{}, err
	}
	return ShowOut{SchemaVersion: SchemaVersion, Kind: KindShow, Data: withMeta(ctx, o.env, recordOf(kn.LabelerOf(o.env), it.Source, it.Row), it.Row)}, nil
}

// edit patches the given fields.
func (o ops) edit(ctx context.Context, in EditIn) (WriteOut, error) {
	if err := kn.OneOf(noun, "severity", in.Severity, schema.HotfixSeverities()); err != nil {
		return WriteOut{}, err
	}
	summary, err := checkSummary(in.Summary)
	if err != nil {
		return WriteOut{}, err
	}
	e := kn.Edit{
		Texts: map[string]string{
			kn.FieldTitle: in.Title, kn.FieldBody: in.Body, fieldSeverity: in.Severity, schema.FieldSummary: summary,
		},
		RebindRepo: in.RebindRepo, RebindMaster: in.RebindMaster, Tags: in.Tags, Untags: in.Untags,
		Hint: "give at least one of --title, --body, --severity, --summary, --scope, --rebind-repo, --rebind-master, --tag, --untag",
	}
	plan, err := knscope.ForEdit(ctx, o.env, noun, in.ID, in.Scope, in.Overrides, &e)
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
	src, w, err := kn.Stamp(ctx, o.env, noun, id, kn.FieldArchivedAt, v)
	if err != nil {
		return WriteOut{}, err
	}
	return WriteOut{ID: w.Row.ID.String(), Store: string(src), Warnings: kn.Warnings(w), done: done}, nil
}

// delete writes the hotfix's tombstone.
func (o ops) delete(ctx context.Context, in DeleteIn) (WriteOut, error) {
	ctx = engine.WithDeleteReason(ctx, in.Reason) // DELETION 2.1 --reason; E_REASON_REQUIRED under delete.require_reason
	src, w, err := kn.Delete(ctx, o.env, noun, in.ID)
	if err != nil {
		return WriteOut{}, err
	}
	return WriteOut{ID: w.Row.ID.String(), Store: string(src), trashed: true}, nil
}

// search runs the full-text search.
func (o ops) search(ctx context.Context, in SearchIn) (SearchOut, error) {
	found, err := kn.Search(ctx, o.env, noun, in.Query, in.Columns, in.Limit,
		kn.Scope{Repo: in.Repo, NoInherit: in.NoInherit, MasterOnly: in.MasterOnly, AllRepos: in.AllRepos, Project: in.Project, Store: in.Store}, in.IncludeArchived)
	if err != nil {
		return SearchOut{}, err
	}
	out := SearchOut{SchemaVersion: SchemaVersion, Kind: KindSearch, Query: in.Query}
	for _, f := range found {
		r := f.Row
		out.Data = append(out.Data, SearchHit{
			ID: r.ID.String(), Score: f.Hit.Score, Snippet: f.Hit.Snippet, Store: kn.SourceOf(kn.LabelerOf(o.env), f.Source), Label: kn.Label(kn.LabelerOf(o.env), f.Source),
			Row: HitRow{Body: kn.Field(r, kn.FieldBody), Severity: kn.Field(r, fieldSeverity), Title: kn.Field(r, kn.FieldTitle)},
		})
		hm := metashow.Row(ctx, o.env, r) // MQ13: metadata on every read
		out.Data[len(out.Data)-1].Meta, out.Data[len(out.Data)-1].MetaMasked = hm.Meta, hm.MetaMasked
	}
	out.Count = len(out.Data)
	return out, nil
}

// recordOf is the v0.1.17 --json form of a hotfix row.
func recordOf(lab kn.Labeler, src store.Source, r row.Row) Record {
	return Record{
		ID: r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, CreatedBy: auditrec.Of(r).CreatedBy, UpdatedBy: auditrec.Of(r).UpdatedBy,
		TrustScore: kn.Number(r, kn.FieldTrust), Confidence: kn.Number(r, kn.FieldConfidence),
		LastValidatedAt: kn.Time(r, kn.FieldValidated), ArchivedAt: kn.Time(r, kn.FieldArchivedAt),
		SourceKind: kn.Field(r, kn.FieldSourceKind), SourceRef: kn.Field(r, kn.FieldSourceRef),
		ProjectID: kn.Field(r, kn.FieldProject), RepoID: kn.Field(r, kn.FieldRepo),
		Title: kn.Field(r, kn.FieldTitle), Body: kn.Field(r, kn.FieldBody), Severity: kn.Field(r, fieldSeverity),
		SupersededByID: kn.Field(r, kn.FieldSuperseded), ScopeID: kn.Field(r, schema.FieldScope), OverridesID: kn.Field(r, schema.FieldOverrides), CreatedByActorID: kn.CreatedBy(r),
		TagIDs: kn.TagsOf(r), Store: kn.SourceOf(lab, src), Label: kn.Label(lab, src),
	}
}

// checkSummary trims a --summary and refuses one longer than
// contextfit.MaxSummary characters (CONTEXT_BUDGET 6.4): the short form
// render uses when the full text does not fit LORE.md.
func checkSummary(s string) (string, error) {
	s = strings.TrimSpace(s)
	if n := utf8.RuneCountInString(s); n > contextfit.MaxSummary {
		return "", errs.New(errs.InvalidInput, fmt.Sprintf("%s: --summary is %d characters; the most is %d", Noun, n, contextfit.MaxSummary)).
			WithHint("keep it to one short line; the full text stays in the body")
	}
	return s, nil
}
