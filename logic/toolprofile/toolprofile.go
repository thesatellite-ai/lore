// Package toolprofile is the one registry of AI coding tools lore wires its
// generated context into.
//
// TOOL NEUTRAL (owner 2026-11-04, "lore can be wired to any tool, claude,
// codex"): no tool is the default or special. `lore render` always writes the
// canonical file .lore/LORE.md; each tool is a Profile (a name, the pointer
// file the tool reads, how that file points at LORE.md, and the tool's
// context budget). The built-in profiles are ordinary entries of the same
// registry a user extends from config, so nothing in lore names one tool's
// file outside this package.
//
// Which profiles a repo uses is its context.targets settings row (Q-R2-3,
// SETTINGS ARE DATA): Select turns the row's names into profiles. A repo
// without the row (every repo wired before 0.1.18) falls back to Resolve,
// which reads the repo itself: the committed pointer files that already
// carry lore's pointer block are the record, so the same checkout resolves
// the same targets on every machine with no hidden state. A repo where no
// pointer file carries the block yet uses the tools whose pointer files
// exist (detected); a repo with none of them gets the cross-tool convention
// AGENTS.md, never CLAUDE.md by default.
//
// Layer L1: pure. File access goes through the Probe the caller supplies.
package toolprofile

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// CanonicalOutput is the generated knowledge file every profile points at,
// relative to the repo root. TOOL NEUTRAL: it is named after lore, not after
// any tool.
const CanonicalOutput = ".lore/LORE.md"

// Built-in profile names. Snake case, the form a user profile name takes too.
const (
	NameClaudeCode = "claude_code"
	NameCodex      = "codex"
	NameCursor     = "cursor"
	NameGeminiCLI  = "gemini_cli"
	NameCopilot    = "copilot"
	NameGeneric    = "generic"
)

// FallbackName is the profile a repo gets when no tool is detected. TOOL
// NEUTRAL: AGENTS.md (codex's file) is the cross-tool convention several
// tools read, so it is the neutral choice; CLAUDE.md is never a default.
const FallbackName = NameCodex

// Budgets, in bytes (CONTEXT_BUDGET 6.1: a UTF-8 byte count is never
// smaller than the character count, so it is safe for every tool). TOOL NEUTRAL: each comes from the tool's profile and
// is documented as a neutral table, never as one tool's default.
const (
	// DefaultBudget applies to a profile that states none (CQ1 table).
	DefaultBudget = 16000
	// largeBudget is for tools whose context comfortably holds 32k of
	// pinned project knowledge (claude_code, gemini_cli; CQ1).
	largeBudget = 32000
)

// Import is how a pointer file makes its tool load LORE.md.
type Import string

// Import styles.
const (
	// ImportAt writes an `@path` import line (tools that resolve imports
	// relative to the importing file) plus a prose fallback comment. Its
	// bytes are v0.1.17's CLAUDE.md pointer, so existing repos re-render
	// with no diff.
	ImportAt Import = "at"
	// ImportRead writes one visible sentence telling the agent to read
	// LORE.md, for tools without an import syntax.
	ImportRead Import = "read"
	// ImportMDC is a Cursor rule file: the read sentence after an
	// `alwaysApply: true` frontmatter, which must stay the file's first
	// lines, so lore's blocks go after it.
	ImportMDC Import = "mdc"
	// ImportNone has no pointer file: the tool reads LORE.md itself (or the
	// user wires it by hand). Rendering for it writes only LORE.md.
	ImportNone Import = "none"
)

// Imports lists every import style, for validation and docs.
func Imports() []Import { return []Import{ImportAt, ImportRead, ImportMDC, ImportNone} }

