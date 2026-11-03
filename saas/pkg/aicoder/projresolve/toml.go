// Package projresolve resolves the (db, project_id, repo) tuple for every
// lore command via the universal flag-chain (R20, R36):
//
//  1. CLI flag                     (--db / --project / --repo)
//  2. Env var                      (LORE_DB / LORE_PROJECT_ID / LORE_REPO)
//  3. .lore/lore.yaml        (db_path / project_id; cwd ONLY, no walk-up;
//     a legacy .lore/lore.toml is migrated to it on first read — lorecfg)
//  4. Local .lore/lore.db    (Mode A inferred)
//
// Hard rules:
//   - No walk-up. Cwd must contain .lore/ directly (R14).
//   - Refuse ambiguous .lore/ (lore.db AND a lore.yaml/lore.toml pointer) (R18 #1).
//   - Refuse path traversal in db_path post-expansion (R27 #21).
//   - Refuse network-FS paths (R18 #18).
//   - Path expansion: ${HOME}, ${LORE_HOME}, ${PROJECT_ROOT} (R27 #3).
//
// Catches: R14, R17, R18 #1+#5, R20, R27 #3+#21, R36.
package projresolve

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/lorecfg"
)

// Standard filenames inside the .lore/ marker directory.
const (
	MarkerDir = ".lore"
	ModeAFile = "lore.db" // Mode A — local DB
	// ModeBBase is the Mode B pointer's base name; lorecfg adds the extension.
	ModeBBase = "lore"
	// ModeBFile is the Mode B pointer file every writer produces.
	ModeBFile = ModeBBase + lorecfg.ExtYAML
	// ModeBLegacyFile is the pre-YAML pointer. Still read (and migrated to
	// ModeBFile on first read), so it still marks a project for detection.
	ModeBLegacyFile = ModeBBase + lorecfg.ExtTOML
	StateDir        = "state"
	BackupDir       = "backups"
	CrashDirRel     = "crashes" // relative to ~/.lore/
)

// Environment variables of the resolution chain (step 2, between the flags
// and the pointer file). Each mirrors one flag: --db, --project, --repo.
const (
	EnvDB        = "LORE_DB"
	EnvProjectID = "LORE_PROJECT_ID"
	EnvRepo      = "LORE_REPO"
)

// EnvAllowDBPathOutside is the one-command opt-in that lets db_path contain a
// ".." segment (R27 #21). It is named in the E_BAD_PATH hint, so the refusal
// tells the user exactly how to let the path through when they mean it.
const EnvAllowDBPathOutside = "LORE_ALLOW_DB_PATH_OUTSIDE"

// badPathHint is the next step printed under an E_BAD_PATH refusal of db_path.
const badPathHint = "write db_path in .lore/lore.yaml without a `..` part, or set " +
	EnvAllowDBPathOutside + "=1 for one command if the path really is meant to leave the project"

// Sentinels.
var (
	ErrNotProjectRoot      = errors.New("projresolve: cwd is not an aicoder project root (no .lore/lore.db or lore.yaml)")
	ErrAmbiguousProject    = errors.New("projresolve: both lore.db and a lore.yaml/lore.toml pointer present (Mode A vs Mode B ambiguity)")
	ErrBadPath             = errors.New("projresolve: path contains traversal sequence or refers outside allowed roots")
	ErrPointerMissingField = errors.New("projresolve: lore.yaml missing required field")
)

// Mode identifies which DB mode the project root is configured for.
type Mode int

const (
	ModeUnknown Mode = iota
	ModeA            // local .lore/lore.db
	ModeB            // .lore/lore.yaml (or legacy lore.toml) pointer
)

// String returns the canonical mode name.
func (m Mode) String() string {
	switch m {
	case ModeA:
		return "A"
	case ModeB:
		return "B"
	default:
		return "unknown"
	}
}

// ProjectMarkerFiles lists every file inside .lore/ whose presence makes the
// directory a project (init refuses, materialize skips). The legacy TOML is
// included because an un-migrated project is still a project.
func ProjectMarkerFiles() []string {
	return []string{ModeAFile, ModeBFile, ModeBLegacyFile}
}

// Pointer is the parsed contents of .lore/lore.yaml.
//
// Two-key file format per Round 20:
//
//	db_path: ${HOME}/.lore/shared.db
//	project_id: prj_018f3b2c9a8e7891b4f51234567890ab
//
// The toml tags read a legacy lore.toml in place when its migration could
// not be written (lorecfg falls back to the old file).
type Pointer struct {
	DBPath    string `yaml:"db_path" toml:"db_path"`
	ProjectID string `yaml:"project_id" toml:"project_id"`
}

