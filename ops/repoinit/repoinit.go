// Package repoinit defines `repo.init` (layer L5): the behaviour of `lore
// init` as a registry operation, so the setup page's "this folder" step can
// run it over /v1 like every other step (ONBOARDING_DESIGN 4.10, conflict
// 10.2). Before this package `lore init` was only a cobra command in
// app/init.go; that command is now a thin caller of Run, so `lore init`
// and `lore repo init` take the same path and print the same text.
//
// Flow (unchanged from app/init.go, FA-OLDCODE-2):
//
//	SharedRepoMeta (G-MONO-1: two ids refuse, unless wd already has a project)
//	  ─▶ RootAt (OWNERQ-1: the project is rooted at wd; undone on failure)
//	  ─▶ Project: found ─▶ local state existed  ─▶ E_ALREADY_INITIALIZED
//	  │                └▶ no local state (a clone) ─▶ adopt (E1): gitignore, prime
//	  └▶ not found ─▶ name (--name, else origin's repo name, else the folder)
//	       ─▶ projects row ─▶ _meta.json ─▶ inherit the checkout's repo id
//	       ─▶ EnsureRepoIdentity ─▶ gitignore ─▶ prime ─▶ wire the tool files
//
// Every file and store touch goes through Host, which the binary that owns
// the checkout supplies (cmd/lore); the decisions live here.
package repoinit

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names. The CLI word `lore init` is a host shortcut that runs
// the same Run (app/init.go); the registry also mounts `lore repo init`.
const (
	Noun       = "repo"
	OpRepoInit = "repo.init"
)

// Fields of the projects row init writes (schema/builtin.go).
const (
	FieldName   = "name"
	FieldOrigin = "origin_url"
)

// Output lines (v0.1.17 wording; tests and scripts key on them).
const (
	// NextHint ends a fresh init: the pointer files are wired, LORE.md is
	// written by render.
	NextHint = "  next: lore render            to write .lore/LORE.md for those tools"
	// warnPrefix starts every warning line (printed on stderr by the CLI).
	warnPrefix          = "WARN: "
	warnGitignore       = "could not update .gitignore: "
	warnToolFiles       = "could not wire AI tool files: "
	alreadyInitializedF = "a lore project already exists here (%s)"
	// linkedRemoteF says the project id came from an org store link (remote).
	linkedRemoteF = "  the org store already links %s: this project keeps that link's id, so its store folder's notes reach this repo\n"
	// otherProjectsF says a new project sits beside others in the same repo
	// (name, their folders): its notes are its own (D-1, the monorepo answer
	// of docs/choosing/database-mode.md).
	otherProjectsF = "  this repo already holds a lore project in %[2]s: %[1]s is a separate project for this folder, with its own notes (run lore here to list or search them; give another name with lore init --name <name>)\n"
	// toolsFromSetupF says which setup chose the tool files (setup, files).
	toolsFromSetupF = "  tool files from setup %s: %s\n"
	// alreadyInitializedHint is the next step of a second init
	// (INTEG-MIGREST-2, docs/reference/errors.md "use the existing project").
	alreadyInitializedHint = "nothing to do: the project is ready; lore doctor checks it"
	// alreadyInitializedTargetHintF is the next step of a second init that
	// asked for tool files (--target): init never rewires an existing
	// project, so it names the command that adds them (targets, joined).
	alreadyInitializedTargetHintF = "the project is ready; to add the tool file(s) run: lore render --add-target %s"
)