// Profile is one tool lore can wire into.
type Profile struct {
	// Name identifies the profile (`lore render --target <name>`).
	Name string `yaml:"name" json:"name"`
	// PointerFile is the file the tool reads, a slash path relative to the
	// repo root. Empty exactly when Import is ImportNone.
	PointerFile string `yaml:"pointer_file,omitempty" json:"pointer_file,omitempty"`
	// Import is how PointerFile points at LORE.md.
	Import Import `yaml:"import" json:"import"`
	// Budget is the tool's context budget in bytes; 0 means DefaultBudget.
	Budget int `yaml:"budget,omitempty" json:"budget,omitempty"`
	// PrivateFile is the tool's uncommitted local file (a slash path
	// relative to the repo root) that carries one import of each private
	// store render (CFG6, FQ13): lore writes it and hides it through
	// .git/info/exclude. Empty when the tool has no such file; that tool
	// gets internal context through `lore context` and the hooks only.
	PrivateFile string `yaml:"private_file,omitempty" json:"private_file,omitempty"`
}

// EffectiveBudget returns Budget, or DefaultBudget when unset.
func (p Profile) EffectiveBudget() int {
	if p.Budget <= 0 {
		return DefaultBudget
	}
	return p.Budget
}

// HasPointerFile reports whether rendering for p stitches a pointer file.
func (p Profile) HasPointerFile() bool { return p.Import != ImportNone && p.PointerFile != "" }

// Builtins returns the built-in profiles in a fixed order (the order targets
// are listed and stitched in). TOOL NEUTRAL: these are plain entries; a user
// profile is resolved by exactly the same code.
func Builtins() []Profile {
	return []Profile{
		{Name: NameClaudeCode, PointerFile: "CLAUDE.md", Import: ImportAt, Budget: largeBudget, PrivateFile: "CLAUDE.local.md"},
		{Name: NameCodex, PointerFile: "AGENTS.md", Import: ImportRead, Budget: DefaultBudget},
		{Name: NameCursor, PointerFile: ".cursor/rules/lore.mdc", Import: ImportMDC, Budget: DefaultBudget, PrivateFile: ".cursor/rules/lore-private.mdc"},
		{Name: NameGeminiCLI, PointerFile: "GEMINI.md", Import: ImportAt, Budget: largeBudget},
		{Name: NameCopilot, PointerFile: ".github/copilot-instructions.md", Import: ImportRead, Budget: DefaultBudget},
		{Name: NameGeneric, Import: ImportNone, Budget: DefaultBudget},
	}
}

// nameRe is the shape of a profile name: it is typed after --target and
// stored in config, so it stays one plain token.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// maxNameLen keeps a profile name usable in one-line output.
const maxNameLen = 64

// Registry is the ordered set of known profiles: the built-ins, then the
// user's own from config.
type Registry struct {
	profiles []Profile
}

// New builds the registry from the built-ins plus user profiles. A user
// profile may not reuse a built-in's name or any profile's pointer file
// (one file, one owner), so a name always means one thing; every problem is
// reported together.
func New(user []Profile) (*Registry, error) {
	r := &Registry{profiles: Builtins()}
	var problems []error
	for _, p := range user {
		if err := r.validate(p); err != nil {
			problems = append(problems, err)
			continue
		}
		p.PointerFile = path.Clean(p.PointerFile)
		if p.Import == ImportNone {
			p.PointerFile = ""
			p.PrivateFile = "" // no import syntax to carry the private lines
		}
		if p.PrivateFile != "" {
			p.PrivateFile = path.Clean(p.PrivateFile)
		}
		r.profiles = append(r.profiles, p)
	}
	if len(problems) > 0 {
		return nil, errors.Join(problems...)
	}
	return r, nil
}

