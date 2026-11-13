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

	"github.com/thesatellite-ai/lore/logic/onboard"
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

// Health is doctor's overall verdict (Report.Status): healthy, degraded
// or broken. A closed set: the OpenAPI document lists it (EnumValues).
type Health string

// Healths lists every verdict, best first.
func Healths() []Health { return []Health{StatusHealthy, StatusDegraded, StatusBroken} }

// EnumValues lists every verdict (op.Enumerated). internal/layers pins it
// to the constants.
func (Health) EnumValues() []string {
	out := make([]string, 0, len(Healths()))
	for _, h := range Healths() {
		out = append(out, string(h))
	}
	return out
}

// Statuses and their exit statuses (a stable contract for monitoring).
// ds:def id=doctor-statuses-y89guvyz owner=@khanakia stability=stable desc="lore doctor statuses and their exit codes (healthy 0, degraded 1, broken 2)"
const (
	StatusHealthy  Health = "healthy"
	StatusDegraded Health = "degraded"
	StatusBroken   Health = "broken"

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

// lineFixIsFix heads the steps of doctor --fix (D-ak).
const lineFixIsFix = "lore doctor --fix runs the same steps as lore fix:"

// noLoreDBNote is the note of a binary without the lore.db checks.
const noLoreDBNote = "this build checks the stores only (no lore.db checks)"

// Finding is one result of a core check (wire.DoctorFinding).
type Finding struct {
	Check string
	Level string
	Fixed bool
	// Line is the finding's one printed line.
	Line string
	// Fix is the command or step that repairs it ("" when none): the
	// finding's Fix in findings[] (a plain `lore …` command is runnable).
	Fix string
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
	// Format names the project's format (D12: the committed _meta.json's
	// "format", or the rows'), "" outside a lore project.
	Format string `json:"format,omitempty"`

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
	Status        Health
	DBOK          bool
	WALSizeBytes  int64
	Identity      Identity
	PathConflicts []string
	Warnings      []string
	Notes         []string
	Errors        []string
	// Fixed are the repairs this run made (each also a note): lore fix's
	// doctor fixes step lists them, as it lists the core checks' fixed
	// findings (item 46).
	Fixed []string
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
	Fix bool `json:"fix,omitempty" flag:"fix" desc:"run lore fix's steps (never a migration: a project on the lore 0.1.17 format needs lore migrate run or lore fix --migrate): render, agent hooks, old store clones proved safe, then every safe doctor repair (git info/exclude, store clone identity, merge driver, config style, actor roster, reversed --supersedes links, …); prints each step and the status after them"`
	// All checks every setup on this machine (ONB 6), not only the active
	// one.
	All bool `json:"all,omitempty" flag:"all" desc:"check every setup on this machine: identity, GitHub, stores, repos, tools (exit 0 healthy, 1 degraded, 2 broken)"`
	// Fresh skips the 60-second result cache (ONB 6.1).
	Fresh bool `json:"fresh,omitempty" flag:"fresh" desc:"probe again instead of reusing results from the last minute"`
	// Setup limits --all to one setup (and names the active one for the
	// [setup] section): REST body only; the CLI passes the global --setup
	// (op.SetupOf), never a per-op flag, which would clash with it.
	Setup string `json:"setup,omitempty" desc:"the setup to check (default: every setup with --all, else the active one)"`
}

// Report is the output of doctor.run: v0.1.17's report keys, plus
// exit_status (the process exit the CLI uses, for HTTP callers).
type Report struct {
	SchemaVersion int      `json:"schema_version"`
	OK            bool     `json:"ok"`
	Status        Health   `json:"status"`
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
	// Setups are the setups checked (ONB 6.3): every one with --all, else
	// the active setup only (absent when none is active).
	Setups []SetupReport `json:"setups,omitempty"`
	// Machine holds the machine-wide checks (--all only).
	Machine MachineReport `json:"machine,omitzero"`
	// Findings are the report's warnings and errors as results (check,
	// status, level, detail, fix), so every finding that changes Status is
	// listed and counted (findings.go).
	Findings []onboard.CheckResult `json:"findings,omitempty"`
	// Counts are the results by status: every setup's checks and the
	// machine checks with --all, the active setup's checks in plain doctor,
	// and Findings in both.
	Counts Counts `json:"counts,omitzero"`
	// Fixed is what --fix fixed: every warning or error of the fixing run
	// that the check after it no longer finds, and every finding a check
	// fixed itself. With --fix, Status and the rest of the report are the
	// check after the fixes (owner report 2026-11-12: doctor --fix ended
	// with the status from before its fixes).
	Fixed []string `json:"fixed,omitempty"`
	// Steps are the lore fix steps --fix ran (fixproject.go): the same steps
	// as lore fix, each with where and its result.
	Steps []FixStep `json:"steps,omitempty"`
	// fixedNow are the findings a check marked fixed in this run.
	fixedNow []string
	// fixNotes are the fixing run's notes the re-check did not give
	// (repairRepo): what a repair said about why it could not fix.
	fixNotes []string
	// meta is what each warning or error line's source knew beyond its
	// text (check, fix, error level), for finalize.
	meta map[string]findingMeta
	// all selects the 6.2 text layout.
	all bool
	// disabled are --all's one-line notes about skipped disabled setups (alltext prints them).
	disabled []string
	// unchecked are --all's info lines about known repos no setup applies
	// to, whose stores were not checked (alltext prints them).
	unchecked []string
	sync      *Sync
	// local is false in a binary without lore.db: the text then leaves out
	// the [db] and [identity] sections it has nothing for.
	local bool
	// repoSkipped counts the core checks skipped because the run is not
	// inside a repo (outsideRepo); --all prints one line for them.
	repoSkipped int
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
		lastOp(env),
		fixOp(env),
	}
}

