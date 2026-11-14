package sync

import "github.com/thesatellite-ai/lore/op"

// types.go — the inputs and outputs of the sync, land and explain
// operations. Field names and JSON keys are v0.1.17's (`lore sync … --json`),
// so a script reading the old output reads the new one unchanged.

// ---------------------------------------------------------------- inputs

// RunIn is the input of sync.run (`lore sync`): no flags of its own.
type RunIn struct{}

// StatusIn is the input of sync.status.
type StatusIn struct{}

// ConflictsIn is the input of sync.conflicts.
type ConflictsIn struct {
	All bool `json:"all,omitempty" flag:"all" desc:"include resolved conflicts"`
}

// ResolveIn is the input of sync.resolve.
type ResolveIn struct {
	ID   string `json:"id" arg:"0" desc:"the conflict number (see lore sync conflicts)"`
	Take string `json:"take" flag:"take" desc:"kept | other (required)"`
}

// TrashListIn is the input of sync.trash.list (old spelling of
// `lore trash --local`).
type TrashListIn struct {
	// --limit / --page: the shared paging flags of every list (op.PageFlags, T-4).
	op.PageFlags
}

// TrashRestoreIn is the input of sync.trash.restore.
type TrashRestoreIn struct {
	ID string `json:"id" arg:"0" desc:"the trash entry number (see lore trash --local)"`
}

// PurgeIn is the input of sync.purge (old spelling of
// `lore trash purge --archived-before`).
type PurgeIn struct {
	ArchivedBefore string `json:"archived_before,omitempty" flag:"archived-before" desc:"cutoff: date (2006-01-02) or age (90d, 720h)"`
	Confirm        bool   `json:"confirm,omitempty" flag:"confirm" desc:"required: purge is durable"`
	DryRun         bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"count what would be purged"`
}

// FixProjectsIn is the input of sync.fix_projects.
type FixProjectsIn struct {
	Keep   string `json:"keep,omitempty" flag:"keep" desc:"project id to keep (default: the one _meta.json names)"`
	DryRun bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"show what would change"`
}

// PeekIn is the input of sync.peek.
type PeekIn struct {
	Ref   string `json:"ref" arg:"0" desc:"a branch, tag or commit (origin/<branch> after git fetch)"`
	Table string `json:"table,omitempty" arg:"1" desc:"only this table (tasks, rules, …)"`
}

// PromoteIn is the input of sync.promote. The target branch is the json
// field "branch" behind the v0.1.17 flag --to: a field named "to" is the
// store target the wire layer resolves (engine.InputTarget), and a branch
// like feature/x would read as <store>/<folder> there.
type PromoteIn struct {
	IDs    []string `json:"ids" arg:"0" rest:"true" desc:"row ids to commit onto the branch"`
	Branch string   `json:"branch" flag:"to" alias:"branch" desc:"target branch (required)"`
	DryRun bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"show what would be committed"`
}

// MergeRowsIn is the input of sync.merge_rows.
type MergeRowsIn struct {
	Table string `json:"table" arg:"0" desc:"the table of both rows (memories, rules, …)"`
	Keep  string `json:"keep" flag:"keep" desc:"id of the row to keep"`
	Drop  string `json:"drop" flag:"drop" desc:"id of the duplicate to remove"`
}

// CIMergeIn is the input of sync.ci_merge.
type CIMergeIn struct {
	Base   string `json:"base" flag:"base" desc:"ref to merge in (e.g. origin/main)"`
	DryRun bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"report what would happen; change nothing"`
}

