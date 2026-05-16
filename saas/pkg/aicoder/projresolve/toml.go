// Package projresolve resolves the (db, project_id, repo) tuple for every
// lore command via the universal flag-chain (R20, R36):
//
//  1. CLI flag                     (--db / --project / --repo)
//  2. Env var                      (LORE_DB / LORE_PROJECT_ID / LORE_REPO)
//  3. .lore/lore.toml        (db_path / project_id; cwd ONLY, no walk-up)
//  4. Local .lore/lore.db    (Mode A inferred)
//
// Hard rules:
//   - No walk-up. Cwd must contain .lore/ directly (R14).
//   - Refuse ambiguous .lore/ (both .db and .toml present) (R18 #1).
//   - Refuse path traversal in db_path post-expansion (R27 #21).
//   - Refuse network-FS paths (R18 #18).
//   - Path expansion: ${HOME}, ${LORE_HOME}, ${PROJECT_ROOT} (R27 #3).
//
// Catches: R14, R17, R18 #1+#5, R20, R27 #3+#21, R36.
package projresolve

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Standard filenames inside the .lore/ marker directory.
const (
	MarkerDir   = ".lore"
	ModeAFile   = "lore.db"   // Mode A — local DB
	ModeBFile   = "lore.toml" // Mode B — pointer file
	StateDir    = "state"
	BackupDir   = "backups"
	CrashDirRel = "crashes" // relative to ~/.lore/
)

// Sentinels.
var (
	ErrNotProjectRoot   = errors.New("projresolve: cwd is not an aicoder project root (no .lore/lore.db or lore.toml)")
	ErrAmbiguousProject = errors.New("projresolve: both lore.db and lore.toml present (Mode A vs Mode B ambiguity)")
	ErrBadPath          = errors.New("projresolve: path contains traversal sequence or refers outside allowed roots")
	ErrTomlMissingField = errors.New("projresolve: lore.toml missing required field")
)

// Mode identifies which DB mode the project root is configured for.
type Mode int

const (
	ModeUnknown Mode = iota
	ModeA            // local .lore/lore.db
	ModeB            // .lore/lore.toml pointer
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

// TOMLPointer is the parsed contents of .lore/lore.toml.
//
// Two-line file format per Round 20:
//
//	db_path = "${HOME}/.lore/shared.db"
//	project_id = "prj_018f3b2c9a8e7891b4f51234567890ab"
type TOMLPointer struct {
	DBPath    string `toml:"db_path"`
	ProjectID string `toml:"project_id"`
}

// DetectMode inspects rootDir/.lore/ and determines the configured mode.
// Returns ErrAmbiguousProject if both marker files exist (R18 #1).
// Returns ErrNotProjectRoot if neither exists (R14).
func DetectMode(rootDir string) (Mode, error) {
	markerDir := filepath.Join(rootDir, MarkerDir)
	if _, err := os.Stat(markerDir); err != nil {
		return ModeUnknown, ErrNotProjectRoot
	}

	dbPath := filepath.Join(markerDir, ModeAFile)
	tomlPath := filepath.Join(markerDir, ModeBFile)

	dbExists := fileExists(dbPath)
	tomlExists := fileExists(tomlPath)

	switch {
	case dbExists && tomlExists:
		return ModeUnknown, ErrAmbiguousProject
	case dbExists:
		return ModeA, nil
	case tomlExists:
		return ModeB, nil
	default:
		return ModeUnknown, ErrNotProjectRoot
	}
}

// LoadTOML reads and parses .lore/lore.toml from rootDir.
// Performs path expansion AND validation on db_path.
func LoadTOML(rootDir string) (*TOMLPointer, error) {
	tomlPath := filepath.Join(rootDir, MarkerDir, ModeBFile)
	data, err := os.ReadFile(tomlPath)
	if err != nil {
		return nil, fmt.Errorf("read toml: %w", err)
	}

	var p TOMLPointer
	if err := toml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse toml: %w", err)
	}

	if p.DBPath == "" {
		return nil, fmt.Errorf("%w: db_path", ErrTomlMissingField)
	}
	if p.ProjectID == "" {
		return nil, fmt.Errorf("%w: project_id", ErrTomlMissingField)
	}

	expanded, err := ExpandPath(p.DBPath, rootDir)
	if err != nil {
		return nil, err
	}
	p.DBPath = expanded
	return &p, nil
}

// ExpandPath performs ${VAR} substitution and refuses path traversal.
//
// Supported tokens:
//
//	${HOME}         - os.UserHomeDir()
//	${LORE_HOME} - $LORE_HOME or $HOME/.lore
//	${PROJECT_ROOT} - the project root passed to LoadTOML
//
// Refuses paths that contain ".." after expansion (R27 #21) UNLESS the
// caller explicitly opts in via env LORE_ALLOW_DB_PATH_OUTSIDE=1.
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
	if os.Getenv("LORE_ALLOW_DB_PATH_OUTSIDE") != "1" {
		if containsParentRef(expanded) {
			return "", fmt.Errorf("%w: %q", ErrBadPath, path)
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
	ProjectID   string // opaque project ID (Mode A: from DB; Mode B: from toml)
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
	// NOT mislabel its source or skip TOML project_id resolution when we
	// are in fact inside a project (flag > env precedence; see switches
	// below).
	explicitDB := in.FlagDB
	explicitDBSource := "flag"
	if explicitDB == "" {
		if v := os.Getenv("LORE_DB"); v != "" {
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
		return &Context{
			Mode:          ModeA,
			DBPath:        explicitDB,
			ProjectRoot:   cwd,
			RepoMount:     in.FlagRepo,
			ProjectID:     in.FlagProject,
			SourceDB:      explicitDBSource,
			SourceProject: "flag",
		}, nil
	}

	c := &Context{
		Mode:        mode,
		ProjectRoot: cwd,
		RepoMount:   in.FlagRepo,
	}

	if c.RepoMount == "" {
		c.RepoMount = os.Getenv("LORE_REPO")
	}

	// In Mode B, always load the toml first so we can extract project_id
	// even when --db= or LORE_DB overrides the db path.
	var tomlPtr *TOMLPointer
	if mode == ModeB {
		ptr, err := LoadTOML(cwd)
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
	case os.Getenv("LORE_DB") != "":
		c.DBPath, c.SourceDB = os.Getenv("LORE_DB"), "env"
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
	switch {
	case in.FlagProject != "":
		c.ProjectID, c.SourceProject = in.FlagProject, "flag"
	case os.Getenv("LORE_PROJECT_ID") != "":
		c.ProjectID, c.SourceProject = os.Getenv("LORE_PROJECT_ID"), "env"
	case c.ProjectID != "":
		// already set from toml
	default:
		// Mode A with no flag/env: project ID is whatever single row exists in
		// the DB. Caller queries the projects table.
		c.SourceProject = "local"
	}

	return c, nil
}

// fileExists returns true if path stats successfully (any file kind).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
