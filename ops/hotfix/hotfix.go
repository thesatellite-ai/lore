// Package hotfix defines the `hotfix` noun's operations (loud recurring
// warnings, pinned in render and never truncated), layer L5: add, list, show, edit, archive, unarchive,
// delete and search, with v0.1.17's flag spellings, --json envelope and
// human output. The row work is shared with rule and hotfix in
// ops/internal/knowledge.
package hotfix

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	kn "github.com/thesatellite-ai/lore/ops/internal/knowledge"
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
			Examples: []op.Example{{Title: "warn about a trap", CLI: `lore hotfix add --title="Flaky CI" --body="Rerun the job once" --severity=high`, Want: "hfx_"}},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List hotfixes",
			Permission: op.PermRead, Access: op.AccessRead, Errors: kn.ReadErrors,
		}, o.list),
		op.Define(op.Spec{
			Name: OpShow, Summary: "Show hotfix details",
			Permission: op.PermRead, Access: op.AccessRead, Errors: kn.ReadErrors,
		}, o.show),
		op.Define(op.Spec{
			Name: OpEdit, Summary: "Edit hotfix fields (only the flags given change)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: kn.WriteErrors,
			Confirm: op.ConfirmRule{MayAsk: true},
		}, o.edit),
		op.Define(op.Spec{
			Name: OpArchive, Summary: "Archive a hotfix (hidden from render and search; kept)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: kn.WriteErrors,
		}, o.archive),
		op.Define(op.Spec{
			Name: OpUnarchive, Summary: "Bring an archived hotfix back",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: kn.WriteErrors,
		}, o.unarchive),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a hotfix (a tombstone is kept so old copies never return)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: kn.DeleteErrors(),
			Confirm: op.ConfirmRule{Always: true},
		}, o.delete),
		op.Define(op.Spec{
			Name: OpSearch, Summary: "Full-text search over hotfixes of every attached store",
			Permission: op.PermRead, Access: op.AccessRead, Errors: kn.SearchErrors,
		}, o.search),
	}
}

// AddIn is the input of hotfix.add.
type AddIn struct {
	Title      string   `json:"title" flag:"title" desc:"short headline (required)"`
	Body       string   `json:"body,omitempty" flag:"body" desc:"body (required; or give it as the first argument)" ui:"widget=markdown"`
	Text       string   `json:"text,omitempty" arg:"0" desc:"the hotfix body, instead of --body"`
	Severity   string   `json:"severity,omitempty" flag:"severity" desc:"low | medium | high | critical (default high)"`
	Source     string   `json:"source,omitempty" flag:"source" desc:"source_kind (default manual)"`
	Supersedes string   `json:"supersedes,omitempty" flag:"supersedes" desc:"hotfix_id (hfx_*) this entry replaces"`
	CreatedBy  string   `json:"created_by,omitempty" flag:"created-by" desc:"actor_id (act_*); defaults to current identity"`
	To         string   `json:"to,omitempty" flag:"to" desc:"store to write to (a name from .lore/stores.yaml, me, <store>/<folder>; default repo)"`
	Repo       string   `json:"repo,omitempty" flag:"repo" desc:"scope to a specific repo (mount_name or rep_id)"`
	Project    string   `json:"project,omitempty" flag:"project" desc:"override project (id or name)"`
	Tags       []string `json:"tags,omitempty" flag:"tag" desc:"tag name (repeatable; the tag must exist)"`
}

// AddOut is the output of hotfix.add.
type AddOut struct {
	ID       string       `json:"id"`
	Store    string       `json:"store"`
	Title    string       `json:"title"`
	Severity string       `json:"severity"`
	Warnings []kn.Warning `json:"warnings,omitempty"`
}

// Text prints v0.1.17's add line.
func (o AddOut) Text() string {
	return fmt.Sprintf("⚠ %s — %s [%s]%s\n", o.ID, o.Title, o.Severity, kn.StoreLabel(o.Store))
}