// Host is the machinery behind repo.init, supplied by the binary that owns
// the checkout (cmd/lore). Invariants: HadLocalState is read before any
// store opened (opening it creates the cache); RootAt's undo removes the
// folder it created with everything a failed init wrote into it; Prime and the two
// warning-returning methods never fail the init.
type Host interface {
	// WorkDir is the folder init runs in (the project root).
	WorkDir() string
	// HadLocalState reports whether lore's local files (the repo cache
	// under the lore home or an older .lore/lore-cache.db, v0.1.17's
	// lore.db / lore.yaml / lore.toml) existed when the process
	// started: the line between "already initialised" and "a fresh clone".
	HadLocalState() bool
	// SharedRepoMeta is the repo identity the checkout's other projects
	// carry (G-MONO-1); err refuses two different ids.
	SharedRepoMeta(ctx context.Context) (rowfs.RepoMeta, bool, error)
	// OwnRepoMeta reports whether wd already has its own _meta.json.
	OwnRepoMeta(ctx context.Context) bool
	// RootAt makes wd the root the repo store opens (OWNERQ-1).
	RootAt(ctx context.Context) (undo func(), err error)
	// Project is the repo store's project (E_NOT_FOUND when there is none).
	Project(ctx context.Context) (ids.ID[ids.Project], error)
	// DefaultName is the name when --name is unset: origin's repo name,
	// else the folder name, lower-cased.
	DefaultName(ctx context.Context) string
	// OriginURL is origin's URL without credentials, or "".
	OriginURL(ctx context.Context) string
	// CreateProject writes the projects row into the repo store and returns
	// its row id as stored and as a project id. A zero id mints a new one;
	// otherwise the row takes id (the project a store link already names).
	CreateProject(ctx context.Context, id ids.ID[ids.Project], fields map[string]value.Value) (string, ids.ID[ids.Project], error)
	// LinkedProject is the project id an org store link already gives this
	// repo by its remote (a repo linked with `lore org link` before it had
	// any lore files), with that remote; ok false when nothing links it.
	LinkedProject(ctx context.Context) (pid ids.ID[ids.Project], remote string, ok bool)
	// SeedRepoMeta writes _meta.json naming pid.
	SeedRepoMeta(ctx context.Context, pid ids.ID[ids.Project]) error
	// InheritRepoID gives the new project the checkout's repo identity.
	InheritRepoID(ctx context.Context, shared rowfs.RepoMeta) error
	// EnsureRepoIdentity adopts or mints the repo id and returns it.
	EnsureRepoIdentity(ctx context.Context) (string, error)
	// EnsureGitignore adds lore's local files to wd's .gitignore.
	EnsureGitignore(ctx context.Context) error
	// Prime runs the first live fetch (v0.1.17's init did); never fails.
	Prime(ctx context.Context)
	// WireToolFiles points the AI tools at .lore/LORE.md (--target, else
	// the context.targets row, else their files, else AGENTS.md) and
	// returns the lines it reports. fromSetup names the setup whose tools
	// targets are ("" when they are --target): the printed reason says
	// where they came from (devco J-2: it said "from --target").
	WireToolFiles(ctx context.Context, targets []string, fromSetup string) (string, error)
	// SetupTools is the setup that applies in the work folder and its
	// chosen tools that have a pointer file (zero when no setup applies,
	// it chose none, or it cannot be read: init then keeps its default).
	SetupTools(ctx context.Context) SetupTools
}

// MonorepoHost is an optional Host part: the folders of the checkout's
// other lore projects (relative to the repo top, "." for the top), so a
// sub-project's init says it sits beside them (D-1). A Host without it
// prints no such line.
type MonorepoHost interface {
	OtherProjectFolders(ctx context.Context) []string
}

// SetupTools is what the setup applying to the work folder chose: the
// tool profiles (in the setup's order) and their pointer files.
type SetupTools struct {
	Setup    string
	Profiles []string
	Files    []string
}

// Env is what repo.init needs; *wire.App implements it.
type Env interface {
	// RepoInitHost is the binary's Host (E_UNSUPPORTED when it has none).
	RepoInitHost(ctx context.Context) (Host, error)
}

