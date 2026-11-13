// Package reveal defines `lore machine reveal <target>` and `lore machine
// path list` (layer L5; owner request 2026-11-08): show lore's own files
// in the file manager (Finder on macOS) from the setup page or the CLI.
//
//	machine.path.list         Port.MachinePath for every Target ─► paths + exists
//	machine.reveal <target>   refuse a REST call that is not the setup page
//	                          ─► Port.MachinePath ─► missing: E_NOT_FOUND
//	                          ─► Argv (per platform) ─► Port.Open (no shell)
//
// The input is a closed set of targets, never a path: a caller can only
// ever make lore open ~/.lore, ~/.lore/config.yaml or the current repo's
// .lore, whatever it sends.
package reveal

import (
	"context"
	"path/filepath"
	"runtime"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Operation names.
const (
	OpReveal   = "machine.reveal"
	OpPathList = "machine.path.list"
)

// Target names one of lore's files: a closed set (Targets).
type Target string

// The targets, each resolved by the composition root.
const (
	// TargetLoreHome is the lore home folder (~/.lore).
	TargetLoreHome Target = "lore_home"
	// TargetConfigFile is ~/.lore/config.yaml.
	TargetConfigFile Target = "config_file"
	// TargetRepoLore is the .lore folder of the git repo lore runs in.
	TargetRepoLore Target = "repo_lore"
)

// Targets lists every target, in the order the page shows them.
func Targets() []Target { return []Target{TargetLoreHome, TargetConfigFile, TargetRepoLore} }

// EnumValues lists every target (op.Enumerated).
func (Target) EnumValues() []string {
	ts := Targets()
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = string(t)
	}
	return out
}

// Valid reports a known target.
func (t Target) Valid() bool {
	for _, k := range Targets() {
		if k == t {
			return true
		}
	}
	return false
}

// Place is a resolved target: its absolute path, whether it exists, and
// whether it is a folder.
type Place struct {
	Path   string
	Exists bool
	IsDir  bool
}

// Port is what the operations need; *wire.App implements it.
type Port interface {
	// MachinePath resolves t on this machine. Path is "" when t has no
	// place here (repo_lore outside a git repo); never an error for that.
	MachinePath(ctx context.Context, t Target) (Place, error)
	// Open starts argv (a fixed opener and one resolved path; no shell)
	// and does not wait for it.
	Open(ctx context.Context, argv []string) error
}

// Platform names and openers (Argv).
const (
	goosDarwin   = "darwin"
	goosWindows  = "windows"
	openDarwin   = "open"
	revealDarwin = "-R" // open -R: reveal the file in Finder, selected
	openUnix     = "xdg-open"
	openWindows  = "explorer"
	// selectWindows is explorer's "open the parent and select this" flag.
	selectWindows = "/select,"
	envDisplay    = "DISPLAY"
	envWayland    = "WAYLAND_DISPLAY"
)

// Argv is the command that shows p in the file manager on goos: a folder
// is opened, a file is revealed in its folder (selected where the
// platform can: macOS open -R, Windows explorer /select,; xdg-open opens
// the file's folder). false: no display to open it on (a Linux or BSD
// session without DISPLAY or WAYLAND_DISPLAY, e.g. over ssh). getenv is a
// parameter so every platform's branch is tested everywhere.
func Argv(goos string, p Place, getenv func(string) string) ([]string, bool) {
	switch goos {
	case goosDarwin:
		if p.IsDir {
			return []string{openDarwin, p.Path}, true
		}
		return []string{openDarwin, revealDarwin, p.Path}, true
	case goosWindows:
		if p.IsDir {
			return []string{openWindows, p.Path}, true
		}
		return []string{openWindows, selectWindows + p.Path}, true
	}
	if getenv(envDisplay) == "" && getenv(envWayland) == "" {
		return nil, false
	}
	if p.IsDir {
		return []string{openUnix, p.Path}, true
	}
	return []string{openUnix, filepath.Dir(p.Path)}, true
}

// RevealIn is machine.reveal's input.
type RevealIn struct {
	Target Target `json:"target" arg:"0" desc:"lore_home (~/.lore), config_file (~/.lore/config.yaml) or repo_lore (this repo's .lore)"`
}

// RevealOut is the target shown and its path.
type RevealOut struct {
	Target Target `json:"target"`
	Path   string `json:"path"`
}

// Text implements op.Texter.
func (o RevealOut) Text() string { return "✓ opened " + o.Path + "\n" }