// InstallActionIn is the input of sync.install_action.
type InstallActionIn struct {
	Branches    []string `json:"branches,omitempty" flag:"branch" desc:"branch whose pushes trigger the workflow (repeatable; default: the remote's default branch)"`
	Auto        bool     `json:"auto,omitempty" flag:"auto" desc:"also run automatically when lore data changes on a pull request or a --branch (spends Actions time)"`
	Manual      bool     `json:"manual,omitempty" flag:"manual" desc:"run only when started by hand: already the default, kept for compatibility (switches an automatic workflow back)"`
	LoreVersion string   `json:"lore_version,omitempty" flag:"lore-version" desc:"lore release the workflow downloads: vX.Y.Z or latest (default: this lore's version, kept current)"`
	DryRun      bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"print the workflow; write nothing"`
	Force       bool     `json:"force,omitempty" flag:"force" desc:"overwrite a file at that path that lore did not generate"`
}

// LandIn is the input of land.run (`lore land`).
type LandIn struct {
	ID      string `json:"id,omitempty" arg:"0" desc:"the row to land"`
	All     bool   `json:"all,omitempty" flag:"all" desc:"every live row of this project"`
	Kind    string `json:"kind,omitempty" flag:"kind" desc:"only rows of this kind (tasks, rules, …)"`
	Mission string `json:"mission,omitempty" flag:"mission" desc:"only the mission and its tasks"`
	DryRun  bool   `json:"dry_run,omitempty" flag:"dry-run" desc:"print the plan, change nothing"`
}

// ExplainIn is the input of explain.run (`lore explain <id>`).
type ExplainIn struct {
	ID string `json:"id" arg:"0" desc:"the row id"`
}

// ---------------------------------------------------------------- outputs

// FileError is a row file the pass could not read or apply.
type FileError struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// ConflictNote is one conflict a pass met: which side it kept and why.
type ConflictNote struct {
	Path string `json:"path"`
	Kept string `json:"kept"`
	Why  string `json:"why"`
}

// StoreReport is one attached store's sync result (ops/stores.Report's
// shape, repeated here so the sync noun imports no other noun).
type StoreReport struct {
	Store    string   `json:"store"`
	Sent     int      `json:"sent"`
	Received int      `json:"received"`
	Merged   int      `json:"merged"`
	Refused  int      `json:"refused"`
	Problems []string `json:"problems,omitempty"`
	Offline  bool     `json:"offline,omitempty"`
	// HeldBy names the store folder whose refusal held this folder's push
	// and Held is the line that says so (ops/stores.HeldLine); empty when
	// the folder's push was its own.
	HeldBy string `json:"held_by,omitempty"`
	Held   string `json:"held,omitempty"`
	// Pushed is true when this sync pushed local commits of the store,
	// also ones that carry no row (a store link, the org file), which
	// Sent does not count.
	Pushed bool `json:"pushed,omitempty"`
	// URL, Status, Error and Next are set by `lore sync` run outside a
	// code repo (every store of the setup, or the store checkout it runs
	// in): the store's url, what happened (StoreStatus*), and for a store
	// that failed the cause and the next step. Inside a repo only URL and
	// Error are set, for an offline store (devco L-10: the cause was in no
	// field).
	URL    string `json:"url,omitempty"`
	Status string `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
	Next   string `json:"next,omitempty"`
}

// The status of one store in `lore sync` run outside a code repo.
const (
	StoreStatusUpToDate = "up to date"
	StoreStatusPulled   = "pulled"
	StoreStatusPushed   = "pushed"
	StoreStatusBoth     = "pulled and pushed"
	StoreStatusOffline  = "offline"
	StoreStatusFailed   = "failed"
)

// PassSetup and PassStore name the pass of `lore sync` run outside a code
// repo: every store of the setup, or the one store checkout it runs in.
const (
	PassSetup = "setup"
	PassStore = "store"
)

// RunData is the data of `lore sync --json`: the repo pass plus the stores.
type RunData struct {
	Pass           string         `json:"pass"`
	Imported       []string       `json:"imported,omitempty"`
	Exported       []string       `json:"exported,omitempty"`
	Removed        []string       `json:"removed_files,omitempty"`
	Deleted        []string       `json:"deleted_rows,omitempty"`
	Conflicts      []ConflictNote `json:"conflicts,omitempty"`
	Errors         []FileError    `json:"errors,omitempty"`
	Warnings       []string       `json:"warnings,omitempty"`
	Merged         []string       `json:"natural_key_merges,omitempty"`
	BackupPath     string         `json:"backup_path,omitempty"`
	PendingExports int            `json:"pending_exports,omitempty"`
	// LivePending counts live writes this sync could not push (offline, or
	// the remote refused refs/lore/*); they wait in the clone for the next
	// sync. 0 (omitted) when every write reached the remote.
	LivePending int `json:"live_pending,omitempty"`
	// LiveOffline and LiveError (devco L-10): the live pass could not
	// reach the repo's remote (its fetch or its push failed, after one
	// retry of a dropped connection); LiveError names the remote, the git
	// step and git's own reason, so a script can tell a failed sync from a
	// clean one without reading the text.
	LiveOffline bool          `json:"live_offline,omitempty"`
	LiveError   string        `json:"live_error,omitempty"`
	Stores      []StoreReport `json:"stores,omitempty"`
}