// InitIn is the input of repo.init (the flags of `lore init`).
type InitIn struct {
	Name   string   `json:"name,omitempty" flag:"name" desc:"project name (default: the repo name of origin's URL, else the folder name)"`
	Target []string `json:"target,omitempty" flag:"target" desc:"AI tool profile(s) or file(s) to point at .lore/LORE.md (default: the tools of the setup that applies here, else tool files found in the repo, else AGENTS.md)"`
	// NonInteractive is v0.1.17's --non-interactive: init never asks
	// (CLI_REFERENCE 2.14, Q-R2-4), so it is accepted and changes nothing.
	NonInteractive bool `json:"non_interactive,omitempty" flag:"non-interactive" desc:"accepted for v0.1.17 scripts; init never asks"`
}

// InitOut is the output of repo.init.
type InitOut struct {
	// Path is the folder the project is rooted at.
	Path string `json:"path"`
	// Adopted is true when a fresh clone took the shared project (E1);
	// nothing was minted then and Name / RepoID are empty.
	Adopted   bool   `json:"adopted"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name,omitempty"`
	RepoID    string `json:"repo_id,omitempty"`
	// ToolLines is what wiring the AI tool files reported (fresh init only).
	ToolLines string `json:"tool_lines,omitempty"`
	// ToolsFromSetup names the setup whose tools chose the files wired
	// (owner decision 2026-11-07: no --target, a setup applies and picked
	// tools with a pointer file); "" when --target or the default chose.
	ToolsFromSetup string `json:"tools_from_setup,omitempty"`
	// ToolFiles are those tools' pointer files (with ToolsFromSetup).
	ToolFiles []string `json:"tool_files,omitempty"`
	// OtherProjects are the folders (relative to the repo top, "." for the
	// top) of the checkout's other lore projects when init made a new one
	// beside them (a monorepo sub-project, D-1); init names them.
	OtherProjects []string `json:"other_projects,omitempty"`
	// LinkedRemote is the remote an org store link already named this repo
	// by; the project took that link's id (B1-18). "" for a new project.
	LinkedRemote string `json:"linked_remote,omitempty"`
	// Warnings are the non-fatal failures (.gitignore, tool files), each
	// without the "WARN: " prefix; the CLI prints them on stderr.
	Warnings []string `json:"warnings,omitempty"`
}

// Body is the stdout text of `lore init`, byte for byte as before the move.
func (o InitOut) Body() string {
	if o.Adopted {
		return fmt.Sprintf("✓ adopted the shared lore project at %s\n    project_id: %s\n", o.Path, o.ProjectID) + o.ToolLines
	}
	from := ""
	if o.ToolsFromSetup != "" {
		from = fmt.Sprintf(toolsFromSetupF, o.ToolsFromSetup, strings.Join(o.ToolFiles, ", "))
	}
	linked := ""
	if len(o.OtherProjects) > 0 {
		folders := make([]string, len(o.OtherProjects))
		for i, f := range o.OtherProjects {
			folders[i] = f
			if f == "." {
				folders[i] = "the repo root"
			}
		}
		linked = fmt.Sprintf(otherProjectsF, o.Name, strings.Join(folders, ", "))
	}
	if o.LinkedRemote != "" {
		linked = fmt.Sprintf(linkedRemoteF, o.LinkedRemote)
	}
	return fmt.Sprintf("lore initialized\n    project_id: %s\n    name:       %s\n    repo_id:    %s\n\n", o.ProjectID, o.Name, o.RepoID) +
		linked + from + o.ToolLines + NextHint + "\n"
}

// WarningLines are the warnings as printed ("WARN: …", one per line).
func (o InitOut) WarningLines() string {
	var b strings.Builder
	for _, w := range o.Warnings {
		b.WriteString(warnPrefix + w + "\n")
	}
	return b.String()
}

// Text implements op.Texter for `lore repo init` and the page: the body,
// then the warnings (a registry command has one stream).
func (o InitOut) Text() string { return o.Body() + o.WarningLines() }