// WriteOut is the output of edit, archive, unarchive and delete.
type WriteOut struct {
	ID       string       `json:"id"`
	Store    string       `json:"store"`
	Warnings []kn.Warning `json:"warnings,omitempty"`
	done     string
}

// Text prints v0.1.17's "✓ <id> <verb>".
func (o WriteOut) Text() string { return "✓ " + o.ID + " " + o.done + "\n" }

// ListIn is the input of hotfix.list. v0.1.17 listed archived
// hotfixes too (no --archived flag); so does this.
type ListIn struct {
	Repo       string   `json:"repo,omitempty" flag:"repo" desc:"scope to a specific repo (mount_name or rep_id)"`
	NoInherit  bool     `json:"no_inherit,omitempty" flag:"no-inherit" desc:"strict scope: --repo only, no master inheritance"`
	MasterOnly bool     `json:"master_only,omitempty" flag:"master-only" desc:"only master-scoped rows (repo_id IS NULL)"`
	AllRepos   bool     `json:"all_repos,omitempty" flag:"all-repos" desc:"list across every repo (ignore --repo)"`
	Project    string   `json:"project,omitempty" flag:"project" desc:"override project (id or name)"`
	Tags       []string `json:"tags,omitempty" flag:"tag" desc:"only hotfixes with this tag (repeatable: all must match)"`
	Store      string   `json:"store,omitempty" flag:"store" desc:"only hotfixes of this store (default every attached store)"`
}

// ListOut is v0.1.17's list envelope.
type ListOut struct {
	SchemaVersion int      `json:"schema_version"`
	Kind          string   `json:"kind"`
	Count         int      `json:"count,omitempty"`
	Data          []Record `json:"data"`
}

// Text prints one line per hotfix, or "(no hotfixes)".
func (o ListOut) Text() string {
	if len(o.Data) == 0 {
		return "(no hotfixes)\n"
	}
	var b strings.Builder
	for _, r := range o.Data {
		fmt.Fprintf(&b, "%s [%s] %s%s\n", r.ID, r.Severity, kn.Cut(r.Body, kn.ListCut), kn.StoreLabel(r.Store))
	}
	return b.String()
}