// RunOut is the output of sync.run (envelope kind sync.report).
type RunOut struct {
	SchemaVersion int      `json:"schema_version"`
	Kind          string   `json:"kind"`
	Data          RunData  `json:"data"`
	Lines         []string `json:"-"`
}

// Text implements op.Texter.
func (o RunOut) Text() string { return text(o.Lines) }

// StatusData is the data of `lore sync status --json`.
type StatusData struct {
	Baselined         bool        `json:"baselined"`
	DataDirPresent    bool        `json:"data_dir_present"`
	TrackedFiles      int         `json:"tracked_files"`
	PendingExports    int         `json:"pending_exports"`
	OpenConflicts     int         `json:"open_conflicts"`
	TrashRows         int         `json:"trash_rows"`
	Errors            []FileError `json:"errors,omitempty"`
	Uncommitted       []string    `json:"uncommitted,omitempty"`
	GitError          string      `json:"git_error,omitempty"`
	MetaQuarantined   []string    `json:"meta_quarantined,omitempty"`
	MetaRegistriesCap []string    `json:"meta_registries_over_cap,omitempty"`
	// StoreRefusals are the stores whose last push the remote refused
	// (the commits wait in the local clone; a later push that lands
	// clears the entry).
	StoreRefusals []StoreRefusal `json:"store_refusals,omitempty"`
	// StoreHeld are the store folders whose push waits on a refusal of
	// another folder of the same store (the clone pushes every folder at
	// once); the refusal itself is listed once, under StoreRefusals.
	StoreHeld []StoreHeld `json:"store_held,omitempty"`
	// Stores is where each store this repo and machine use stands against
	// its remote, read from the local copy (no network).
	Stores []StoreState `json:"stores,omitempty"`
}

// StoreState is one store's local copy against its remote.
type StoreState struct {
	Store string `json:"store"`
	// Kind is git or folder (a folder store has no remote).
	Kind string `json:"kind"`
	// Cloned is false for a git store with no copy on this machine.
	Cloned bool   `json:"cloned"`
	Branch string `json:"branch,omitempty"`
	// Ahead are the local commits the remote has not confirmed; Behind
	// the remote commits the last fetch saw that are not merged yet.
	Ahead  int `json:"ahead"`
	Behind int `json:"behind"`
	// OldestUnpushed and LastPush are RFC 3339 UTC ("" = none).
	OldestUnpushed string `json:"oldest_unpushed,omitempty"`
	LastPush       string `json:"last_push,omitempty"`
	// Error is why the state could not be read.
	Error string `json:"error,omitempty"`
	// Push is the store's push mode and PushSummary its settings in one
	// line (store push modes, 2026-11-09); Epoch its compaction epoch;
	// PushProblem names a push setting that does not read (the store then
	// pushes as now). Empty for a store push modes do not apply to.
	Push        string `json:"push,omitempty"`
	PushSummary string `json:"push_summary,omitempty"`
	Epoch       int    `json:"epoch,omitempty"`
	PushProblem string `json:"push_problem,omitempty"`
	// SameCloneAs names the store listed earlier that shares this store's
	// clone (a folder of it, B2-5): its commits are counted there once, so
	// Ahead and Behind are 0 here.
	SameCloneAs string `json:"same_clone_as,omitempty"`
}

// Store kinds of StoreState.
const (
	StoreKindGit    = "git"
	StoreKindFolder = "folder"
)

// StoreRefusal is one store push the remote refused.
type StoreRefusal struct {
	Store  string   `json:"store"`
	Code   string   `json:"code"`
	Reason string   `json:"reason"`
	Rows   []string `json:"rows,omitempty"`
	At     string   `json:"at"`
	// Next is the step that settles it.
	Next string `json:"next"`
}