// Ops returns the repo.init operation bound to env, for op.NewRegistry.
func Ops(env Env) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name:    OpRepoInit,
			Summary: "Initialize a lore project in this repository (project row and repo id); the same as lore init",
			// Local: init writes the checkout's own files and the repo store's
			// first rows before any project exists to open (setup.run is the
			// same shape).
			Permission: op.PermWrite, Access: op.AccessRead, Local: true,
			// MayAsk (I-13): in a client repo it answers E_NEEDS_CONFIRM before
			// writing lore's files into the client's repository (--yes).
			Confirm:  op.ConfirmRule{MayAsk: true, NoSecretFindings: true},
			Errors:   []errs.Code{errs.InvalidInput, errs.AlreadyInitialized, errs.AmbiguousID, errs.BadPath, errs.Unsupported, errs.NeedsConfirm, errs.Internal},
			Examples: []op.Example{{Title: "set up lore in this repository", CLI: "lore repo init --non-interactive --name app"}},
		}, func(ctx context.Context, in InitIn) (InitOut, error) {
			h, err := env.RepoInitHost(ctx)
			if err != nil {
				return InitOut{}, err
			}
			return Run(ctx, h, in)
		}),
	}
}

// Run is repo.init over h.
func Run(ctx context.Context, h Host, in InitIn) (InitOut, error) {
	wd := h.WorkDir()
	// G-MONO-1: two ids refuse before the store opens (it would leave a
	// cache file), unless wd already holds a project (adopt / already
	// initialised: no new id).
	shared, hasShared, sharedErr := h.SharedRepoMeta(ctx)
	if sharedErr != nil && !h.OwnRepoMeta(ctx) {
		return InitOut{}, sharedErr
	}
	undo, err := h.RootAt(ctx)
	if err != nil {
		return InitOut{}, err
	}
	ok := false
	defer func() {
		if !ok {
			undo()
		}
	}()
	out := InitOut{Path: wd}
	if pid, perr := h.Project(ctx); perr == nil {
		if h.HadLocalState() {
			return InitOut{}, errs.New(errs.AlreadyInitialized, fmt.Sprintf(alreadyInitializedF, pid.String())).
				WithHint(alreadyInitializedHintFor(in.Target))
		}
		// E1: the checkout carries the shared project: adopt it.
		out.Adopted, out.ProjectID = true, pid.String()
		out.addWarning(warnGitignore, h.EnsureGitignore(ctx))
		h.Prime(ctx)
		ok = true
		// A-9: adopting wires the tools asked for (--target) or chosen by
		// the setup that applies here, as a fresh init does; with neither
		// it touches no tool file (a teammate's clone is already wired).
		// WireToolFiles with no targets reads the repo's context.targets
		// row first, so a team's saved choice still wins over a setup.
		st := h.SetupTools(ctx)
		if len(in.Target) > 0 || len(st.Profiles) > 0 {
			lines, terr := h.WireToolFiles(ctx, in.Target, "")
			out.ToolLines = lines
			out.addWarning(warnToolFiles, terr)
		}
		return out, nil
	} else if !errs.Has(perr, errs.NotFound) {
		return InitOut{}, perr
	}
	if sharedErr != nil { // a new project would take a second id (G-MONO-1)
		return InitOut{}, sharedErr
	}
	fields, name, err := projectFields(ctx, h, in.Name)
	if err != nil {
		return InitOut{}, err
	}
	// B1-18: the org store may already link this repo by its remote; a new
	// id would leave that link naming nothing, and the folder's rows would
	// stop reaching the repo, so a fresh project takes the linked id.
	linked, linkedRemote, isLinked := h.LinkedProject(ctx)
	rowID, pid, err := h.CreateProject(ctx, linked, fields)
	if err != nil {
		return InitOut{}, err
	}
	if err := h.SeedRepoMeta(ctx, pid); err != nil {
		return InitOut{}, err
	}
	if hasShared {
		if err := h.InheritRepoID(ctx, shared); err != nil { // one repo id per checkout
			return InitOut{}, err
		}
	}
	repoID, err := h.EnsureRepoIdentity(ctx)
	if err != nil {
		return InitOut{}, err
	}
	ok = true
	out.addWarning(warnGitignore, h.EnsureGitignore(ctx))
	h.Prime(ctx) // the first fetch happens here, as v0.1.17's init did
	out.ProjectID, out.Name, out.RepoID = rowID, name, repoID
	if isLinked {
		out.LinkedRemote = linkedRemote
	}
	if mh, isMono := h.(MonorepoHost); isMono {
		out.OtherProjects = mh.OtherProjectFolders(ctx)
	}
	targets, fromSetup := in.Target, ""
	if len(targets) == 0 {
		// --target always wins; else the applying setup's tools; else
		// WireToolFiles' default (context.targets, found files, AGENTS.md).
		if st := h.SetupTools(ctx); len(st.Profiles) > 0 {
			targets, out.ToolsFromSetup, out.ToolFiles = st.Profiles, st.Setup, st.Files
			fromSetup = st.Setup
		}
	}
	lines, terr := h.WireToolFiles(ctx, targets, fromSetup)
	out.ToolLines = lines
	out.addWarning(warnToolFiles, terr)
	return out, nil
}

