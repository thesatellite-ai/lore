package setup

// folders.go — the Folders step's suggestions and folder browser (owner
// test 2026-11-07: the step offered only the gitconfig includeIf folders,
// which belonged to the other identity, and never the folder `lore setup`
// was started in).
//
//	setup.folders ─▶ FolderLister.FolderFacts (start folder, its git root, includeIf folders, setups) ─▶ SuggestFolders (pure)
//	setup.browse  ─▶ FolderLister.Browse (subfolders under the home folder; names only)
//
// Both are read-only. SuggestFolders orders: the start folder, its git
// root, its parent (all "where you ran lore setup", each only when
// startFolderFits; one another setup already claims is still offered,
// dimmed, labelled with that setup and never prefilled), then the includeIf
// folders of this setup's identity, then those of another identity
// (shown dimmed, never preselected: they are evidence of the other
// identity's folders, and hiding them would leave the person wondering why
// a folder git knows about is missing). Any row another setup's glob
// already covers says which setup.

import (
	"context"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// Op names (on the page allowlist, surface/setupweb allow.go).
const (
	OpFolders = "setup.folders"
	OpBrowse  = "setup.browse"
)

// FoldersIn is setup.folders' input.
type FoldersIn struct {
	// Setup is the setup being made or edited ("" = none yet).
	Setup string `json:"setup,omitempty" desc:"the setup the folders are for"`
	// Emails are this setup's identity emails when not saved yet (the
	// Identity step's answers); the saved identity counts too.
	Emails []string `json:"emails,omitempty" flag:"email" desc:"this setup's identity emails (adds to the saved ones)"`
}

// FolderKind is where a suggestion comes from (closed set).
type FolderKind string

// Folder suggestion kinds, in display order.
const (
	FolderHere         FolderKind = "here"          // the folder lore setup was started in
	FolderGitRoot      FolderKind = "git_root"      // its git repo's top folder
	FolderParent       FolderKind = "parent"        // the folder holding it
	FolderInclude      FolderKind = "include"       // a gitconfig includeIf folder of this identity
	FolderIncludeOther FolderKind = "include_other" // one of another identity: shown dimmed
)

// FolderKinds lists every folder kind in display order.
func FolderKinds() []FolderKind {
	return []FolderKind{FolderHere, FolderGitRoot, FolderParent, FolderInclude, FolderIncludeOther}
}

// EnumValues lists every folder kind (op.Enumerated): the OpenAPI
// document's enum for a field of this type. internal/layers pins it to the
// constants.
func (FolderKind) EnumValues() []string {
	out := make([]string, 0, len(FolderKinds()))
	for _, k := range FolderKinds() {
		out = append(out, string(k))
	}
	return out
}

// FolderSuggestion is one folder the step offers.
type FolderSuggestion struct {
	// Glob is what the setup saves for this row ("~/code/proj/**").
	Glob string `json:"glob"`
	// Folder is the folder itself, ~ form.
	Folder string     `json:"folder"`
	Kind   FolderKind `json:"kind"`
	// Label is the row's short source ("where you ran lore setup").
	Label string `json:"label"`
	// Email is the includeIf identity's email (include kinds).
	Email string `json:"email,omitempty"`
	// ClaimedBy is the other setup whose folders already cover this one
	// and ClaimedGlob its glob that does (both set or both empty); Note
	// says what picking it means (the folder would match two setups) and
	// the fix. The page builds its warning from these typed fields.
	ClaimedBy   string `json:"claimed_by,omitempty"`
	ClaimedGlob string `json:"claimed_glob,omitempty"`
	Note        string `json:"note,omitempty"`
	// Dim: offered but not a good pick for this setup (another identity's
	// folder, or a folder another setup already claims).
	Dim bool `json:"dim,omitempty"`
}

// FoldersOut is setup.folders' output.
type FoldersOut struct {
	Suggestions []FolderSuggestion `json:"suggestions"`
	// Start is the folder lore setup was started in (~ form).
	Start string `json:"start"`
}

// IncludeFolder is one gitconfig includeIf gitdir folder and the identity
// its file sets.
type IncludeFolder struct {
	Folder string
	Name   string
	Email  string
}

// FolderFacts is what SuggestFolders decides from (all read-only facts).
type FolderFacts struct {
	// Home is the home folder (absolute); Start and GitRoot are absolute
	// ("" GitRoot outside a repo).
	Home, Start, GitRoot string
	Includes             []IncludeFolder
	// Setup is the setup the folders are for; Emails its identity.
	Setup  string
	Emails []string
	// Setups are every saved setup (the one being edited is skipped).
	Setups []onboard.SetupEntry
}

// Labels of the suggestion kinds.
const (
	labelHere         = "where you ran lore setup"
	labelGitRoot      = "the git repo where you ran lore setup"
	labelParent       = "the folder holding where you ran lore setup"
	labelInclude      = "your gitconfig uses this identity here"
	labelIncludeOther = "used by your other identity "
	globSuffix        = "/**"
)

// slashed is f with every folder in slash form. The rules below work on
// slash paths (path.Clean, "/" joins, ~/ prefixes); on Windows the facts
// come with backslashes, and then no folder was ever written as ~/….
func (f FolderFacts) slashed() FolderFacts {
	f.Home, f.Start, f.GitRoot = filepath.ToSlash(f.Home), filepath.ToSlash(f.Start), filepath.ToSlash(f.GitRoot)
	inc := make([]IncludeFolder, len(f.Includes))
	for i, x := range f.Includes {
		x.Folder = filepath.ToSlash(x.Folder)
		inc[i] = x
	}
	f.Includes = inc
	return f
}

// SuggestFolders is the Folders step's rows, best first, each glob once.
func SuggestFolders(f FolderFacts) []FolderSuggestion {
	f = f.slashed()
	var out []FolderSuggestion
	seen := map[string]bool{}
	add := func(abs string, kind FolderKind, label, email string, dim bool) {
		abs = strings.TrimSuffix(path.Clean(abs), "/")
		if abs == "" || abs == "." || abs == "/" || abs == strings.TrimSuffix(f.Home, "/") {
			return // the whole disk or home is never a setup's folder
		}
		g := tildeForm(abs, f.Home) + globSuffix
		if seen[g] {
			return
		}
		seen[g] = true
		s := FolderSuggestion{Glob: g, Folder: tildeForm(abs, f.Home), Kind: kind, Label: label, Email: email, Dim: dim}
		if by, glob := claimedBy(abs, f); by != "" {
			s.ClaimedBy, s.ClaimedGlob, s.Dim = by, glob, true
			s.Note = ClaimNote(by, glob)
		}
		out = append(out, s)
	}
	if f.Start != "" {
		for _, r := range []struct {
			abs   string
			kind  FolderKind
			label string
		}{{f.Start, FolderHere, labelHere}, {f.GitRoot, FolderGitRoot, labelGitRoot}, {path.Dir(path.Clean(f.Start)), FolderParent, labelParent}} {
			if r.abs != "" && startFolderFits(f, r.abs) {
				add(r.abs, r.kind, r.label, "", false)
			}
		}
	}
	var other []IncludeFolder
	for _, in := range f.Includes {
		if in.Folder == "" {
			continue
		}
		if in.Email != "" && containsFold(f.Emails, in.Email) {
			add(expandSlashed(in.Folder, f.Home), FolderInclude, labelInclude, in.Email, false)
		} else {
			other = append(other, in)
		}
	}
	for _, in := range other {
		label := strings.TrimSpace(labelIncludeOther + in.Email)
		add(expandSlashed(in.Folder, f.Home), FolderIncludeOther, label, in.Email, true)
	}
	return out
}

// ClaimNote is the note of a folder another setup (by, with its glob)
// already covers: picking it here makes the folder match two setups.
func ClaimNote(by, glob string) string {
	return "setup " + by + " already covers this folder with " + glob + ", so it would match two setups: the most specific glob wins, and two equally specific ones are refused. Pick a folder only this setup uses, or remove " + glob + " from setup " + by + " first"
}

// StartFolderAllowed is the rule for the step's prefill from a "where you
// ran lore setup" folder (the start folder, its git repo or its parent,
// absolute abs): startFolderFits, and no other setup already claims it
// (claimedBy). A claimed folder is still suggested (dimmed, labelled with
// the setup, owner 2026-11-07) but never prefilled: the person picks it
// knowingly, after the page's conflict warning.
func StartFolderAllowed(f FolderFacts, abs string) bool {
	f = f.slashed()
	abs = strings.TrimSuffix(path.Clean(filepath.ToSlash(abs)), "/")
	if by, _ := claimedBy(abs, f); by != "" {
		return false
	}
	return startFolderFits(f, abs)
}

// startFolderFits is the identity rule for offering a "where you ran lore
// setup" folder at all (owner 2026-11-07: started in ~/Downloads/lore1, a
// work setup whose identity's includeIf folders are elsewhere was offered
// ~/Downloads/**). It holds when both hold:
//
//   - it is not under another identity's includeIf folder (git commits
//     there as that identity);
//   - when this setup's own identity has includeIf folders, it is under
//     one of them (those folders are the identity's own answer, and they
//     are suggested as FolderInclude rows).
//
// An include with no email is another identity's, as in SuggestFolders.
func startFolderFits(f FolderFacts, abs string) bool {
	abs = strings.TrimSuffix(path.Clean(abs), "/")
	ownAny, underOwn := false, false
	for _, in := range f.Includes {
		if in.Folder == "" {
			continue
		}
		under := within(abs, expandSlashed(in.Folder, f.Home))
		own := in.Email != "" && containsFold(f.Emails, in.Email)
		switch {
		case own:
			ownAny = true
			underOwn = underOwn || under
		case under:
			return false
		}
	}
	return !ownAny || underOwn
}

// within reports whether abs is folder dir or under it (both cleaned).
func within(abs, dir string) bool {
	dir = strings.TrimSuffix(path.Clean(dir), "/")
	return dir != "" && dir != "." && (abs == dir || strings.HasPrefix(abs, dir+"/"))
}

// PrefillFolder is the Folders step's starting answer: the git repo the
// page was started in, else the start folder itself, as a glob; "" when
// that is the home folder, the disk root or unknown. Never the start
// folder's parent: started in ~/Downloads/lore1, prefilling ~/Downloads/**
// made the new setup claim every folder under Downloads (realmac M1). The
// parent stays a suggestion only (SuggestFolders).
func PrefillFolder(home, start, gitRoot string) string {
	home, start, gitRoot = filepath.ToSlash(home), filepath.ToSlash(start), filepath.ToSlash(gitRoot)
	dir := start
	if gitRoot != "" {
		dir = gitRoot
	}
	dir = strings.TrimSuffix(path.Clean(dir), "/")
	if start == "" || dir == "" || dir == "." || dir == "/" || dir == strings.TrimSuffix(path.Clean(home), "/") {
		return ""
	}
	return tildeForm(dir, home) + globSuffix
}

// claimedBy is the first other setup with a glob matching folder abs (or
// anything under it), and that glob.
func claimedBy(abs string, f FolderFacts) (string, string) {
	probe := abs + "/x"
	for _, s := range f.Setups {
		if s.Name == f.Setup {
			continue
		}
		for _, g := range s.Folders {
			eg := expandSlashed(g, f.Home)
			if onboard.GlobMatch(eg, abs) || onboard.GlobMatch(eg, probe) {
				return s.Name, g
			}
		}
	}
	return "", ""
}

// tildeForm writes abs under home as ~/… (slash form on every system).
func tildeForm(abs, home string) string {
	abs, home = filepath.ToSlash(abs), strings.TrimSuffix(filepath.ToSlash(home), "/")
	if home != "" {
		if rest, ok := strings.CutPrefix(abs, home+"/"); ok {
			return "~/" + rest
		}
	}
	return abs
}

// BrowseIn is setup.browse's input.
type BrowseIn struct {
	// Path is the folder to list (~/…, or absolute under the home
	// folder); "" is the home folder.
	Path string `json:"path,omitempty" arg:"0" desc:"the folder to list (default: your home folder)"`
}

// BrowseDir is one subfolder.
type BrowseDir struct {
	Name string `json:"name"`
	// Path is the subfolder, ~ form (what the next browse takes).
	Path string `json:"path"`
}

// BrowseOut is setup.browse's output.
type BrowseOut struct {
	// Path is the listed folder (~ form); Parent the one above it, ""
	// at the home folder (the browser goes no higher).
	Path   string      `json:"path"`
	Parent string      `json:"parent,omitempty"`
	Dirs   []BrowseDir `json:"dirs"`
	// Truncated: more subfolders than BrowseMax; the rest are left out.
	Truncated bool `json:"truncated,omitempty"`
}

// BrowseMax caps one listing (a folder with thousands of subfolders is
// not one a person picks from a list).
const BrowseMax = 500

// FolderLister is the optional Prober upgrade setup.folders and
// setup.browse need (wire's Prober has it).
type FolderLister interface {
	FolderFacts(ctx context.Context, in FoldersIn) (FolderFacts, error)
	Browse(ctx context.Context, in BrowseIn) (BrowseOut, error)
}

// folderLister is the Prober's FolderLister, or E_UNSUPPORTED.
func (e engine) folderLister(ctx context.Context) (FolderLister, error) {
	pr, err := e.env.SetupProber(ctx)
	if err != nil {
		return nil, err
	}
	l, ok := pr.(FolderLister)
	if !ok {
		return nil, errs.New(errs.Unsupported, "this lore cannot list folders").WithHint("type the folder globs instead")
	}
	return l, nil
}

// Folders is setup.folders.
func (e engine) Folders(ctx context.Context, in FoldersIn) (FoldersOut, error) {
	l, err := e.folderLister(ctx)
	if err != nil {
		return FoldersOut{}, err
	}
	f, err := l.FolderFacts(ctx, in)
	if err != nil {
		return FoldersOut{}, err
	}
	out := FoldersOut{Suggestions: SuggestFolders(f), Start: tildeForm(f.Start, f.Home)}
	if out.Suggestions == nil {
		out.Suggestions = []FolderSuggestion{}
	}
	return out, nil
}

// Browse is setup.browse.
func (e engine) Browse(ctx context.Context, in BrowseIn) (BrowseOut, error) {
	l, err := e.folderLister(ctx)
	if err != nil {
		return BrowseOut{}, err
	}
	out, err := l.Browse(ctx, in)
	if err == nil && out.Dirs == nil {
		out.Dirs = []BrowseDir{}
	}
	return out, err
}

// SetupEmails are a setup's identity emails: its key's address and its
// other emails (dedup, case-insensitive), plus extra.
func SetupEmails(e onboard.SetupEntry, extra []string) []string {
	var out []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" && !containsFold(out, s) {
			out = append(out, s)
		}
	}
	if _, addr, ok := strings.Cut(e.Identity.Key, ":"); ok {
		add(addr)
	}
	for _, m := range slices.Concat(e.Identity.Emails, extra) {
		add(m)
	}
	return out
}