// Run is doctor.run over env.
func Run(ctx context.Context, env Env, in RunIn) (Report, error) {
	if in.All {
		ctx = WithMachineSweep(ctx) // --all checks the whole machine: every cache file
	}
	var r Report
	var err error
	if in.Fix {
		// D-ak: --fix is lore fix's steps (fixproject.go), then the
		// re-check; the report shows the state now
		r, err = fixProject(ctx, env, false)
	} else {
		r, err = runRepo(ctx, env, false)
	}
	if err != nil {
		return Report{}, err
	}
	if err := addSetups(ctx, env, in, &r); err != nil {
		return Report{}, err
	}
	r.finalize()
	r.OK = r.Status == StatusHealthy
	r.Exit = exitOf(r.Status)
	return r, nil
}

// runRepo is the report's repo part: this binary's base checks and the
// core checks (doctor.fix re-reads only this part: no setup is probed).
// The caller finalizes it.
func runRepo(ctx context.Context, env Env, fix bool) (Report, error) {
	in := RunIn{Fix: fix}
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
	r.fixedNow = append(r.fixedNow, base.Fixed...)
	unnamedBase(&r, base)
	for _, e := range r.Errors {
		// A base error breaks the report only when the base said so (a
		// version below the repo's floor); the rest degrade.
		r.note(e, findingMeta{check: CheckError, broken: base.Status == StatusBroken})
	}
	addChecks(&r, env.DoctorChecks(ctx, in.Fix), outsideRepo(ctx, env))
	return r, nil
}

// Lines of a host status that names no finding (a host bug): the status
// is listed instead of standing unexplained (findings.go).
const (
	lineUnnamedDegraded = "doctor: this lore's own checks reported degraded without naming a finding; run lore doctor --json and report it"
	lineUnnamedBroken   = "doctor: this lore's own checks reported broken without naming an error; run lore doctor --json and report it"
)

// unnamedBase lists a base status that no warning or error explains, so
// the report's status never rests on something it does not show.
func unnamedBase(r *Report, base Base) {
	switch {
	case base.Status == StatusBroken && len(base.Errors) == 0:
		r.Errors = append(r.Errors, lineUnnamedBroken)
	case base.Status == StatusDegraded && len(base.Errors) == 0 && len(base.Warnings) == 0:
		r.Warnings = append(r.Warnings, lineUnnamedDegraded)
	}
}

// noteRepoSkipped is the one note for the core checks that need a repo,
// run outside one (B4: twelve "could not run" warnings said nothing the
// user could act on).
const noteRepoSkipped = "not inside a git repo, so the repo checks were skipped (%d); run lore doctor in a repo to check it"

// outsideRepo reports whether the run is known to be outside any repo:
// the Env answers RepoTop (SetupEnv) and it names no folder. An Env
// without RepoTop is never "outside", so its failures stay warnings.
func outsideRepo(ctx context.Context, env Env) bool {
	rt, ok := env.(interface {
		RepoTop(ctx context.Context) (string, error)
	})
	if !ok {
		return false
	}
	top, err := rt.RepoTop(ctx)
	return err != nil || top == ""
}

