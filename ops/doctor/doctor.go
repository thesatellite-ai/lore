// Package doctor defines `lore doctor` (layer L5, CLI_REFERENCE 3.7): the
// health checks, with the stable exit contract 0 healthy, 1 degraded,
// 2 broken.
//
//	doctor.run ─▶ Host.Base (this binary's lore.db, sync, identity and PATH
//	          │   checks; none in a binary without lore.db)
//	          ─▶ Env.DoctorChecks(fix): the core's store checks (task leads,
//	          │   triage owner, store locks, edits after a purge, internal off)
//	          │   warning and not fixed ─▶ Warnings + degraded; else Notes;
//	          │   a check that cannot run ─▶ one warning naming it, the rest run
//	          ─▶ Report (v0.1.17 keys + exit_status) ─▶ op.ExitStatuser: the CLI
//	              prints it, then exits 1 / 2 (P3-MISC-8)
//
// A degraded or broken report is a result, not an error: the run itself
// succeeded. --fix applies the safe repairs, so the operation writes.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names (`lore doctor`, a bare-noun shortcut of run).
const (
	Noun  = "doctor"
	OpRun = "doctor.run"
	// RunVerb is the generated verb the bare `lore doctor` runs.
	RunVerb = "run"
)

// restNoun keeps the REST collection "doctor" un-pluralised.
const restNoun = "doctor"

// SchemaVersion is the v0.1.17 report schema_version.
const SchemaVersion = 1

// Statuses and their exit statuses (a stable contract for monitoring).
const (
	StatusHealthy  = "healthy"
	StatusDegraded = "degraded"
	StatusBroken   = "broken"

	exitHealthy  = 0
	exitDegraded = 1
	exitBroken   = 2
)

// Levels of a core check finding: an error breaks (exit 2), a warning
// degrades (exit 1), a note does not count.
const (
	LevelError   = "error"
	LevelWarning = "warning"
	LevelNote    = "note"
)

// noLoreDBNote is the note of a binary without the lore.db checks.
const noLoreDBNote = "this build checks the stores only (no lore.db checks)"

// Finding is one result of a core check (wire.DoctorFinding).
type Finding struct {
	Check string
	Level string
	Fixed bool
	// Line is the finding's one printed line.
	Line string
}

// CheckRun is one core check's findings, or why it could not run.
type CheckRun struct {
	Check    string
	Findings []Finding
	Err      error
}

// Identity is how this machine resolves the person.
type Identity struct {
	Resolved string `json:"resolved"`
	Source   string `json:"source"`
	Stable   bool   `json:"stable"`
}

// FileError is a broken or refused .lore/data file.
type FileError struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// File error kinds (v0.1.17's lsync kinds, the two a row-file scan finds).
const (
	FileErrorInvalid  = "invalid-file"
	FileErrorConflict = "conflict-markers"
)

// Sync is the [sync] section (v0.1.17 keys), present only in a project.
//
// Field sources (E-GAPS-1 G4): the old binary fills every field from
// lore.db's sync tables. A binary without lore.db (NoLoreDB) fills only
// the fields with a source on disk or in git (wire/hostbase_doctor_sync.go):
// data_dir_present, tracked_files (files git tracks under .lore/data),
// open_conflicts (row files with conflict markers), file_errors,
// project_files, uncommitted_files, data_dir_gitignored,
// merge_driver_configured, in_git_repo. The lore.db counters below the
// line (baselined, pending_exports, trash_rows, errors,
// dangling_references, duplicate_groups) have no source there: they are
// omitempty, so that report never shows a value it did not read, and the
// text leaves their lines out. They go away with the old binary (Phase E).
type Sync struct {
	DataDirPresent bool        `json:"data_dir_present"`
	TrackedFiles   int         `json:"tracked_files"`
	OpenConflicts  int         `json:"open_conflicts"`
	ProjectFiles   int         `json:"project_files"`
	FileErrors     []FileError `json:"file_errors,omitempty"`
	Uncommitted    int         `json:"uncommitted_files"`
	DataIgnored    bool        `json:"data_dir_gitignored"`
	MergeDriverOK  bool        `json:"merge_driver_configured"`
	InGitRepo      bool        `json:"in_git_repo"`

	// lore.db counters (old binary only; see above).
	Baselined       bool        `json:"baselined,omitempty"`
	PendingExports  int         `json:"pending_exports,omitempty"`
	TrashRows       int         `json:"trash_rows,omitempty"`
	Errors          []FileError `json:"errors,omitempty"`
	DanglingRefs    int         `json:"dangling_references,omitempty"`
	DuplicateGroups int         `json:"duplicate_groups,omitempty"`

	// NoLoreDB is set by a host without lore.db: the text then prints the
	// row-file lines instead of the lore.db ones.
	NoLoreDB bool `json:"-"`
}

