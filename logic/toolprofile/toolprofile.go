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
	"io/fs"
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

// displayNames are the built-in profiles' product names, for pages and
// lists (the profile name stays the id everywhere else).
var displayNames = map[string]string{
	NameClaudeCode: "Claude Code",
	NameCodex:      "Codex",
	NameCursor:     "Cursor",
	NameGeminiCLI:  "Gemini CLI",
	NameCopilot:    "Copilot",
	NameGeneric:    "Generic",
}

// DisplayName is the product name of a built-in profile, else name itself
// (a user profile has no other name).
func DisplayName(name string) string {
	if d, ok := displayNames[name]; ok {
		return d
	}
	return name
}

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
	// claudeCodeLimit is the smallest per-file limit Claude Code documents
	// (40,000 characters on a 200K window; CQ1 table).
	claudeCodeLimit = 40000
	// codexLimit is Codex's combined project_doc_max_bytes (32 KiB).
	codexLimit = 32768
	// LimitWarnPct: a budget above this share of a tool's documented limit
	// gets LimitNote's warning (6.1).
	LimitWarnPct = 80
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
	// Limit is the per-file size the tool itself documents, in bytes; 0
	// when it documents none. Raising a budget above LimitWarnPct of it
	// gets a warning (CONTEXT_BUDGET 6.1, D8; LimitNote).
	Limit int `yaml:"limit,omitempty" json:"limit,omitempty"`
	// PrivateFile is the tool's uncommitted local file (a slash path
	// relative to the repo root) that carries one import of each private
	// store render (CFG6, FQ13): lore writes it and hides it through
	// .git/info/exclude. Empty when the tool has no such file; that tool
	// gets internal context through `lore context` and the hooks only.
	PrivateFile string `yaml:"private_file,omitempty" json:"private_file,omitempty"`
	// PathRules is the tool's own loader of path-scoped rule files
	// (CONTEXT_BUDGET 6.7 claude_rules / cursor_rules); zero when it has
	// none (pathrules.go).
	PathRules PathRules `yaml:"path_rules,omitempty" json:"path_rules,omitzero"`
	// SkillDir is the folder the tool loads the lore skill bundle from, a
	// home-relative slash path ("~/.claude/skills/lore"); empty when the
	// tool has no skill folder and reads lore only through its pointer
	// file and LORE.md (ONBOARDING_DESIGN 4.7.1; skilldir.go).
	SkillDir string `yaml:"skill_dir,omitempty" json:"skill_dir,omitempty"`
	// Detect are other files that show the repo uses the tool when its
	// pointer file is missing (slash paths relative to the repo root;
	// path.Match globs allowed in the last element): Cursor's legacy
	// .cursorrules and its own .cursor/rules/*.mdc. Used only to pick a
	// fresh repo's targets (Resolve, ReasonDetected) and only through a
	// Probe that is also a Finder; lore never writes these files. Built-in
	// profiles only for now: it is not a config.yaml key.
	Detect []string `yaml:"-" json:"-"`
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
		{Name: NameClaudeCode, PointerFile: "CLAUDE.md", Import: ImportAt, Budget: largeBudget, Limit: claudeCodeLimit, PrivateFile: "CLAUDE.local.md",
			PathRules: PathRules{Dir: ".claude/rules/lore", Style: PathRulesPaths}, SkillDir: skillDirClaudeCode},
		{Name: NameCodex, PointerFile: "AGENTS.md", Import: ImportRead, Budget: DefaultBudget, Limit: codexLimit, SkillDir: skillDirCodex},
		{Name: NameCursor, PointerFile: ".cursor/rules/lore.mdc", Import: ImportMDC, Budget: DefaultBudget, PrivateFile: ".cursor/rules/lore-private.mdc",
			PathRules: PathRules{Dir: ".cursor/rules", Style: PathRulesMDCGlobs}, Detect: []string{".cursorrules", ".cursor/rules/*.mdc"}},
		{Name: NameGeminiCLI, PointerFile: "GEMINI.md", Import: ImportAt, Budget: largeBudget},
		{Name: NameCopilot, PointerFile: ".github/copilot-instructions.md", Import: ImportRead, Budget: DefaultBudget},
		{Name: NameGeneric, Import: ImportNone, Budget: DefaultBudget},
	}
}