// addChecks folds the core checks into r (a-doctor's rule: a warning not
// fixed degrades, everything else is a note; a check that cannot run is
// one warning naming it). An error finding (a damaged cache file, the old
// binary's r.Errors) goes to Errors and breaks the status. Outside a repo
// (outside), a check that failed only because there is no store here
// (E_NOT_A_STORE) is skipped, not a warning: the skips become one note
// and do not degrade.
func addChecks(r *Report, runs []CheckRun, outside bool) {
	for _, c := range runs {
		if c.Err != nil && outside && errs.Has(c.Err, errs.NotAStore) {
			r.repoSkipped++
			continue
		}
		if c.Err != nil {
			line := c.Check + ": the check could not run: " + c.Err.Error()
			r.note(line, findingMeta{check: onboard.CheckID(c.Check)})
			r.Warnings = append(r.Warnings, line)
			degrade(r)
			continue
		}
		for _, f := range c.Findings {
			if f.Level == LevelError && !f.Fixed {
				r.note(f.Line, findingMeta{check: onboard.CheckID(f.Check), fix: f.Fix, broken: true})
				r.Errors = append(r.Errors, f.Line)
				r.Status = StatusBroken
				continue
			}
			if f.Level != LevelWarning || f.Fixed {
				if f.Fixed {
					r.fixedNow = append(r.fixedNow, f.Line)
				}
				r.Notes = append(r.Notes, f.Line)
				continue
			}
			r.note(f.Line, findingMeta{check: onboard.CheckID(f.Check), fix: f.Fix})
			r.Warnings = append(r.Warnings, f.Line)
			degrade(r)
		}
	}
	if r.repoSkipped > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf(noteRepoSkipped, r.repoSkipped))
	}
}

// degrade lowers healthy to degraded (broken stays broken).
func degrade(r *Report) {
	if r.Status == StatusHealthy {
		r.Status = StatusDegraded
	}
}

// exitOf is a status's exit status.
func exitOf(status Health) int {
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
	if r.all {
		return r.allText()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Status: %s\nBinary: %s\n\n", strings.ToUpper(string(r.Status)), r.BinaryVersion)
	if len(r.Steps) > 0 {
		// the status stays the first line (scripts read it); the steps follow
		b.WriteString(lineFixIsFix + "\n" + StepsText(r.Steps))
		for _, n := range NextLines(r.Steps) {
			b.WriteString(n + "\n")
		}
		b.WriteString("\n")
	}
	if len(r.Fixed) > 0 {
		list(&b, fmt.Sprintf("Fixed (%d, checked again after fixing; the status above is now):", len(r.Fixed)), r.Fixed)
	}
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
	if sec := r.setupSection(); sec != "" {
		// ONB 6.2: plain doctor adds one [setup] section, the active setup.
		b.WriteString("\n" + sec)
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
	fmt.Fprintf(&b, "  uncommitted:      %d\n", s.Uncommitted)
	if s.Format != "" {
		fmt.Fprintf(&b, "  format:           %s\n", s.Format)
	}
	b.WriteString("\n")
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

// fixedBetween is what a fixing run fixed: its findings (warnings and
// errors) that the check after it no longer reports, and the findings a
// check fixed itself, each once, in report order.
// withFixNotes is the re-check's notes plus the fixing run's notes it
// does not repeat: what a fix did ("repaired 1 --supersedes link(s): …")
// is said by the fixing run only, and must not vanish with the re-check.
func withFixNotes(after, fixing []string) []string {
	have := map[string]bool{}
	for _, l := range after {
		have[noteKey(l)] = true
	}
	for _, l := range fixing {
		if k := noteKey(l); !have[k] {
			have[k] = true
			after = append(after, l)
		}
	}
	return after
}

func fixedBetween(fixing, after Report) []string {
	still := map[string]bool{}
	for _, l := range append(append([]string(nil), after.Warnings...), after.Errors...) {
		still[l] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, l := range append(append(append([]string(nil), fixing.Errors...), fixing.Warnings...), fixing.fixedNow...) {
		if still[l] || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	return out
}

// machineSweepKey marks a context whose doctor run checks machine-wide
// state (every cache file under the lore home), not only the repo's own.
type machineSweepKey struct{}

// WithMachineSweep marks ctx for the machine-wide checks: `lore doctor
// --all` sets it; a repo's own doctor (and each `lore fix --all` child)
// checks only that repo's caches (owner item 35 follow-up).
func WithMachineSweep(ctx context.Context) context.Context {
	return context.WithValue(ctx, machineSweepKey{}, true)
}

// MachineSweep reports whether ctx asks for the machine-wide checks.
func MachineSweep(ctx context.Context) bool {
	v, _ := ctx.Value(machineSweepKey{}).(bool)
	return v
}