// Record is one hotfix in v0.1.17's --json form (ent Hotfix JSON:
// same keys, same order).
type Record struct {
	ID               string    `json:"id,omitempty"`
	CreatedAt        time.Time `json:"created_at,omitempty"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
	TrustScore       float64   `json:"trust_score,omitempty"`
	Confidence       float64   `json:"confidence,omitempty"`
	LastValidatedAt  time.Time `json:"last_validated_at,omitempty,omitzero"`
	ArchivedAt       time.Time `json:"archived_at,omitempty,omitzero"`
	SourceKind       string    `json:"source_kind,omitempty"`
	SourceRef        string    `json:"source_ref,omitempty"`
	ProjectID        string    `json:"project_id,omitempty"`
	RepoID           string    `json:"repo_id,omitempty"`
	Title            string    `json:"title,omitempty"`
	Body             string    `json:"body,omitempty"`
	Severity         string    `json:"severity,omitempty"`
	SupersededByID   string    `json:"superseded_by_id,omitempty"`
	CreatedByActorID string    `json:"created_by_actor_id,omitempty"`
	TagIDs           []string  `json:"tag_ids,omitempty"`
	Store            string    `json:"_source,omitempty"`
}

// IDIn is the input of the operations that take one hotfix id.
type IDIn struct {
	ID      string `json:"id" arg:"0" desc:"the hotfix id (hfx_…)"`
	Project string `json:"project,omitempty" flag:"project" desc:"accepted for v0.1.17 scripts; the id already names its project"`
}

// ShowOut is v0.1.17's show envelope.
type ShowOut struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Data          Record `json:"data"`
}

// Text prints v0.1.17's show block.
func (o ShowOut) Text() string {
	return fmt.Sprintf("%s\n  severity: %s\n\n%s\n", o.Data.ID, o.Data.Severity, o.Data.Body)
}

// EditIn is the input of hotfix.edit; an empty flag leaves its field.
type EditIn struct {
	ID           string   `json:"id" arg:"0" desc:"the hotfix id (hfx_…)"`
	Title        string   `json:"title,omitempty" flag:"title" desc:"new title"`
	Body         string   `json:"body,omitempty" flag:"body" desc:"new body" ui:"widget=markdown"`
	Severity     string   `json:"severity,omitempty" flag:"severity" desc:"low | medium | high | critical"`
	RebindRepo   string   `json:"rebind_repo,omitempty" flag:"rebind-repo" desc:"move this row to repo <mount> (rebinds repo_id)"`
	RebindMaster bool     `json:"rebind_master,omitempty" flag:"rebind-master" desc:"move this row to master scope (clears repo_id)"`
	Tags         []string `json:"tags,omitempty" flag:"tag" desc:"add this tag (repeatable; the tag must exist)"`
	Untags       []string `json:"untags,omitempty" flag:"untag" desc:"remove this tag (repeatable)"`
	Project      string   `json:"project,omitempty" flag:"project" desc:"accepted for v0.1.17 scripts; the id already names its project"`
}

// SearchIn is the input of hotfix.search.
type SearchIn struct {
	Query           string `json:"query" arg:"0" desc:"full-text query: words, \"exact phrase\", prefix*, AND / OR / NOT"`
	Repo            string `json:"repo,omitempty" flag:"repo" desc:"scope to a specific repo (mount_name or rep_id)"`
	NoInherit       bool   `json:"no_inherit,omitempty" flag:"no-inherit" desc:"strict scope: --repo only, no master inheritance"`
	MasterOnly      bool   `json:"master_only,omitempty" flag:"master-only" desc:"only master-scoped rows (repo_id IS NULL)"`
	AllRepos        bool   `json:"all_repos,omitempty" flag:"all-repos" desc:"search across every repo (ignore --repo)"`
	Project         string `json:"project,omitempty" flag:"project" desc:"override project (id or name)"`
	Limit           int    `json:"limit,omitempty" flag:"limit" desc:"max hits to return (default 20)"`
	IncludeArchived bool   `json:"include_archived,omitempty" flag:"include-archived" desc:"include archived rows"`
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
	ID      string  `json:"id"`
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet,omitempty"`
	Row     HitRow  `json:"row"`
	Store   string  `json:"_source,omitempty"`
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
	if err := kn.OneOf(noun, "severity", severity, schema.HotfixSeverities()); err != nil {
		return AddOut{}, err
	}
	src, w, err := kn.Add(ctx, o.env, noun, kn.Create{
		To: in.To, Project: in.Project, Repo: in.Repo, Supersedes: in.Supersedes, CreatedBy: in.CreatedBy, Tags: in.Tags,
		Texts: map[string]string{
			kn.FieldTitle: in.Title, kn.FieldBody: body, fieldSeverity: severity, kn.FieldSourceKind: in.Source,
		},
	})
	if err != nil {
		return AddOut{}, err
	}
	return AddOut{ID: w.Row.ID.String(), Store: string(src), Title: in.Title, Severity: severity, Warnings: kn.Warnings(w)}, nil
}

// list returns the hotfixes in scope (archived included, as v0.1.17).
func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	items, err := kn.List(ctx, o.env, noun, kn.Scope{Repo: in.Repo, NoInherit: in.NoInherit, MasterOnly: in.MasterOnly, AllRepos: in.AllRepos, Project: in.Project},
		true, store.Source(in.Store), kn.HasTags(in.Tags))
	if err != nil {
		return ListOut{}, err
	}
	out := ListOut{SchemaVersion: SchemaVersion, Kind: KindList, Data: []Record{}}
	for _, it := range items {
		out.Data = append(out.Data, recordOf(it.Source, it.Row))
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
	return ShowOut{SchemaVersion: SchemaVersion, Kind: KindShow, Data: recordOf(it.Source, it.Row)}, nil
}