// nameRe is the shape of a profile name: it is typed after --target and
// stored in config, so it stays one plain token.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// LooksLikeName reports whether s has the shape of a profile name (one
// plain lowercase token, no dot or slash). A --target value of that shape
// that names no profile is almost always a typo of one (claud_code), so
// callers refuse it rather than create a file of that name (INTEG-RENDER-1);
// a real file with such a name is still reachable as a path (./notes).
func LooksLikeName(s string) bool {
	return len(s) <= maxNameLen && nameRe.MatchString(s)
}

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
		p.SkillDir = cleanSkillDir(p.SkillDir)
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
	if p.Limit < 0 {
		return fmt.Errorf("tool profile %q: limit must not be negative", p.Name)
	}
	if err := validateSkillDir(p); err != nil {
		return err
	}
	if p.Import == ImportNone {
		return nil
	}
	if err := r.validatePrivate(p); err != nil {
		return err
	}
	if err := validatePathRules(p); err != nil {
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

// Finder is the optional upgrade of a Probe that lists the regular files
// matching a slash pattern relative to the repo root (a path.Match glob in
// the last element), as slash paths. Resolve uses it for Profile.Detect; a
// Probe without it detects by pointer files only.
type Finder interface {
	Matches(pattern string) []string
}

// GlobFiles is the regular files in fsys matching the slash pattern
// (fs.Glob). Probes implement Finder with it over os.DirFS(root), so this
// package itself touches no file system.
func GlobFiles(fsys fs.FS, pattern string) []string {
	matches, err := fs.Glob(fsys, pattern)
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range matches {
		if fi, serr := fs.Stat(fsys, m); serr == nil && fi.Mode().IsRegular() {
			out = append(out, m)
		}
	}
	return out
}

// detectedBy reports whether one of p's Detect files exists (probe must
// be a Finder; false otherwise). A file lore itself writes for p (its
// pointer, private, path-rule or folder rule files) is no sign the person
// uses the tool: a repo that stopped targeting Cursor keeps none of them
// as a reason to pick Cursor again.
func detectedBy(p Profile, probe Probe) bool {
	f, ok := probe.(Finder)
	if !ok {
		return false
	}
	for _, pat := range p.Detect {
		for _, m := range f.Matches(pat) {
			if !p.writesFile(m) {
				return true
			}
		}
	}
	return false
}

// writesFile reports whether rel (a slash path from the repo root) is a
// file lore writes for p.
func (p Profile) writesFile(rel string) bool {
	if rel == p.PointerFile || rel == p.PrivateFile {
		return true
	}
	base := path.Base(rel)
	return strings.HasPrefix(base, PathRulePrefix) || strings.HasPrefix(base, DirRulePrefix)
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
	// ReasonSetup: no context.targets row; the tools the guided setup that
	// applies here chose (A-9), plus every file already wired (FromSetup).
	ReasonSetup Reason = "setup"
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
	// present: the pointer file exists (Unwired names those files);
	// detected: present, or one of the tool's Detect files exists (only a
	// fresh repo's pick: a Detect file is not the pointer file render
	// would name as "found but not wired").
	var wired, present, detected []Profile
	for _, p := range r.profiles {
		if !p.HasPointerFile() {
			continue
		}
		switch probe.State(p.PointerFile) {
		case FileWired:
			wired = append(wired, p)
		case FilePresent:
			present = append(present, p)
			detected = append(detected, p)
		case FileMissing:
			if detectedBy(p, probe) {
				detected = append(detected, p)
			}
		}
	}
	switch {
	case len(wired) > 0:
		return Selection{Profiles: wired, Reason: ReasonWired, Unwired: present}
	case len(detected) > 0:
		return Selection{Profiles: detected, Reason: ReasonDetected}
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

// RefuseTypo refuses a --target value that has a profile name's shape
// (LooksLikeName) but names no profile of r, with E_INVALID_INPUT listing
// the known profiles; nil for anything else. Callers ask it after the
// profile and pointer-file lookups missed, before treating the value as a
// file to create (INTEG-RENDER-1: `--target claud_code` was a new file).
func (r *Registry) RefuseTypo(v string) error {
	if !LooksLikeName(v) {
		return nil
	}
	if _, ok := r.Lookup(v); ok {
		return nil
	}
	return errs.New(errs.InvalidInput, fmt.Sprintf("--target %q: unknown tool profile (known: %s)", v, strings.Join(r.Names(), ", "))).
		WithHint("add your own under context.profiles in ~/.lore/config.yaml, or write a file of that name as a path: --target ./" + v)
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

// FromSetup is the selection of a repo with no context.targets row whose
// guided setup chose tools (A-9: `lore render` left Codex and Cursor
// unwired after setup chose them, until --target). It is base (Resolve's
// answer) with the setup's tools added: a file lore already wires stays
// wired (EXISTING REPOS KEEP WORKING), a file only detected is wired only
// when the setup names its tool, else it stays in Unwired so the caller
// names it (only when its pointer file exists). An unknown name is skipped (the setup was checked when saved;
// a profile removed since is not this repo's error).
func (r *Registry) FromSetup(names []string, base Selection, probe Probe) Selection {
	sel := Selection{Reason: ReasonSetup}
	if base.Reason == ReasonWired {
		sel.Profiles = slices.Clone(base.Profiles)
	}
	for _, p := range r.profiles {
		if slices.Contains(names, p.Name) && !slices.ContainsFunc(sel.Profiles, func(q Profile) bool { return q.Name == p.Name }) {
			sel.Profiles = append(sel.Profiles, p)
		}
	}
	unwired := slices.Clone(base.Unwired)
	if base.Reason == ReasonDetected {
		unwired = append(unwired, base.Profiles...)
	}
	for _, p := range unwired {
		if !slices.ContainsFunc(sel.Profiles, func(q Profile) bool { return q.Name == p.Name }) && p.HasPointerFile() && probe.State(p.PointerFile) == FilePresent {
			sel.Unwired = append(sel.Unwired, p)
		}
	}
	return sel
}

// LimitNote is the warning for a budget of n bytes on p: "" unless p
// documents a limit and n is above LimitWarnPct of it (CONTEXT_BUDGET 6.1:
// raising a budget near a tool's limit is allowed but said out loud; the
// policy write itself always confirms).
func (p Profile) LimitNote(n int) string {
	if p.Limit <= 0 || n*100 <= p.Limit*LimitWarnPct {
		return ""
	}
	return fmt.Sprintf("WARN: %d bytes is %d%% of %s's documented %d-byte limit; sessions may see a size warning or a cut file", n, n*100/p.Limit, p.Name, p.Limit)
}