// FolderOps returns setup.folders and setup.browse bound to env; wire
// registers them next to DiscoverOps.
func FolderOps(env Onboard) []op.Entry {
	e := newEngine(env, onboardCatalog{})
	return []op.Entry{
		op.Define(op.Spec{Name: OpFolders, Local: true, RESTNoun: restNoun, Permission: op.PermRead, Access: op.AccessRead,
			Summary:  "Suggest folders for a setup: where lore setup was started, its git repo and parent, and the gitconfig includeIf folders",
			Errors:   errsPort,
			Examples: []op.Example{{Title: "folders for a setup", CLI: "lore setup folders --json", Want: ""}}},
			e.Folders),
		op.Define(op.Spec{Name: OpBrowse, Local: true, RESTNoun: restNoun, Permission: op.PermRead, Access: op.AccessRead,
			Summary:  "List the subfolders of a folder under your home folder (names only), for picking a setup's folders",
			Errors:   []errs.Code{errs.Unsupported, errs.InvalidInput, errs.BadPath, errs.Internal},
			Examples: []op.Example{{Title: "folders in your home folder", CLI: "lore setup browse --json", Want: ""}}},
			e.Browse),
	}
}

// expandSlashed is onboard.ExpandHome in the slash form every folder here
// is compared in (on Windows, filepath.Join gives C:\Users\a\work).
func expandSlashed(p, home string) string {
	return filepath.ToSlash(onboard.ExpandHome(p, home))
}
