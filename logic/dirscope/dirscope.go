// Package dirscope is the pure grammar of knowledge scopes (CONTEXT_BUDGET
// 6.2): the --scope spellings, directory paths (relative to the repo root,
// forward slashes) and the general-to-specific order a narrower row needs
// before it may --overrides a wider one.
//
//	--scope value ─ Parse ─► Spec{Kind, Path}
//	                              │
//	row fields ─► Place{Repo, Path} ─ Wider(a, b) ─► may b override a?
//	file path  ─► Chain(dirs, file) ─► the dir scopes that cover the file
//
// Layer L1: no I/O; callers bring the rows and the disk.
package dirscope

import (
	"path"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Kind is one of the six scopes (6.2.1); glob is not a --scope value (it is
// the rule's --globs field, 6.2.3).
type Kind string

// Kinds.
const (
	// KindDefault: no --scope given; the command keeps today's default.
	KindDefault Kind = ""
	KindGlobal  Kind = "global"
	KindOrg     Kind = "org"
	KindProject Kind = "project"
	KindRepo    Kind = "repo"
	KindDir     Kind = "dir"
)

// dirPrefix opens a directory scope value ("dir:src/booking").
const dirPrefix = "dir:"

// Spec is a parsed --scope value.
type Spec struct {
	Kind Kind
	// Path is the clean directory for KindDir; Org is the org named by
	// "org:<name>" ("" for a bare "org").
	Path string
	Org  string
}

// Label is the spelling the add line prints ("dir:src/booking").
func (s Spec) Label() string {
	switch s.Kind {
	case KindDir:
		return dirPrefix + s.Path
	case KindOrg:
		if s.Org != "" {
			return string(KindOrg) + ":" + s.Org
		}
	case KindDefault, KindGlobal, KindProject, KindRepo:
	}
	return string(s.Kind)
}

// Parse reads a --scope value: global | org[:<org>] | project | repo |
// dir:<path>. "" is KindDefault.
func Parse(raw string) (Spec, error) {
	v := strings.TrimSpace(raw)
	switch {
	case v == "":
		return Spec{}, nil
	case v == string(KindGlobal), v == string(KindProject), v == string(KindRepo), v == string(KindOrg):
		return Spec{Kind: Kind(v)}, nil
	case strings.HasPrefix(v, string(KindOrg)+":"):
		return Spec{Kind: KindOrg, Org: strings.TrimPrefix(v, string(KindOrg)+":")}, nil
	case strings.HasPrefix(v, dirPrefix):
		p, err := Clean(strings.TrimPrefix(v, dirPrefix))
		if err != nil {
			return Spec{}, err
		}
		return Spec{Kind: KindDir, Path: p}, nil
	}
	return Spec{}, errs.New(errs.InvalidInput, "--scope "+v+": not one of global, org[:<org>], project, repo, dir:<path>")
}

// Clean normalises a directory path given relative to the repo root: forward
// slashes, no "./", no trailing slash. It refuses an empty path, the root
// itself, an absolute path and any path that leaves the repo (6.2.3).
func Clean(raw string) (string, error) {
	p := strings.ReplaceAll(strings.TrimSpace(raw), `\`, "/")
	if p == "" {
		return "", errs.New(errs.InvalidInput, "dir scope: empty path").WithHint("name a folder relative to the repo root, like dir:src/booking")
	}
	if strings.HasPrefix(p, "/") || (len(p) > 1 && p[1] == ':') {
		return "", errs.New(errs.InvalidInput, "dir scope "+raw+": the path must be relative to the repo root")
	}
	c := path.Clean(p)
	if c == "." {
		return "", errs.New(errs.InvalidInput, "dir scope "+raw+": that is the repo root").WithHint("use --scope repo for the whole repo")
	}
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", errs.New(errs.InvalidInput, "dir scope "+raw+": the path is outside the repo")
	}
	return c, nil
}

// Under reports whether p is dir or a path inside it.
func Under(p, dir string) bool { return p == dir || strings.HasPrefix(p, dir+"/") }

// Place is where a row attaches, from its fields: Repo is repo_id ("" =
// project-wide), Path the directory scope's path ("" = none).
type Place struct {
	Repo string
	Path string
}

// rank orders the scopes general to specific (6.2.2): project, repo, then
// directories by depth.
func (p Place) rank() int {
	switch {
	case p.Repo == "":
		return 0
	case p.Path == "":
		return 1
	}
	return 2 + strings.Count(p.Path, "/")
}

// Wider reports whether wide is a strictly more general scope that covers
// narrow (6.2.2): only such a row may be named by narrow's --overrides.
// A project row covers every repo of it; a repo row covers its folders; a
// folder covers its subfolders. Rows of two different repos never cover
// each other.
func Wider(wide, narrow Place) bool {
	if wide.rank() >= narrow.rank() {
		return false
	}
	if wide.Repo == "" {
		return true
	}
	if wide.Repo != narrow.Repo {
		return false
	}
	return wide.Path == "" || Under(narrow.Path, wide.Path)
}

// Chain returns the directories of dirs that cover file, outermost first
// (6.2.2 step 5). file is a slash path relative to the repo root.
func Chain(dirs []string, file string) []string {
	var out []string
	for _, d := range dirs {
		if Under(file, d) {
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return strings.Count(a, "/") - strings.Count(b, "/") })
	return out
}

// Parent is the nearest directory of dirs strictly above d ("" = the root).
func Parent(dirs []string, d string) string {
	best := ""
	for _, x := range dirs {
		if x != d && Under(d, x) && len(x) > len(best) {
			best = x
		}
	}
	return best
}

// Moved returns p relabelled from old to new when p is old or under it
// (`lore scope move`, 6.2.3); ok is false for a path outside old.
func Moved(p, oldDir, newDir string) (string, bool) {
	if !Under(p, oldDir) {
		return "", false
	}
	return newDir + strings.TrimPrefix(p, oldDir), true
}

// Slug is d as one file-name token for root files that stand for a folder
// (.cursor/rules/lore-dir-<slug>.mdc, CQ11): slashes become "--".
func Slug(d string) string { return strings.ReplaceAll(d, "/", "--") }