func (r *Registry) validate(p Profile) error {
	if len(p.Name) > maxNameLen || !nameRe.MatchString(p.Name) {
		return fmt.Errorf("tool profile %q: name must be lowercase letters, digits and _ (start with a letter, at most %d)", p.Name, maxNameLen)
	}
	if _, ok := r.Lookup(p.Name); ok {
		return fmt.Errorf("tool profile %q: the name is already taken (built-in names are reserved)", p.Name)
	}
	known := false
	for _, imp := range Imports() {
		known = known || p.Import == imp
	}
	if !known {
		return fmt.Errorf("tool profile %q: import %q is not one of at, read, mdc, none", p.Name, p.Import)
	}
	if p.Budget < 0 {
		return fmt.Errorf("tool profile %q: budget must not be negative", p.Name)
	}
	if p.Import == ImportNone {
		return nil
	}
	if err := r.validatePrivate(p); err != nil {
		return err
	}
	f := p.PointerFile
	if f == "" {
		return fmt.Errorf("tool profile %q: pointer_file is required unless import is none", p.Name)
	}
	clean := path.Clean(f)
	if path.IsAbs(clean) || strings.Contains(f, `\`) || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("tool profile %q: pointer_file %q must be a slash path inside the repo", p.Name, f)
	}
	if clean == ".lore" || strings.HasPrefix(clean, ".lore/") {
		return fmt.Errorf("tool profile %q: pointer_file %q must not be inside .lore/ (lore owns that folder)", p.Name, f)
	}
	if other, ok := r.ByPointerFile(clean); ok {
		return fmt.Errorf("tool profile %q: pointer_file %q already belongs to profile %q", p.Name, f, other.Name)
	}
	for _, o := range r.profiles {
		if o.HasPrivateFile() && o.PrivateFile == clean {
			return fmt.Errorf("tool profile %q: pointer_file %q is the private file of profile %q", p.Name, f, o.Name)
		}
	}
	return nil
}

// Profiles returns every profile in registry order.
func (r *Registry) Profiles() []Profile { return append([]Profile(nil), r.profiles...) }

// Names returns every profile name in registry order.
func (r *Registry) Names() []string {
	out := make([]string, len(r.profiles))
	for i, p := range r.profiles {
		out[i] = p.Name
	}
	return out
}

// Lookup returns the profile named name.
func (r *Registry) Lookup(name string) (Profile, bool) {
	for _, p := range r.profiles {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

// ByPointerFile returns the profile whose pointer file is rel (a slash path
// relative to the repo root).
func (r *Registry) ByPointerFile(rel string) (Profile, bool) {
	rel = path.Clean(rel)
	for _, p := range r.profiles {
		if p.HasPointerFile() && p.PointerFile == rel {
			return p, true
		}
	}
	return Profile{}, false
}

// Fallback returns the profile used when no tool is detected (FallbackName).
func (r *Registry) Fallback() Profile {
	p, _ := r.Lookup(FallbackName) // a built-in: always present
	return p
}

// FileState is what a Probe found at one pointer file.
type FileState int

// File states.
const (
	// FileMissing: no file at the path.
	FileMissing FileState = iota
	// FilePresent: the file exists without lore's pointer block (a tool the
	// repo uses that lore is not wired into yet).
	FilePresent
	// FileWired: the file carries lore's pointer block.
	FileWired
)

// Probe reports the state of a pointer file, given as a slash path relative
// to the repo root. The caller owns file access, so this package stays pure.
type Probe interface {
	State(rel string) FileState
}

// Reason says how Resolve chose the targets.
type Reason string

// Reasons, in the order Resolve tries them.
const (
	// ReasonWired: pointer files that already carry lore's block.
	ReasonWired Reason = "wired"
	// ReasonDetected: no file is wired yet; the tools whose pointer files
	// exist.
	ReasonDetected Reason = "detected"
	// ReasonFallback: no tool detected; FallbackName.
	ReasonFallback Reason = "fallback"
	// ReasonSetting: the repo's context.targets settings row (Select).
	ReasonSetting Reason = "setting"
)

// Selection is the resolved context.targets of a repo.
type Selection struct {
	Profiles []Profile
	Reason   Reason
	// Unwired are tools whose file exists but carries no pointer while
	// others do (ReasonWired), or that the context.targets row does not
	// name (ReasonSetting): never wired silently, so the caller can tell
	// the user how to add them.
	Unwired []Profile
	// Unlisted are tools whose pointer file already carries lore's block
	// but that the context.targets row does not name (ReasonSetting only).
	// Render leaves such a file exactly as it is (never re-wired, never
	// stripped); the caller says so, so the gap is never silent
	// (DECISION P2-OLDCLI-2, r3-targets).
	Unlisted []Profile
}

// Resolve returns the repo's context targets (see the package doc). TOOL
// NEUTRAL + EXISTING REPOS KEEP WORKING: a repo already wired into CLAUDE.md
// keeps exactly that target, and a hand-written file of another tool is
// never wired behind the user's back once lore is wired somewhere; a new
// repo is wired into the tools it already uses, else AGENTS.md.
func (r *Registry) Resolve(probe Probe) Selection {
	var wired, present []Profile
	for _, p := range r.profiles {
		if !p.HasPointerFile() {
			continue
		}
		switch probe.State(p.PointerFile) {
		case FileWired:
			wired = append(wired, p)
		case FilePresent:
			present = append(present, p)
		case FileMissing:
		}
	}
	switch {
	case len(wired) > 0:
		return Selection{Profiles: wired, Reason: ReasonWired, Unwired: present}
	case len(present) > 0:
		return Selection{Profiles: present, Reason: ReasonDetected}
	default:
		return Selection{Profiles: []Profile{r.Fallback()}, Reason: ReasonFallback}
	}
}

// Unknown returns the names that are not a profile of r, in input order
// without repeats.
func (r *Registry) Unknown(names []string) []string {
	var out []string
	for _, n := range names {
		if _, ok := r.Lookup(n); !ok && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// CheckNames refuses names that are not profiles of r with E_INVALID_INPUT
// naming each one and listing the known profiles; an empty list is refused
// too (the generic profile is how a repo asks for LORE.md only).
//
// Q-R2-3: context.targets is validated against the registry the user
// actually has (built-ins plus context.profiles), so a typo never becomes a
// row that silently wires nothing.
func (r *Registry) CheckNames(names []string) error {
	if len(names) == 0 {
		return errs.New(errs.InvalidInput, "context.targets: name at least one tool profile (known: "+strings.Join(r.Names(), ", ")+"; "+NameGeneric+" writes only "+CanonicalOutput+")")
	}
	if bad := r.Unknown(names); len(bad) > 0 {
		return errs.New(errs.InvalidInput, "context.targets: unknown tool profile "+quoteAll(bad)+" (known: "+strings.Join(r.Names(), ", ")+")").
			WithHint("add your own under context.profiles in ~/.lore/config.yaml")
	}
	return nil
}

// quoteAll is `"a", "b"`.
func quoteAll(xs []string) string {
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = fmt.Sprintf("%q", x)
	}
	return strings.Join(q, ", ")
}

// Select returns the profiles a context.targets row names, in registry
// order (so the stitch order never depends on how the row was typed),
// with Reason ReasonSetting. Unwired lists the tools whose pointer file
// exists without lore's block and that the row does not name. An unknown
// or empty name list is refused (CheckNames).
//
// Q-R2-3: the row is the repo's record of its tools; Resolve's pointer-file
// detection is only the fallback for repos without one.
func (r *Registry) Select(names []string, probe Probe) (Selection, error) {
	if err := r.CheckNames(names); err != nil {
		return Selection{}, err
	}
	sel := Selection{Reason: ReasonSetting}
	for _, p := range r.profiles {
		switch {
		case slices.Contains(names, p.Name):
			sel.Profiles = append(sel.Profiles, p)
		case p.HasPointerFile() && probe.State(p.PointerFile) == FilePresent:
			sel.Unwired = append(sel.Unwired, p)
		case p.HasPointerFile() && probe.State(p.PointerFile) == FileWired:
			sel.Unlisted = append(sel.Unlisted, p)
		}
	}
	return sel, nil
}
