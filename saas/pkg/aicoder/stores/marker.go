package stores

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// MarkerFile is the store marker at a store's root (F12). Its presence is
// what makes a repo a store; `lore store init` writes it.
const MarkerFile = "lore-store.yaml"

// MarkerVersion is the marker format version written by this binary.
const MarkerVersion = 1

// RootFolder names a store's root project in config (`org_folder: .`).
const RootFolder = "."

// loreDir is the lore folder inside every project folder of a store.
const loreDir = ".lore"

// Allowed top-level files besides project folders (F12 "allowed" list).
// They exist because a store is still a repo people browse on the host: a
// README and LICENSE describe it, .gitattributes carries lore's merge
// driver, and lore's own optional merge workflow lives under
// .github/workflows/lore-*.yml.
const (
	allowedReadmePrefix  = "README"
	allowedLicensePrefix = "LICENSE"
	allowedGitattributes = ".gitattributes"
	allowedWorkflowDir   = ".github/workflows/"
	allowedWorkflowName  = "lore-"
	allowedWorkflowExt   = ".yml"
)

// maxListedOffenders is how many non-lore paths an E_NOT_A_STORE message
// names: enough to recognise a code repo, short enough to read.
const maxListedOffenders = 3

// Marker is the parsed lore-store.yaml.
type Marker struct {
	// LoreStore is the marker format version (MarkerVersion).
	LoreStore int `yaml:"lore_store"`
	// Created is the date the store was initialised (YYYY-MM-DD).
	Created string `yaml:"created,omitempty"`
	// OrgFolder is the folder suggested for the roster and company rules;
	// informational — the org entry's org_folder decides (F13).
	OrgFolder string `yaml:"org_folder,omitempty"`
}

// markerDateLayout is the Created field's format.
const markerDateLayout = "2006-01-02"

// NewMarker returns the marker `lore store init` writes.
func NewMarker(now time.Time, orgFolder string) Marker {
	return Marker{LoreStore: MarkerVersion, Created: now.UTC().Format(markerDateLayout), OrgFolder: orgFolder}
}

// Encode renders the marker as YAML.
func (m Marker) Encode() ([]byte, error) {
	b, err := yaml.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("stores: encode %s: %w", MarkerFile, err)
	}
	return b, nil
}

// ParseMarker parses lore-store.yaml. A marker without a positive
// lore_store version is not a marker.
func ParseMarker(b []byte) (Marker, error) {
	var m Marker
	if err := yaml.Unmarshal(b, &m); err != nil {
		return Marker{}, fmt.Errorf("stores: %s: %w", MarkerFile, err)
	}
	if m.LoreStore < 1 {
		return Marker{}, fmt.Errorf("stores: %s: lore_store must be a positive version", MarkerFile)
	}
	return m, nil
}

// ErrNotAStore is the sentinel inside every *NotAStoreError
// (errors.Is(err, ErrNotAStore)); the CLI maps it to E_NOT_A_STORE.
var ErrNotAStore = errors.New("stores: not a lore store")

// NotAStoreError says why a repo listed under stores: is refused.
type NotAStoreError struct {
	// URL is the store URL as configured (may be redacted by the caller).
	URL string
	// Branch is the store branch checked.
	Branch string
	// NoMarker is set when lore-store.yaml is missing or unreadable.
	NoMarker bool
	// Offenders are paths outside the allowed list (at most
	// maxListedOffenders), sorted.
	Offenders []string
	// Total is how many offending paths there were.
	Total int
}

// Error implements error.
func (e *NotAStoreError) Error() string {
	var parts []string
	if e.NoMarker {
		parts = append(parts, "no "+MarkerFile)
	}
	if len(e.Offenders) > 0 {
		more := ""
		if e.Total > len(e.Offenders) {
			more = fmt.Sprintf(" and %d more", e.Total-len(e.Offenders))
		}
		parts = append(parts, "it holds non-lore files such as "+strings.Join(e.Offenders, ", ")+more)
	}
	return fmt.Sprintf("%s (branch %s) is listed under stores: but is not a lore store (%s)", e.URL, e.Branch, strings.Join(parts, "; "))
}

