// Package importscan reads an existing lore install and proposes setups to
// import (owner request "Import from existing lore", fix-all item 2). It is
// strictly read-only: every source is opened for reading, machine.db is
// read from a private temporary copy (a WAL database opened in place would
// touch its -shm file), and nothing under the lore home or a repo is ever
// created, renamed or written. TestScanWritesNothing pins this by hashing
// every scanned file and folder listing before and after a scan.
//
//	Scan ─▶ <lore home>/config.yaml        identity, personal.store, orgs[], repo_orgs, setups[]
//	     ─▶ identity.yaml(.migrated), identity.toml(.migrated)   v0.1.17 and older identities
//	     ─▶ state/machine.db (copy)         known_repos roots
//	     ─▶ picked folders (depth-limited)  folders holding .lore/
//	     ─▶ each repo's .lore/stores.yaml   store urls
//	     ─▶ propose (proposal.go)           one solo setup + one member setup per org
//
// Every proposed value carries the file and line it came from (Field), so
// the page can show provenance next to each value.
package importscan

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// File names under the lore home.
const (
	ConfigFile       = "config.yaml"
	IdentityYAML     = "identity.yaml"
	IdentityTOML     = "identity.toml"
	MigratedSuffix   = ".migrated"
	MachineDBFile    = "state/machine.db"
	RepoStoresFile   = ".lore/stores.yaml"
	loreDir          = ".lore"
	defaultDepth     = 4
	maxDepth         = 8
	defaultMaxRepos  = 200
	maxFileBytes     = 1 << 20 // a config file larger than this is not lore's
	tomlCommentStart = "#"
)

// State is what a source looked like (closed set; the page labels each).
type State string

// Source states.
const (
	StateFound      State = "found"
	StateMissing    State = "missing"
	StateEmpty      State = "empty"
	StateMalformed  State = "malformed"
	StateUnreadable State = "unreadable"
)

// Source kinds (closed set).
const (
	KindConfig      = "config"
	KindIdentity    = "identity"
	KindIdentityOld = "identity_legacy"
	// KindIdentityBackup is a *.migrated copy of an old identity file: the
	// backup lore keeps after moving that file into config.yaml. Its data
	// is already imported, so it is listed (for the details) and never
	// proposed, and it never makes the banner say "existing lore install"
	// (owner report 2026-11-07: a purged setup came back from
	// identity.yaml.migrated as an import offer).
	KindIdentityBackup = "identity_backup"
	KindMachineDB      = "machine_db"
	KindStores         = "repo_stores"
	KindFolder         = "scan_folder"
)

// Repo origins.
const (
	FromMachineDB = "machine.db"
	FromScan      = "scan"
)

// Source is one file or folder the scan read and what it found there.
type Source struct {
	Kind   string
	Path   string
	State  State
	Detail string
}

// Field is one proposed value and where it came from. Line is 1-based, 0
// when the source has no lines (a machine.db row, a folder walk).
type Field struct {
	Field string
	Value string
	File  string
	Line  int
	Note  string
}

// RepoStore is one stores.yaml entry.
type RepoStore struct {
	Name string
	URL  string
	Line int
}

// Repo is a lore repo found on this machine.
type Repo struct {
	Root   string
	RepoID string
	From   string
	Org    string
	Stores []RepoStore
	// StoresFile is the stores.yaml path when it exists.
	StoresFile string
}

// Proposal is one setup to import: the entry to write and every field's
// provenance.
type Proposal struct {
	Entry  onboard.SetupEntry
	Fields []Field
	Repos  []string
}

// Result is a scan's answer.
type Result struct {
	Sources   []Source
	Repos     []Repo
	Proposals []Proposal
	// Covered names the proposals left out because an existing setup
	// already covers them (same name, same org, or same personal store).
	Covered []string
	// Truncated is set when the folder walk stopped at MaxRepos.
	Truncated bool
}