// StoreHeld is one store folder whose push waits on another folder's
// refusal: nothing of its own was refused.
type StoreHeld struct {
	Store string `json:"store"`
	// HeldBy is the store folder whose refusal holds this push (its
	// attached name when one of this machine's refusals names the row).
	HeldBy string   `json:"held_by"`
	Code   string   `json:"code"`
	Rows   []string `json:"rows,omitempty"`
	At     string   `json:"at"`
	// Next is the step that settles it.
	Next string `json:"next"`
}

// StatusOut is the output of sync.status (envelope kind sync.status).
type StatusOut struct {
	SchemaVersion int        `json:"schema_version"`
	Kind          string     `json:"kind"`
	Data          StatusData `json:"data"`
	Lines         []string   `json:"-"`
}

// Text implements op.Texter.
func (o StatusOut) Text() string { return text(o.Lines) }

// Conflict is one saved conflict copy.
type Conflict struct {
	ID       int64  `json:"id"`
	Table    string `json:"table"`
	RowID    string `json:"row_id"`
	Kept     string `json:"kept"`
	OtherDoc string `json:"other_doc"`
	At       string `json:"at"`
	Resolved bool   `json:"resolved"`
	// Store, Fields and KeptDoc are set for a row of an attached store
	// that both this machine and the store's remote changed: the store's
	// name, the fields both sides changed differently, and this machine's
	// row file (OtherDoc is the remote's).
	Store   string   `json:"store,omitempty"`
	Fields  []string `json:"fields,omitempty"`
	KeptDoc string   `json:"kept_doc,omitempty"`
}

// ConflictsOut is the output of sync.conflicts (envelope kind
// sync.conflicts).
type ConflictsOut struct {
	SchemaVersion int        `json:"schema_version"`
	Kind          string     `json:"kind"`
	Count         int        `json:"count,omitempty"`
	Data          []Conflict `json:"data"`
	Lines         []string   `json:"-"`
}

// Text implements op.Texter.
func (o ConflictsOut) Text() string { return text(o.Lines) }

// TrashEntry is one row sync removed from this machine's lore.db.
type TrashEntry struct {
	ID     int64  `json:"id"`
	Table  string `json:"table"`
	RowID  string `json:"row_id"`
	Reason string `json:"reason"`
	At     string `json:"at"`
	Doc    string `json:"doc"`
}

// TrashListOut is the output of sync.trash.list (envelope kind sync.trash).
type TrashListOut struct {
	SchemaVersion int          `json:"schema_version"`
	Kind          string       `json:"kind"`
	Count         int          `json:"count,omitempty"`
	Data          []TrashEntry `json:"data"`
	Lines         []string     `json:"-"`
}

// Text implements op.Texter.
func (o TrashListOut) Text() string { return text(o.Lines) }