// Base is what Host.Base found.
type Base struct {
	// Status is healthy, degraded or broken.
	Status        string
	DBOK          bool
	WALSizeBytes  int64
	Identity      Identity
	PathConflicts []string
	Warnings      []string
	Notes         []string
	Errors        []string
	// Sync is nil outside a project (and in a repo store that is not row
	// files).
	Sync *Sync
	// RepoAddresses is the "Repo addresses" report as JSON (nil: none).
	RepoAddresses []byte
}

// Host runs this binary's own checks (lore.db, sync, identity, PATH).
type Host interface {
	Base(ctx context.Context, fix bool) (Base, error)
}

// Env is what doctor needs; *wire.App implements it.
type Env interface {
	// DoctorHost is the binary's Host; ok is false in a binary without
	// lore.db (the core checks still run).
	DoctorHost(ctx context.Context) (Host, bool)
	// DoctorChecks runs the core's store checks (fix: the safe repairs).
	DoctorChecks(ctx context.Context, fix bool) []CheckRun
	// BinaryVersion is the binary's version.
	BinaryVersion() string
}

// RawJSON is a JSON value kept as its bytes: it encodes as itself, and is
// text for the operation's field model (TypeText).
type RawJSON []byte

// MarshalJSON implements json.Marshaler ("null" when empty).
func (r RawJSON) MarshalJSON() ([]byte, error) {
	if len(r) == 0 {
		return []byte("null"), nil
	}
	return r, nil
}