// Validate is cfgkit's post-load hook: both keys are required.
func (p *Pointer) Validate() error {
	var errs []error
	if p.DBPath == "" {
		errs = append(errs, fmt.Errorf("%w: db_path", ErrPointerMissingField))
	}
	if p.ProjectID == "" {
		errs = append(errs, fmt.Errorf("%w: project_id", ErrPointerMissingField))
	}
	return errors.Join(errs...)
}

// RefFields lets a future secret-store resolver fill these values from a
// reference (lorecfg.RefResolver); with none registered they stay literal.
func (p *Pointer) RefFields() []*string { return []*string{&p.DBPath, &p.ProjectID} }

// DetectMode inspects rootDir/.lore/ and determines the configured mode.
// Returns ErrAmbiguousProject if both marker files exist (R18 #1).
// Returns ErrNotProjectRoot if neither exists (R14).
func DetectMode(rootDir string) (Mode, error) {
	markerDir := filepath.Join(rootDir, MarkerDir)
	if _, err := os.Stat(markerDir); err != nil {
		return ModeUnknown, ErrNotProjectRoot
	}

	dbExists := fileExists(filepath.Join(markerDir, ModeAFile))
	ptrExists := fileExists(filepath.Join(markerDir, ModeBFile)) ||
		fileExists(filepath.Join(markerDir, ModeBLegacyFile))

	switch {
	case dbExists && ptrExists:
		return ModeUnknown, ErrAmbiguousProject
	case dbExists:
		return ModeA, nil
	case ptrExists:
		return ModeB, nil
	default:
		return ModeUnknown, ErrNotProjectRoot
	}
}

// LoadPointer reads .lore/lore.yaml from rootDir (migrating a legacy
// lore.toml first, see lorecfg). Performs path expansion AND validation on
// db_path.
func LoadPointer(ctx context.Context, rootDir string) (*Pointer, error) {
	dir := filepath.Join(rootDir, MarkerDir)
	loc, err := lorecfg.Locate(ctx, dir, ModeBBase, lorecfg.Options{})
	if err != nil {
		return nil, fmt.Errorf("locate pointer: %w", err)
	}
	if !loc.Exists {
		return nil, ErrNotProjectRoot
	}
	p, err := lorecfg.Load[Pointer](ctx, loc, lorecfg.Options{})
	if err != nil {
		return nil, fmt.Errorf("load pointer: %w", err)
	}

	expanded, err := ExpandPath(p.DBPath, rootDir)
	if err != nil {
		return nil, err
	}
	p.DBPath = expanded
	return p, nil
}

// ExpandPath performs ${VAR} substitution and refuses path traversal.
//
// Supported tokens:
//
//	${HOME}         - os.UserHomeDir()
//	${LORE_HOME} - $LORE_HOME or $HOME/.lore
//	${PROJECT_ROOT} - the project root passed to LoadPointer
//
// Refuses paths that contain ".." after expansion (R27 #21) UNLESS the
// caller explicitly opts in via env LORE_ALLOW_DB_PATH_OUTSIDE=1. The refusal
// is an *errcodes.CLIError with code E_BAD_PATH and a hint naming the opt-in;
// it wraps ErrBadPath, so errors.Is(err, ErrBadPath) still holds.
func ExpandPath(path, projectRoot string) (string, error) {
	expanded := path

	if strings.Contains(expanded, "${HOME}") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ${HOME}: %w", err)
		}
		expanded = strings.ReplaceAll(expanded, "${HOME}", home)
	}

	if strings.Contains(expanded, "${LORE_HOME}") {
		ah := os.Getenv("LORE_HOME")
		if ah == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("expand ${LORE_HOME}: %w", err)
			}
			ah = filepath.Join(home, ".lore")
		}
		expanded = strings.ReplaceAll(expanded, "${LORE_HOME}", ah)
	}

	if strings.Contains(expanded, "${PROJECT_ROOT}") {
		expanded = strings.ReplaceAll(expanded, "${PROJECT_ROOT}", projectRoot)
	}

	// Refuse ANY ".." segment in the input path (R27 #21). We check the
	// original path AND the cleaned form because filepath.Clean resolves
	// embedded ".." (e.g., ./a/../b → b) which would silently allow
	// traversal-as-typed.
	if os.Getenv(EnvAllowDBPathOutside) != "1" {
		if containsParentRef(expanded) {
			return "", errcodes.Newf(errcodes.BadPath, "db_path %q has a `..` part", path).
				WithHint(badPathHint).
				WithCause(ErrBadPath)
		}
	}

	return expanded, nil
}

// containsParentRef returns true if any path segment is exactly "..".
// More precise than substring search ("..foo" is fine; "../foo" is not).
func containsParentRef(p string) bool {
	for _, seg := range strings.Split(p, string(filepath.Separator)) {
		if seg == ".." {
			return true
		}
	}
	return false
}