// Options are a scan's inputs.
type Options struct {
	// LoreHome is the lore home (~/.lore); Home the user's home, used to
	// expand and write folders with ~.
	LoreHome string
	Home     string
	// Folders are extra folders to walk for repos; Depth bounds the walk
	// (0: 4, at most 8); MaxRepos bounds the repos found (0: 200).
	Folders  []string
	Depth    int
	MaxRepos int
	// TempDir holds the private machine.db copy ("" = os.TempDir()).
	TempDir string
}

// Scan reads every source and builds the proposals. A missing or broken
// source is reported in Sources, never an error: only a cancelled ctx is.
func Scan(ctx context.Context, o Options) (Result, error) {
	if o.Depth <= 0 {
		o.Depth = defaultDepth
	}
	o.Depth = min(o.Depth, maxDepth)
	if o.MaxRepos <= 0 {
		o.MaxRepos = defaultMaxRepos
	}
	var res Result
	cfg := readConfig(filepath.Join(o.LoreHome, ConfigFile), &res)
	ids := readIdentities(o.LoreHome, &res)
	repos := readMachineRepos(ctx, filepath.Join(o.LoreHome, filepath.FromSlash(MachineDBFile)), o.TempDir, &res)
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	seen := map[string]bool{}
	for _, r := range repos {
		seen[r.Root] = true
	}
	for _, f := range o.Folders {
		found, truncated := walkFolder(ctx, expandHome(strings.TrimSpace(f), o.Home), o.Depth, o.MaxRepos-len(repos), seen, &res)
		repos = append(repos, found...)
		res.Truncated = res.Truncated || truncated
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
	}
	// Path order, so a scan of the same machine lists the repos, their
	// stores.yaml sources and the proposals' folders the same way every
	// run (machine.db's rows come in random-id order).
	slices.SortStableFunc(repos, func(a, b Repo) int { return strings.Compare(a.Root, b.Root) })
	for i := range repos {
		readRepoStores(&repos[i], &res)
		if org, ok := cfg.repoOrgs[repos[i].RepoID]; ok && repos[i].RepoID != "" {
			repos[i].Org = org.value
		}
	}
	res.Repos = repos
	res.Proposals, res.Covered = propose(cfg, ids, repos, o.Home)
	return res, nil
}

// readFile reads a small file; state says why it gave nothing.
func readFile(path string) ([]byte, State, string) {
	st, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, StateMissing, ""
	}
	if err != nil {
		return nil, StateUnreadable, "cannot be read"
	}
	if !st.Mode().IsRegular() {
		return nil, StateUnreadable, "not a regular file"
	}
	if st.Size() > maxFileBytes {
		return nil, StateUnreadable, "larger than 1 MiB"
	}
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, StateUnreadable, "cannot be read"
	}
	if strings.TrimSpace(string(b)) == "" {
		return nil, StateEmpty, ""
	}
	return b, StateFound, ""
}

// parseYAML reads b as one YAML mapping document.
func parseYAML(b []byte) (*yaml.Node, string) {
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, "not valid YAML"
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, "not a YAML mapping"
	}
	return doc.Content[0], ""
}

// get returns the value node of key in mapping m (nil when absent).
func get(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// located is a scalar value and its line.
type located struct {
	value string
	line  int
}

// scalar is the trimmed scalar at key in m.
func scalar(m *yaml.Node, key string) located {
	n := get(m, key)
	if n == nil || n.Kind != yaml.ScalarNode {
		return located{}
	}
	return located{value: strings.TrimSpace(n.Value), line: n.Line}
}

// expandHome expands a leading ~ with home.
func expandHome(p, home string) string { return onboard.ExpandHome(p, home) }

// tildePath writes p under home as ~/….
func tildePath(p, home string) string {
	if home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, strings.TrimSuffix(home, string(filepath.Separator))+string(filepath.Separator)); ok {
		return "~/" + filepath.ToSlash(rest)
	}
	return p
}