// alreadyInitializedHintFor is the hint of a second init: with --target it
// names the render command that wires those tools (D-3), else "nothing to do".
func alreadyInitializedHintFor(targets []string) string {
	var keep []string
	for _, t := range targets {
		if t = strings.TrimSpace(t); t != "" {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		return alreadyInitializedHint
	}
	return fmt.Sprintf(alreadyInitializedTargetHintF, strings.Join(keep, ","))
}

// projectFields is the projects row of a fresh init and the name it got:
// --name (trimmed, lower-cased), else Host.DefaultName; origin_url when
// origin has a URL (v0.1.17 wrote it too, for project list/show).
func projectFields(ctx context.Context, h Host, flagName string) (map[string]value.Value, string, error) {
	n := strings.ToLower(strings.TrimSpace(flagName))
	if n == "" {
		n = h.DefaultName(ctx)
	}
	if err := refuseControlChars(n); err != nil {
		return nil, "", err
	}
	t, err := value.TextOf(n)
	if err != nil {
		return nil, "", errs.Wrap(errs.InvalidInput, "init: project name", err)
	}
	fields := map[string]value.Value{FieldName: t}
	if origin := h.OriginURL(ctx); origin != "" {
		ov, oerr := value.TextOf(origin)
		if oerr != nil {
			return nil, "", errs.Wrap(errs.InvalidInput, "init: origin url", oerr)
		}
		fields[FieldOrigin] = ov
	}
	return fields, n, nil
}

// controlNames are the words for the control characters a name most often
// picks up by accident (a pasted line, a tab); the rest are named by code.
var controlNames = map[rune]string{'\n': "newline", '\r': "carriage return", '\t': "tab", 0: "NUL", 0x1b: "escape", 0x7f: "delete"}

// refuseControlChars refuses (E_INVALID_INPUT) a project name holding a
// control character (C0, DEL or C1), naming the first one: the name is
// printed in lists, status lines and the rendered context file, where a
// newline or an escape breaks the line it sits on. No length cap is set.
func refuseControlChars(name string) error {
	for _, r := range name {
		if !unicode.IsControl(r) {
			continue
		}
		what := fmt.Sprintf("U+%04X", r)
		if w, ok := controlNames[r]; ok {
			what += " (" + w + ")"
		}
		return errs.New(errs.InvalidInput, "init: the project name "+strconv.Quote(name)+" holds a control character, "+what).
			WithHint("give a name without control characters: lore init --name <name>")
	}
	return nil
}

// addWarning records a non-fatal failure (nil err: nothing).
func (o *InitOut) addWarning(what string, err error) {
	if err != nil {
		o.Warnings = append(o.Warnings, what+err.Error())
	}
}