// edit patches the given fields.
func (o ops) edit(ctx context.Context, in EditIn) (WriteOut, error) {
	if err := kn.OneOf(noun, "severity", in.Severity, schema.HotfixSeverities()); err != nil {
		return WriteOut{}, err
	}
	src, w, err := kn.Apply(ctx, o.env, noun, in.ID, kn.Edit{
		Texts: map[string]string{
			kn.FieldTitle: in.Title, kn.FieldBody: in.Body, fieldSeverity: in.Severity,
		},
		RebindRepo: in.RebindRepo, RebindMaster: in.RebindMaster, Tags: in.Tags, Untags: in.Untags,
		Hint: "give at least one of --title, --body, --severity, --rebind-repo, --rebind-master, --tag, --untag",
	})
	if err != nil {
		return WriteOut{}, err
	}
	return WriteOut{ID: w.Row.ID.String(), Store: string(src), Warnings: kn.Warnings(w), done: "updated"}, nil
}

// archive sets archived_at.
func (o ops) archive(ctx context.Context, in IDIn) (WriteOut, error) {
	return o.stamp(ctx, in.ID, kn.Now(), "archived")
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

// delete writes the hotfix's tombstone.
func (o ops) delete(ctx context.Context, in IDIn) (WriteOut, error) {
	w, err := kn.Delete(ctx, o.env, noun, in.ID)
	if err != nil {
		return WriteOut{}, err
	}
	return WriteOut{ID: w.Row.ID.String(), done: "deleted (hard)"}, nil
}

// search runs the full-text search.
func (o ops) search(ctx context.Context, in SearchIn) (SearchOut, error) {
	found, err := kn.Search(ctx, o.env, noun, in.Query, in.Limit,
		kn.Scope{Repo: in.Repo, NoInherit: in.NoInherit, MasterOnly: in.MasterOnly, AllRepos: in.AllRepos, Project: in.Project}, in.IncludeArchived)
	if err != nil {
		return SearchOut{}, err
	}
	out := SearchOut{SchemaVersion: SchemaVersion, Kind: KindSearch, Query: in.Query}
	for _, f := range found {
		r := f.Row
		out.Data = append(out.Data, SearchHit{
			ID: r.ID.String(), Score: f.Hit.Score, Snippet: f.Hit.Snippet, Store: kn.SourceKey(f.Source),
			Row: HitRow{Body: kn.Field(r, kn.FieldBody), Severity: kn.Field(r, fieldSeverity), Title: kn.Field(r, kn.FieldTitle)},
		})
	}
	out.Count = len(out.Data)
	return out, nil
}

// recordOf is the v0.1.17 --json form of a hotfix row.
func recordOf(src store.Source, r row.Row) Record {
	return Record{
		ID: r.ID.String(), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		TrustScore: kn.Number(r, kn.FieldTrust), Confidence: kn.Number(r, kn.FieldConfidence),
		LastValidatedAt: kn.Time(r, kn.FieldValidated), ArchivedAt: kn.Time(r, kn.FieldArchivedAt),
		SourceKind: kn.Field(r, kn.FieldSourceKind), SourceRef: kn.Field(r, kn.FieldSourceRef),
		ProjectID: kn.Field(r, kn.FieldProject), RepoID: kn.Field(r, kn.FieldRepo),
		Title: kn.Field(r, kn.FieldTitle), Body: kn.Field(r, kn.FieldBody), Severity: kn.Field(r, fieldSeverity),
		SupersededByID: kn.Field(r, kn.FieldSuperseded), CreatedByActorID: kn.CreatedBy(r),
		TagIDs: kn.TagsOf(r), Store: kn.SourceKey(src),
	}
}