// UnmarshalJSON implements json.Unmarshaler: a copy of the bytes.
func (r *RawJSON) UnmarshalJSON(b []byte) error {
	*r = append((*r)[:0], b...)
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (r RawJSON) MarshalText() ([]byte, error) { return r, nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (r *RawJSON) UnmarshalText(b []byte) error { return r.UnmarshalJSON(b) }

// RunIn is the input of doctor.run.
type RunIn struct {
	Fix bool `json:"fix,omitempty" flag:"fix" desc:"repair what doctor can fix safely (reversed --supersedes links, a task lead missing from assignees)"`
}

// Report is the output of doctor.run: v0.1.17's report keys, plus
// exit_status (the process exit the CLI uses, for HTTP callers).
type Report struct {
	SchemaVersion int      `json:"schema_version"`
	OK            bool     `json:"ok"`
	Status        string   `json:"status"`
	DBOK          bool     `json:"db_ok"`
	WALSizeBytes  int64    `json:"wal_size_bytes"`
	Identity      Identity `json:"identity"`
	PathConflicts []string `json:"path_conflicts,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
	Notes         []string `json:"notes,omitempty"`
	Errors        []string `json:"errors,omitempty"`
	BinaryVersion string   `json:"binary_version"`
	// Sync is the [sync] section (absent outside a project).
	Sync RawJSON `json:"sync,omitempty"`
	// RepoAddresses is the "Repo addresses" check (PROV2, Q5).
	RepoAddresses RawJSON `json:"repo_addresses,omitempty"`
	// Exit is the exit status: 0 healthy, 1 degraded, 2 broken.
	Exit int `json:"exit_status"`
	sync *Sync
	// local is false in a binary without lore.db: the text then leaves out
	// the [db] and [identity] sections it has nothing for.
	local bool
}

// ExitStatus implements op.ExitStatuser.
func (r Report) ExitStatus() int { return r.Exit }

// Ops returns the doctor operation bound to env, for op.NewRegistry.
func Ops(env Env) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpRun, Summary: "Run health checks; exit 0 healthy, 1 degraded, 2 broken",
			Permission: op.PermWrite, Access: op.AccessRead, Local: true, RESTNoun: restNoun,
			// --fix of reversed --supersedes links asks first (the host answers
			// E_NEEDS_CONFIRM until the caller said yes).
			Confirm:  op.ConfirmRule{MayAsk: true},
			Errors:   []errs.Code{errs.Internal, errs.NeedsConfirm},
			Examples: []op.Example{{Title: "check this repo and its stores", CLI: "lore doctor", Want: ""}},
		}, func(ctx context.Context, in RunIn) (Report, error) {
			return Run(ctx, env, in)
		}),
	}
}

// Run is doctor.run over env.
func Run(ctx context.Context, env Env, in RunIn) (Report, error) {
	base := Base{Status: StatusHealthy, DBOK: true, Notes: []string{noLoreDBNote}}
	h, local := env.DoctorHost(ctx)
	if local {
		var err error
		if base, err = h.Base(ctx, in.Fix); err != nil {
			return Report{}, err
		}
	}
	r := Report{SchemaVersion: SchemaVersion, Status: base.Status, DBOK: base.DBOK, WALSizeBytes: base.WALSizeBytes,
		Identity: base.Identity, PathConflicts: base.PathConflicts, Warnings: base.Warnings, Notes: base.Notes,
		Errors: base.Errors, BinaryVersion: env.BinaryVersion(), RepoAddresses: base.RepoAddresses, sync: base.Sync, local: local}
	if base.Sync != nil {
		b, err := json.Marshal(base.Sync)
		if err != nil {
			return Report{}, errs.Wrap(errs.Internal, "encode the sync section", err)
		}
		r.Sync = b
	}
	addChecks(&r, env.DoctorChecks(ctx, in.Fix))
	r.OK = r.Status == StatusHealthy
	r.Exit = exitOf(r.Status)
	return r, nil
}

// addChecks folds the core checks into r (a-doctor's rule: a warning not
// fixed degrades, everything else is a note; a check that cannot run is
// one warning naming it). An error finding (a damaged cache file, the old
// binary's r.Errors) goes to Errors and breaks the status.
func addChecks(r *Report, runs []CheckRun) {
	for _, c := range runs {
		if c.Err != nil {
			r.Warnings = append(r.Warnings, c.Check+": the check could not run: "+c.Err.Error())
			degrade(r)
			continue
		}
		for _, f := range c.Findings {
			if f.Level == LevelError && !f.Fixed {
				r.Errors = append(r.Errors, f.Line)
				r.Status = StatusBroken
				continue
			}
			if f.Level != LevelWarning || f.Fixed {
				r.Notes = append(r.Notes, f.Line)
				continue
			}
			r.Warnings = append(r.Warnings, f.Line)
			degrade(r)
		}
	}
}

// degrade lowers healthy to degraded (broken stays broken).
func degrade(r *Report) {
	if r.Status == StatusHealthy {
		r.Status = StatusDegraded
	}
}

// exitOf is a status's exit status.
func exitOf(status string) int {
	switch status {
	case StatusBroken:
		return exitBroken
	case StatusDegraded:
		return exitDegraded
	}
	return exitHealthy
}

// Text implements op.Texter: v0.1.17's report layout.
func (r Report) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Status: %s\nBinary: %s\n\n", strings.ToUpper(r.Status), r.BinaryVersion)
	if r.local {
		fmt.Fprintf(&b, "[db]\n  ok:       %v\n  wal_size: %d bytes\n\n", r.DBOK, r.WALSizeBytes)
	}
	if s := r.sync; s != nil {
		b.WriteString(s.text())
	}
	if r.local {
		stable := "yes"
		if !r.Identity.Stable {
			stable = "NO (ephemeral)"
		}
		fmt.Fprintf(&b, "[identity]\n  resolved: %s\n  source:   %s\n  stable:   %s\n", r.Identity.Resolved, r.Identity.Source, stable)
	}
	list(&b, "Errors:", r.Errors)
	list(&b, "Notes:", r.Notes)
	list(&b, "Warnings:", r.Warnings)
	return b.String()
}

// text is the [sync] block: v0.1.17's lines, or, without lore.db, the
// lines that have a source there (Sync's comment).
func (s *Sync) text() string {
	var b strings.Builder
	b.WriteString("[sync]\n")
	if s.NoLoreDB {
		fmt.Fprintf(&b, "  in git repo:      %v\n  tracked files:    %d\n", s.InGitRepo, s.TrackedFiles)
	} else {
		fmt.Fprintf(&b, "  baselined:        %v\n  pending exports:  %d\n", s.Baselined, s.PendingExports)
	}
	fmt.Fprintf(&b, "  open conflicts:   %d\n  file errors:      %d\n  merge driver:     %v\n", s.OpenConflicts, len(s.FileErrors), s.MergeDriverOK)
	if s.NoLoreDB {
		fmt.Fprintf(&b, "  data gitignored:  %v\n", s.DataIgnored)
	}
	fmt.Fprintf(&b, "  uncommitted:      %d\n\n", s.Uncommitted)
	return b.String()
}

// list writes a titled bullet list after a blank line (nothing when empty).
func list(b *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	b.WriteString("\n" + title + "\n")
	for _, s := range items {
		b.WriteString("  - " + s + "\n")
	}
}