// PeekRow is one row read from another git ref.
type PeekRow struct {
	Table   string `json:"table"`
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

// PeekOut is the output of sync.peek (envelope kind sync.peek).
type PeekOut struct {
	SchemaVersion int       `json:"schema_version"`
	Kind          string    `json:"kind"`
	Count         int       `json:"count,omitempty"`
	Data          []PeekRow `json:"data"`
	Lines         []string  `json:"-"`
}

// Text implements op.Texter.
func (o PeekOut) Text() string { return text(o.Lines) }

// CIMergeData is the data of `lore sync ci-merge --json`.
type CIMergeData struct {
	Outcome  string   `json:"outcome"`
	Base     string   `json:"base"`
	Commit   string   `json:"commit,omitempty"`
	Merged   []string `json:"merged,omitempty"`
	Blocking []string `json:"blocking,omitempty"`
	Reason   string   `json:"reason,omitempty"`
}

// CIMergeOut is the output of sync.ci_merge (envelope kind sync.ci-merge).
type CIMergeOut struct {
	SchemaVersion int         `json:"schema_version"`
	Kind          string      `json:"kind"`
	Data          CIMergeData `json:"data"`
	Lines         []string    `json:"-"`
}

// Text implements op.Texter.
func (o CIMergeOut) Text() string { return text(o.Lines) }

// ActionData is the data of `lore sync install-action --json`.
type ActionData struct {
	Path        string   `json:"path"`
	Status      string   `json:"status"`
	LoreVersion string   `json:"lore_version,omitempty"`
	Pin         string   `json:"pin,omitempty"`
	Branches    []string `json:"branches,omitempty"`
	Manual      bool     `json:"manual"`
	Auto        bool     `json:"auto"`
	Content     string   `json:"content,omitempty"`
}

// ActionOut is the output of sync.install_action (envelope kind
// sync.action).
type ActionOut struct {
	SchemaVersion int        `json:"schema_version"`
	Kind          string     `json:"kind"`
	Data          ActionData `json:"data"`
	Lines         []string   `json:"-"`
}

// Text implements op.Texter. A --dry-run prints the workflow file as is.
func (o ActionOut) Text() string { return text(o.Lines) }

// ExplainPlace names where one copy of a row lives (explain's copies[]):
// a closed set, one value per lookup step of LORE_TEAM_SPEC F1.
type ExplainPlace string

const (
	// ExplainLive is the local live ref.
	ExplainLive ExplainPlace = "live"
	// ExplainBranch is this branch's committed copy (HEAD).
	ExplainBranch ExplainPlace = "branch"
	// ExplainWorkTree is a row file in the working tree not committed yet.
	ExplainWorkTree ExplainPlace = "work_tree"
	// ExplainDefault is the fetched default branch (<remote>/<default>).
	ExplainDefault ExplainPlace = "default_branch"
	// ExplainStore is an attached store's copy (a row of a team or
	// personal store, not of this repo).
	ExplainStore ExplainPlace = "store"
)

// ExplainCopy is one place explain looked for the row: whether a copy is
// there, what it is, and whether it wins the lookup. The text lines carry
// the same facts; this is their machine-readable form.
type ExplainCopy struct {
	Place ExplainPlace `json:"place"`
	// Ref is the commit or ref read: the live ref's local commit (short),
	// "HEAD", or "<remote>/<default>".
	Ref     string `json:"ref,omitempty"`
	Present bool   `json:"present"`
	// State is "row" or "tombstone" (live), or "deleted_on_purpose" (the
	// default branch's purge list names it); empty otherwise.
	State string `json:"state,omitempty"`
	Path  string `json:"path,omitempty"`
	Bytes int    `json:"bytes,omitempty"`
	// HLC, LiveSince and LastCopyAt are the live copy's version stamps.
	HLC        string `json:"hlc,omitempty"`
	LiveSince  string `json:"live_since,omitempty"`
	LastCopyAt string `json:"last_copy_at,omitempty"`
	Conflict   bool   `json:"conflict,omitempty"`
	// Unpushed counts live writes not pushed yet (live copy only).
	Unpushed int  `json:"unpushed,omitempty"`
	Winner   bool `json:"winner"`
	// Store is the attached store's name (place store only).
	Store string `json:"store,omitempty"`
}

// ExplainLand is the landing plan's decision for a live row.
type ExplainLand struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
	// Text is the land line as printed (a finished row's "leaves live…").
	Text string `json:"text"`
}

// ExplainOut is the output of explain.run: the id (items, as before), every
// copy, which wins and why, and the land decision.
type ExplainOut struct {
	Items  []string      `json:"items,omitempty"`
	ID     string        `json:"id"`
	Folder string        `json:"folder"`
	Copies []ExplainCopy `json:"copies"`
	// Winner is the place whose copy wins ("" when none was found);
	// Reason is the rule, as the "wins:" line words it.
	Winner ExplainPlace `json:"winner,omitempty"`
	Reason string       `json:"reason"`
	// Land is empty (omitted) when the row has no live copy.
	Land  ExplainLand `json:"land,omitzero"`
	Lines []string    `json:"-"`
}

// Text implements op.Texter.
func (o ExplainOut) Text() string { return text(o.Lines) }

// DoneOut is the output of the commands v0.1.17 gave no --json shape
// (resolve, trash restore, purge, fix-projects, promote, merge-rows, land): what was done, and the report lines.
type DoneOut struct {
	// Done is the one-line result ("conflict #3 resolved (kept)").
	Done string `json:"done,omitempty"`
	// Items are the ids, files or plan rows the command touched.
	Items []string `json:"items,omitempty"`
	Lines []string `json:"-"`
}

// Text implements op.Texter.
func (o DoneOut) Text() string { return text(o.Lines) }