// Unwrap makes errors.Is(err, ErrNotAStore) true.
func (e *NotAStoreError) Unwrap() error { return ErrNotAStore }

// CheckTree is the pure F12 check over every path on a store branch
// (root-relative, slash-separated, files only). requireMarker is false only
// for `lore store init`, which checks a branch before writing the marker.
// The returned error is a *NotAStoreError or nil; url and branch only feed
// the message.
func CheckTree(url, branch string, paths []string, requireMarker bool) error {
	var bad []string
	marker := false
	for _, p := range paths {
		if p == MarkerFile {
			marker = true
			continue
		}
		if !Allowed(p) {
			bad = append(bad, p)
		}
	}
	if (requireMarker && !marker) || len(bad) > 0 {
		sort.Strings(bad)
		e := &NotAStoreError{URL: url, Branch: branch, NoMarker: requireMarker && !marker, Total: len(bad)}
		e.Offenders = summarise(bad)
		return e
	}
	return nil
}

// summarise picks up to maxListedOffenders paths, showing a top-level
// folder once ("src/") rather than three of its files.
func summarise(bad []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range bad {
		show := p
		if i := strings.IndexByte(p, '/'); i >= 0 {
			show = p[:i+1]
		}
		if seen[show] {
			continue
		}
		seen[show] = true
		out = append(out, show)
		if len(out) == maxListedOffenders {
			break
		}
	}
	return out
}

// Allowed reports whether one path may exist in a store (F12): the marker,
// README*, LICENSE*, .gitattributes, .github/workflows/lore-*.yml, and
// anything under a project's .lore/ folder (`<folder>/.lore/**` or, for the
// root project, `.lore/**`). A project folder is one path segment: nested
// folders would make two stores' folder names incomparable (F13).
func Allowed(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return false
	}
	if p == MarkerFile || p == allowedGitattributes {
		return true
	}
	if !strings.Contains(p, "/") {
		return strings.HasPrefix(p, allowedReadmePrefix) || strings.HasPrefix(p, allowedLicensePrefix)
	}
	if rest, ok := strings.CutPrefix(p, allowedWorkflowDir); ok {
		return !strings.Contains(rest, "/") && strings.HasPrefix(rest, allowedWorkflowName) && strings.HasSuffix(rest, allowedWorkflowExt)
	}
	if strings.HasPrefix(p, loreDir+"/") {
		return true
	}
	first, rest, _ := strings.Cut(p, "/")
	return first != "" && first != loreDir && strings.HasPrefix(rest, loreDir+"/")
}

// ProjectPrefix returns the tree prefix of a store project folder:
// "<folder>/.lore/" or ".lore/" for the root project.
func ProjectPrefix(folder string) string {
	if folder == RootFolder || folder == "" {
		return loreDir + "/"
	}
	return folder + "/" + loreDir + "/"
}

// DataPrefix returns "<folder>/.lore/data/" (or ".lore/data/").
func DataPrefix(folder string) string { return ProjectPrefix(folder) + dataSubdir + "/" }

// dataSubdir is the row folder inside .lore (lsync.DataDirName).
const dataSubdir = "data"

// metaFileName is a project's identity and link file (lsync.MetaFileName).
const metaFileName = "_meta.json"

// MetaPath returns "<folder>/.lore/data/_meta.json".
func MetaPath(folder string) string { return DataPrefix(folder) + metaFileName }

// FoldersOf lists every project folder present in a store tree (paths are
// root-relative files): a folder with any file under <folder>/.lore/data/,
// and RootFolder when .lore/data/ exists at the root. Sorted.
func FoldersOf(paths []string) []string {
	set := map[string]bool{}
	for _, p := range paths {
		if strings.HasPrefix(p, DataPrefix(RootFolder)) {
			set[RootFolder] = true
			continue
		}
		first, rest, ok := strings.Cut(p, "/")
		if ok && strings.HasPrefix(rest, loreDir+"/"+dataSubdir+"/") {
			set[first] = true
		}
	}
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}