// PathListIn is machine.path.list's (empty) input.
type PathListIn struct{}

// PathRow is one target's path; Path is "" when it has no place here.
type PathRow struct {
	Target Target `json:"target"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

// PathListOut is every target's path.
type PathListOut struct {
	Paths []PathRow `json:"paths"`
}

// Text implements op.Texter.
func (o PathListOut) Text() string {
	s := ""
	for _, r := range o.Paths {
		p := r.Path
		switch {
		case p == "":
			p = "(none here)"
		case !r.Exists:
			p += " (missing)"
		}
		s += string(r.Target) + "\t" + p + "\n"
	}
	return s
}

// engine binds the ops to a port and the platform (injected in tests).
type engine struct {
	p      Port
	goos   string
	getenv func(string) string
}

// errCodes are the codes the operations return.
var errCodes = []errs.Code{errs.InvalidInput, errs.NotFound, errs.Unsupported, errs.Internal}

// Ops returns the operations bound to p on this machine's platform.
func Ops(p Port, getenv func(string) string) []op.Entry {
	return engine{p: p, goos: runtime.GOOS, getenv: getenv}.ops()
}

func (e engine) ops() []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpReveal, Summary: "Show one of lore's files in the file manager (Finder): lore_home, config_file or repo_lore; never another path",
			Permission: op.PermRead, Access: op.AccessRead, Errors: errCodes, Local: true,
			Examples: []op.Example{{Title: "show config.yaml in Finder", CLI: "lore machine reveal --target config_file", Want: ""}},
		}, e.reveal),
		op.Define(op.Spec{
			Name: OpPathList, Summary: "List the paths of lore's own files on this machine (lore home, config.yaml, this repo's .lore)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: errCodes, Local: true,
			Examples: []op.Example{{Title: "where lore keeps its files", CLI: "lore machine path list", Want: "config_file"}},
		}, e.pathList),
	}
}

// reveal resolves the target and starts the opener. Over REST it runs only
// for the setup page (op.Page): `lore api` serves any local HTTP client,
// and opening windows on the person's screen is not for those.
func (e engine) reveal(ctx context.Context, in RevealIn) (RevealOut, error) {
	if !in.Target.Valid() {
		return RevealOut{}, errs.New(errs.InvalidInput, "machine reveal: unknown target "+string(in.Target)).
			WithHint("one of lore_home, config_file, repo_lore")
	}
	if op.Remote(ctx) && !op.Page(ctx) {
		return RevealOut{}, errs.New(errs.Unsupported, "machine reveal runs from the setup page or the CLI, not over lore api").
			WithHint("lore machine path list prints the paths")
	}
	pl, err := e.p.MachinePath(ctx, in.Target)
	if err != nil {
		return RevealOut{}, err
	}
	switch {
	case pl.Path == "":
		return RevealOut{}, errs.New(errs.NotFound, "machine reveal: "+string(in.Target)+" has no place here (not inside a git repo)").
			WithHint("run lore setup inside a repo")
	case !pl.Exists:
		return RevealOut{}, errs.New(errs.NotFound, "machine reveal: "+pl.Path+" does not exist").
			WithHint(missingHint(in.Target))
	}
	argv, ok := Argv(e.goos, pl, e.getenv)
	if !ok {
		return RevealOut{}, errs.New(errs.Unsupported, "machine reveal: no display to open "+pl.Path+" on (an ssh session?)").
			WithHint("copy the path instead: " + pl.Path)
	}
	if err := e.p.Open(ctx, argv); err != nil {
		return RevealOut{}, err
	}
	return RevealOut{Target: in.Target, Path: pl.Path}, nil
}

// missingHint is the next step when t's path does not exist yet.
func missingHint(t Target) string {
	switch t {
	case TargetRepoLore:
		return "lore init creates .lore in this repo"
	case TargetConfigFile:
		return "lore config set or lore setup creates it"
	case TargetLoreHome:
	}
	return "lore setup creates it"
}

// pathList resolves every target.
func (e engine) pathList(ctx context.Context, _ PathListIn) (PathListOut, error) {
	out := PathListOut{Paths: make([]PathRow, 0, len(Targets()))}
	for _, t := range Targets() {
		pl, err := e.p.MachinePath(ctx, t)
		if err != nil {
			return PathListOut{}, err
		}
		out.Paths = append(out.Paths, PathRow{Target: t, Path: pl.Path, Exists: pl.Exists})
	}
	return out, nil
}
