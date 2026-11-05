package sync

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
type TrashListIn struct{}

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
	Branch string   `json:"branch" flag:"to" desc:"target branch (required)"`
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
}

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
	Stores         []StoreReport  `json:"stores,omitempty"`
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

// DoneOut is the output of the commands v0.1.17 gave no --json shape
// (resolve, trash restore, purge, fix-projects, promote, merge-rows, land,
// explain): what was done, and the report lines.
type DoneOut struct {
	// Done is the one-line result ("conflict #3 resolved (kept)").
	Done string `json:"done,omitempty"`
	// Items are the ids, files or plan rows the command touched.
	Items []string `json:"items,omitempty"`
	Lines []string `json:"-"`
}

// Text implements op.Texter.
func (o DoneOut) Text() string { return text(o.Lines) }