// Context is the resolved view used by every command.
type Context struct {
	Mode        Mode
	DBPath      string // resolved + expanded path to the SQLite file
	ProjectID   string // opaque project ID (Mode A: from DB; Mode B: from lore.yaml)
	ProjectRoot string // cwd at resolution time (for crash dumps + diagnostics)
	RepoMount   string // raw --repo value from caller; "" = master scope
	// SourceDB / SourceProject record which step in the chain provided each value.
	// Surface in `aicoder doctor` for diagnostic output.
	SourceDB      string // "flag" | "env" | "toml" | "local"
	SourceProject string // "flag" | "env" | "toml" | "local"
}

// Inputs collects raw values from CLI flags and environment.
// Caller (cobra root) populates these.
type Inputs struct {
	FlagDB      string
	FlagProject string
	FlagRepo    string
}

// Resolve computes a Context from cwd + flags + env per the universal chain.
// Returns ErrNotProjectRoot if cwd is not an aicoder project (no walk-up).
func Resolve(in Inputs) (*Context, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}

	// An explicit DB (--db flag or LORE_DB env) is the escape hatch for
	// worktree contexts, cross-project scripts, and recovery after .lore/
	// was wiped. It only *bypasses the project-root requirement* — it must
	// NOT mislabel its source or skip pointer project_id resolution when we
	// are in fact inside a project (flag > env precedence; see switches
	// below).
	explicitDB := in.FlagDB
	explicitDBSource := "flag"
	if explicitDB == "" {
		if v := os.Getenv(EnvDB); v != "" {
			explicitDB = v
			explicitDBSource = "env"
		}
	}

	mode, err := DetectMode(cwd)
	if err != nil {
		// Not inside a project. Permitted only when an explicit DB was
		// given — otherwise surface the not-a-project error.
		if explicitDB == "" {
			return nil, err
		}
		// The project id and repo mount follow the same flag > env chain
		// as inside a project; only the pointer/local steps are missing.
		c := &Context{
			Mode:        ModeA,
			DBPath:      explicitDB,
			ProjectRoot: cwd,
			RepoMount:   resolveRepoMount(in.FlagRepo),
			SourceDB:    explicitDBSource,
		}
		resolveProject(c, in.FlagProject)
		return c, nil
	}

	c := &Context{
		Mode:        mode,
		ProjectRoot: cwd,
		RepoMount:   resolveRepoMount(in.FlagRepo),
	}

	// In Mode B, always load the pointer first so we can extract project_id
	// even when --db= or LORE_DB overrides the db path. Resolve is called
	// from 15 CLI sites with no context, so the I/O gets a background one.
	var tomlPtr *Pointer
	if mode == ModeB {
		ptr, err := LoadPointer(context.Background(), cwd)
		if err != nil {
			return nil, err
		}
		tomlPtr = ptr
		c.ProjectID = ptr.ProjectID
		c.SourceProject = "toml"
	}

	// Resolve DB path: flag > env > toml/local.
	switch {
	case in.FlagDB != "":
		c.DBPath, c.SourceDB = in.FlagDB, "flag"
	case os.Getenv(EnvDB) != "":
		c.DBPath, c.SourceDB = os.Getenv(EnvDB), "env"
	default:
		switch mode {
		case ModeA:
			c.DBPath = filepath.Join(cwd, MarkerDir, ModeAFile)
			c.SourceDB = "local"
		case ModeB:
			c.DBPath = tomlPtr.DBPath
			c.SourceDB = "toml"
		default:
			return nil, ErrNotProjectRoot
		}
	}

	// Resolve project id: flag > env > toml (already loaded above for Mode B).
	resolveProject(c, in.FlagProject)
	return c, nil
}

// resolveRepoMount applies the repo-mount chain: --repo flag, then
// LORE_REPO. "" means master scope.
func resolveRepoMount(flagRepo string) string {
	if flagRepo != "" {
		return flagRepo
	}
	return os.Getenv(EnvRepo)
}

// resolveProject applies the project-id chain to c: --project flag, then
// LORE_PROJECT_ID, then whatever the pointer already put in c.ProjectID.
// With none of them the source is "local": the caller picks the single
// project in the database (or reports E_AMBIGUOUS_PROJECT). Every Resolve
// path goes through here, so an explicit --db / LORE_DB outside a project
// honours LORE_PROJECT_ID exactly like a project folder does.
func resolveProject(c *Context, flagProject string) {
	switch {
	case flagProject != "":
		c.ProjectID, c.SourceProject = flagProject, "flag"
	case os.Getenv(EnvProjectID) != "":
		c.ProjectID, c.SourceProject = os.Getenv(EnvProjectID), "env"
	case c.ProjectID != "":
		// already set from the pointer (Mode B); SourceProject says "toml".
	default:
		c.SourceProject = "local"
	}
}

// fileExists returns true if path stats successfully (any file kind).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
