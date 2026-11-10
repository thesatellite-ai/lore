package skill

// `lore skill install` (ONBOARDING_DESIGN 4.7.1, 10.3): write the skill
// bundle embedded in this binary into each tool's skill folder, stamped
// with the binary's version.
//
//	skill.install ─▶ pick tools: --tool… │ --all │ active setup's │ detected
//	              ─▶ per tool: SkillDir? no ─▶ refuse (--tool) / skip
//	              ─▶ want = embedded bundle, SKILL.md stamped lore_version: <v>
//	              ─▶ same bytes on disk ─▶ changed:false, nothing written
//	              ─▶ hand-edited at the same version ─▶ <file>.bak-<date>
//	              ─▶ write a temp folder beside it, then rename it into place
//
// Local: it touches only the user's home folder, never a store.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/skills"
)

// Operation names of 4.7.1. CONTRACT NOTE: contracts.md did not exist when
// these were written, so they live here; ops/setup may alias them.
const (
	OpInstall = "skill.install"
	OpStatus  = "skill.status"
)

// File modes of the installed bundle: owner-only, like every file lore
// writes (compile.go outputFileMode); folders owner rwx.
const (
	installFileMode fs.FileMode = outputFileMode
	installDirMode  fs.FileMode = 0o700
)

// backupDateLayout names a backup SKILL.md.bak-2026-11-06 (ONB 4.7.1);
// backupTimeLayout is appended when a backup of that date exists.
const (
	backupSep        = ".bak-"
	backupDateLayout = "2006-01-02"
	backupTimeLayout = "150405"
)

// tmpPattern / oldPattern name the sibling folders of the atomic swap.
const (
	tmpPattern = ".lore-skill-tmp-*"
	oldPattern = ".lore-skill-old-*"
)

// LinkMode is how skill.install treats a skill folder (or a bundle file in
// it) that is a symbolic link whose target does not already hold this
// binary's skill.
//
// DECISION (2026-11-08, owner bug on lore-dev): never write through a link
// unasked. The link is the person's (a shared-skills tool, a source
// checkout) and its target is often a git checkout of someone else's;
// before this, install skipped such a folder silently and setup still said
// "installed". Now a link whose target holds this exact skill is reported
// installed with nothing written; anything else is E_SKILL_LINKED naming
// the link and its target, and the person picks one of the two ways out.
type LinkMode string

const (
	// LinkRefuse (the default): report a current target, refuse otherwise.
	LinkRefuse LinkMode = ""
	// LinkReplace removes the link (only the link: its target is not
	// touched) and installs lore's own copy in its place.
	LinkReplace LinkMode = "replace"
	// LinkUpdate writes this binary's bundle into the link's target, file by
	// file, keeping every other file there (hand-edited ones get a .bak copy).
	LinkUpdate LinkMode = "update"
)

// LinkModes are the values --link takes.
func LinkModes() []LinkMode { return []LinkMode{LinkReplace, LinkUpdate} }

// Via values of Installed: how a linked folder was handled.
const (
	viaReplacedLink  = "replaced_link"
	viaUpdatedTarget = "updated_target"
)

// ToolChooser picks the tools when no --tool and no --all are given (ONB
// 4.7.1: "the tools of the active setup, else the detected ones"). SEAM:
// the setup engine (ops/setup, wire) implements it; HomeDetector is the
// fallback detector until it does.
type ToolChooser interface {
	// ActiveSetupTools returns the active setup's `tools` (ok false when no
	// setup is active or it lists none).
	ActiveSetupTools(ctx context.Context) (tools []string, ok bool, err error)
	// DetectedTools returns the tools found on this machine.
	DetectedTools(ctx context.Context) ([]string, error)
}

// InstallEnv is what skill.install and skill.status need; *wire.App
// implements it (BinaryVersion and ToolProfiles exist there already).
type InstallEnv interface {
	// BinaryVersion is the running lore's version (the stamp).
	BinaryVersion() string
	// ToolProfiles are the user's own profiles (context.profiles).
	ToolProfiles(ctx context.Context) ([]toolprofile.Profile, error)
	// SkillHome is the home folder the skill folders live under.
	SkillHome(ctx context.Context) (string, error)
	// SkillTools is the chooser for the default tool list.
	SkillTools(ctx context.Context) (ToolChooser, error)
}

// Installer is skill.install / skill.status with every input explicit, so
// tests run it against a temp HOME with no globals.
type Installer struct {
	// Home is the user's home folder.
	Home string
	// Version is the binary version stamped into SKILL.md ("dev" when empty).
	Version string
	// Registry is built-ins plus the user's profiles.
	Registry *toolprofile.Registry
	// Bundle maps slash paths to the embedded bytes; nil means skills.Files.
	Bundle map[string][]byte
	// Chooser picks default tools; nil means HomeDetector over Registry.
	Chooser ToolChooser
	// Now is the clock for backup names; nil means time.Now. The ops
	// (newInstaller) set it to the engine's clock.
	Now func() time.Time
}

// InstallIn is the input of skill.install.
type InstallIn struct {
	Tools  []string `json:"tools,omitempty" flag:"tool" desc:"tool profile to install for (repeatable; default: the active setup's tools, else the detected ones)"`
	All    bool     `json:"all,omitempty" flag:"all" desc:"install for every tool that has a skill folder"`
	DryRun bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"show what would be written; write nothing"`
	Link   LinkMode `json:"link,omitempty" flag:"link" desc:"when the skill folder is a symbolic link to an older or foreign skill: replace (lore's own copy replaces the link) or update (write into the link's target)"`
}

// Installed is one tool's result.
type Installed struct {
	Tool    string `json:"tool"`
	Dir     string `json:"dir"`
	Version string `json:"version" desc:"the lore_version stamped into SKILL.md"`
	Changed bool   `json:"changed" desc:"false when the folder already held these exact files"`
	// Backup lists the copies kept of hand-edited files before they were
	// overwritten (DEVIATION from 4.7.1's singular "backup": every edited
	// bundle file gets one, not only SKILL.md).
	Backup []string `json:"backup,omitempty" desc:"copies of hand-edited files kept before overwriting"`
	// Link is where Dir's symbolic link ends; empty for a plain folder.
	Link string `json:"link,omitempty" desc:"where the skill folder's symbolic link ends (empty for a plain folder)"`
	// Via says how a linked folder was handled: replaced_link or
	// updated_target; empty when nothing was linked or nothing was written.
	Via string `json:"via,omitempty" desc:"replaced_link or updated_target when the folder was a symbolic link"`
}

// where is Dir, with the link's end when it is one.
func (i Installed) where() string {
	if i.Link == "" || i.Via == viaReplacedLink {
		return i.Dir
	}
	return i.Dir + " -> " + i.Link
}

// Skipped is one tool that was not installed, and why.
type Skipped struct {
	Tool string `json:"tool"`
	Why  string `json:"why"`
}

// InstallOut is the output of skill.install.
type InstallOut struct {
	Installed []Installed `json:"installed"`
	Skipped   []Skipped   `json:"skipped"`
	DryRun    bool        `json:"dry_run,omitempty"`
}

// Text prints one line per tool.
func (o InstallOut) Text() string {
	var b strings.Builder
	verb := "installed"
	if o.DryRun {
		verb = "would install"
	}
	for _, i := range o.Installed {
		switch {
		case !i.Changed:
			fmt.Fprintf(&b, "%s: up to date (lore %s) in %s\n", i.Tool, i.Version, i.where())
		case i.Via == viaReplacedLink:
			fmt.Fprintf(&b, "%s: %s lore %s skill in %s, replacing the link to %s (its target is untouched)\n", i.Tool, verb, i.Version, i.Dir, i.Link)
		default:
			fmt.Fprintf(&b, "%s: %s lore %s skill in %s\n", i.Tool, verb, i.Version, i.where())
		}
		for _, bk := range i.Backup {
			fmt.Fprintf(&b, "  kept your edited copy as %s\n", bk)
		}
	}
	for _, s := range o.Skipped {
		fmt.Fprintf(&b, "%s: skipped: %s\n", s.Tool, s.Why)
	}
	return b.String()
}

// InstallOps returns skill.install and skill.status bound to env, for
// op.NewRegistry (registered beside Ops by the composition root).
func InstallOps(env InstallEnv) []op.Entry {
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpInstall, Summary: "Install the lore skill bundle into each coding tool's skill folder, stamped with this binary's version",
			Permission: op.PermWrite, Access: op.AccessRead, Local: true, RESTNoun: restNoun,
			Errors: []errs.Code{errs.InvalidInput, errs.Unsupported, errs.ConfigInvalid, errs.SkillLinked, errs.SymlinkLoop, errs.Internal},
			Examples: []op.Example{
				{Title: "install for every tool with a skill folder", CLI: "lore skill install --all --dry-run", Want: ""},
			},
		}, func(ctx context.Context, in InstallIn) (InstallOut, error) {
			ins, err := newInstaller(ctx, env)
			if err != nil {
				return InstallOut{}, err
			}
			return ins.Install(ctx, in)
		}),
		op.Define(op.Spec{
			Name: OpStatus, Summary: "Show each coding tool's installed lore skill: folder, version, hand edits and the version check",
			Permission: op.PermRead, Access: op.AccessRead, Local: true, RESTNoun: restNoun,
			Errors:   []errs.Code{errs.InvalidInput, errs.ConfigInvalid, errs.SymlinkLoop, errs.Internal},
			Examples: []op.Example{{Title: "check every skill folder", CLI: "lore skill status", Want: ""}},
		}, func(ctx context.Context, in StatusIn) (StatusOut, error) {
			ins, err := newInstaller(ctx, env)
			if err != nil {
				return StatusOut{}, err
			}
			return ins.Status(ctx, in)
		}),
	}
}

// newInstaller builds the Installer from env.
func newInstaller(ctx context.Context, env InstallEnv) (Installer, error) {
	home, err := env.SkillHome(ctx)
	if err != nil {
		return Installer{}, err
	}
	reg, err := Registry(ctx, env)
	if err != nil {
		return Installer{}, err
	}
	ch, err := env.SkillTools(ctx)
	if err != nil {
		return Installer{}, err
	}
	// Now is the engine's clock (opclock: LORE_NOW in a loretest build),
	// so a backup's SKILL.md.bak-<date> agrees with every other stamp of
	// the run; an env that is no Clocker falls back to the system clock.
	now := func() time.Time { return opclock.Now(env) }
	return Installer{Home: home, Version: env.BinaryVersion(), Registry: reg, Chooser: ch, Now: now}, nil
}

// Registry builds the tool registry from the user's profiles; a bad
// profile (a bad skill_dir among them) is E_CONFIG_INVALID naming
// context.profiles (ONB 4.7.1).
func Registry(ctx context.Context, env InstallEnv) (*toolprofile.Registry, error) {
	user, err := env.ToolProfiles(ctx)
	if err != nil {
		return nil, err
	}
	reg, err := toolprofile.New(user)
	if err != nil {
		return nil, errs.Wrap(errs.ConfigInvalid, "context.profiles in ~/.lore/config.yaml", err).
			WithHint("fix the profile, then check it with `lore config check`")
	}
	return reg, nil
}

// HomeDetector is the fallback ToolChooser: no active setup, and a tool is
// detected when its skill folder's top folder under home exists (~/.claude,
// ~/.codex). ONB 4.7 detection also looks at PATH; the setup engine's
// prober replaces this.
type HomeDetector struct {
	Home     string
	Registry *toolprofile.Registry
}

// ActiveSetupTools: no setup engine, so none.
func (HomeDetector) ActiveSetupTools(context.Context) ([]string, bool, error) {
	return nil, false, nil
}

// DetectedTools lists the skill-capable tools whose home folder exists.
func (d HomeDetector) DetectedTools(context.Context) ([]string, error) {
	var out []string
	for _, p := range d.Registry.SkillProfiles() {
		rel, err := filepath.Rel(d.Home, p.SkillPath(d.Home))
		if err != nil {
			continue
		}
		top, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
		if st, serr := os.Stat(filepath.Join(d.Home, top)); serr == nil && st.IsDir() {
			out = append(out, p.Name)
		}
	}
	return out, nil
}

// prepared fills defaults.
func (ins Installer) prepared() (Installer, error) {
	if ins.Home == "" {
		return ins, errs.New(errs.Internal, "skill install: no home folder").
			WithHint("set HOME and run again")
	}
	if ins.Registry == nil {
		reg, err := toolprofile.New(nil)
		if err != nil {
			return ins, errs.Wrap(errs.Internal, "tool profiles", err)
		}
		ins.Registry = reg
	}
	if ins.Bundle == nil {
		b, _, err := skills.Files()
		if err != nil {
			return ins, errs.Wrap(errs.Internal, "read the embedded skill bundle", err)
		}
		ins.Bundle = b
	}
	if ins.Chooser == nil {
		ins.Chooser = HomeDetector{Home: ins.Home, Registry: ins.Registry}
	}
	if ins.Now == nil {
		ins.Now = time.Now
	}
	ins.Version = toolprofile.NormVersion(ins.Version)
	if ins.Version == "" {
		ins.Version = toolprofile.SkillVersionDev
	}
	return ins, nil
}

// pick resolves the tool names: explicit names are checked against the
// registry (explicit = refuse a tool with no skill folder); --all and the
// default list skip such tools instead.
func (ins Installer) pick(ctx context.Context, tools []string, all bool) (names []string, explicit bool, err error) {
	switch {
	case len(tools) > 0:
		if cerr := ins.Registry.CheckNames(tools); cerr != nil {
			return nil, false, cerr
		}
		return dedupe(tools), true, nil
	case all:
		for _, p := range ins.Registry.SkillProfiles() {
			names = append(names, p.Name)
		}
		return names, false, nil
	}
	got, ok, err := ins.Chooser.ActiveSetupTools(ctx)
	if err != nil {
		return nil, false, err
	}
	if !ok || len(got) == 0 {
		if got, err = ins.Chooser.DetectedTools(ctx); err != nil {
			return nil, false, err
		}
	}
	if len(got) == 0 {
		return nil, false, errs.New(errs.InvalidInput, "no coding tool found: no active setup lists tools and none was detected in "+ins.Home).
			WithHint("name one with --tool <profile> (known: " + strings.Join(ins.Registry.Names(), ", ") + "), or use --all")
	}
	return dedupe(got), false, nil
}

func dedupe(xs []string) []string {
	var out []string
	for _, x := range xs {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// want is the bundle as it is installed for version v.
func (ins Installer) want() map[string][]byte {
	out := make(map[string][]byte, len(ins.Bundle))
	for k, b := range ins.Bundle {
		if k == skills.EntryFile {
			b = toolprofile.StampSkillVersion(b, ins.Version)
		}
		out[k] = b
	}
	return out
}

// Install is skill.install over explicit inputs.
func (ins Installer) Install(ctx context.Context, in InstallIn) (InstallOut, error) {
	ins, err := ins.prepared()
	if err != nil {
		return InstallOut{}, err
	}
	if err := checkLinkMode(in.Link); err != nil {
		return InstallOut{}, err
	}
	names, explicit, err := ins.pick(ctx, in.Tools, in.All)
	if err != nil {
		return InstallOut{}, err
	}
	out := InstallOut{Installed: []Installed{}, Skipped: []Skipped{}, DryRun: in.DryRun}
	want := ins.want()
	for _, name := range names {
		p, ok := ins.Registry.Lookup(name)
		if !ok { // a chooser named a tool the registry lacks
			out.Skipped = append(out.Skipped, Skipped{Tool: name, Why: "not a known tool profile"})
			continue
		}
		if !p.HasSkillDir() {
			if explicit {
				return InstallOut{}, errs.New(errs.Unsupported, p.NoSkillDirReason()).
					WithHint("run lore render in the repo; its pointer file loads .lore/LORE.md")
			}
			out.Skipped = append(out.Skipped, Skipped{Tool: name, Why: p.NoSkillDirReason()})
			continue
		}
		res, ierr := ins.installOne(p, want, in)
		if ierr != nil {
			if !explicit && errs.CodeOf(ierr) == errs.SkillLinked {
				// --all or the default list: one linked folder does not stop
				// the rest; its refusal is the skip reason, word for word.
				out.Skipped = append(out.Skipped, Skipped{Tool: name, Why: skipText(ierr)})
				continue
			}
			return InstallOut{}, ierr
		}
		out.Installed = append(out.Installed, res)
	}
	return out, nil
}

// checkLinkMode refuses a --link value other than replace or update.
func checkLinkMode(m LinkMode) error {
	if m == LinkRefuse || slices.Contains(LinkModes(), m) {
		return nil
	}
	return errs.New(errs.InvalidInput, fmt.Sprintf("--link %q: not a link mode", string(m))).
		WithHint("use --link replace (lore's own copy replaces the link) or --link update (write into the link's target)")
}

// skipText is a refusal as a skip line: the message, then its hint.
func skipText(err error) string {
	var e *errs.Error
	if errors.As(err, &e) && e.Hint != "" {
		return e.Error() + "; " + e.Hint
	}
	return err.Error()
}

// installOne installs the bundle for one profile. A skill folder that is a
// symbolic link (or holds linked bundle files) is handled by in.Link: see
// LinkMode for the decision.
func (ins Installer) installOne(p toolprofile.Profile, want map[string][]byte, in InstallIn) (Installed, error) {
	dir := p.SkillPath(ins.Home)
	res := Installed{Tool: p.Name, Dir: dir, Version: ins.Version}
	pl, err := locate(dir)
	if err != nil {
		return res, wrapRead(dir, err)
	}
	res.Link = pl.Link
	t := tree{Files: map[string][]byte{}, Links: map[string]string{}}
	if !pl.Dangling {
		if t, err = readTree(pl.Real); err != nil {
			return res, wrapRead(dir, err)
		}
	}
	have := t.Files
	linkedFiles := t.linkedBundleFiles(want)
	if !pl.linked() && len(linkedFiles) == 0 {
		return ins.installPlain(res, dir, t, want, in.DryRun)
	}
	if !pl.Dangling && sameFiles(have, want) {
		return res, nil // the link's target already holds this exact skill
	}
	switch in.Link {
	case LinkReplace:
		if !pl.linked() {
			// Only files inside lore's own folder are links: the swap below
			// puts real copies in their place; the link targets are untouched.
			return ins.installPlain(res, dir, t, want, in.DryRun)
		}
		res.Changed, res.Via = true, viaReplacedLink
		if in.DryRun {
			return res, nil
		}
		if err := swapDir(dir, want, nil); err != nil {
			return res, errs.Wrap(errs.Internal, "replace the link "+dir+" with lore's own copy", err).
				WithHint("check that " + filepath.Dir(dir) + " is writable, then run lore skill install again")
		}
		return res, nil
	case LinkUpdate:
		if pl.Dangling {
			return res, errs.New(errs.SkillLinked, fmt.Sprintf("cannot update the target of %s: the link points to %s, which does not exist", dir, pl.Link)).
				WithHint(fmt.Sprintf("replace the link with lore's own copy: lore skill install --tool %s --link replace", p.Name))
		}
		return ins.updateInPlace(res, pl.Real, t, want, in.DryRun)
	}
	return res, linkRefusal(p.Name, pl, t, linkedFiles, ins.Version)
}

// wrapRead is a skill folder that could not be read; a link loop keeps its
// own code.
func wrapRead(dir string, err error) error {
	if errs.CodeOf(err) == errs.SymlinkLoop {
		return err
	}
	return errs.Wrap(errs.Internal, "read "+dir, err)
}

// linkRefusal is the E_SKILL_LINKED refusal: which link, where it ends,
// what is there, and both ways out.
func linkRefusal(tool string, pl place, t tree, linkedFiles []string, version string) error {
	var what string
	entry, hasEntry := t.Files[skills.EntryFile]
	switch {
	case pl.Dangling:
		what = "does not exist"
	case !hasEntry:
		what = "holds no SKILL.md"
	case !isLoreSkill(entry):
		what = "holds a SKILL.md that is not lore's"
	default:
		v, ok := toolprofile.ReadSkillVersion(entry)
		switch {
		case !ok:
			what = "holds a lore skill with no version line"
		case toolprofile.NormVersion(v) != version:
			what = "holds lore skill " + toolprofile.NormVersion(v) + ", not " + version
		default:
			what = "holds lore skill " + version + " with files that differ from this binary's (edited by hand?)"
		}
	}
	var msg string
	if pl.linked() {
		msg = fmt.Sprintf("%s is a symbolic link to %s, which %s", pl.Dir, pl.Link, what)
	} else {
		first := linkedFiles[0]
		msg = fmt.Sprintf("%s in %s is a symbolic link to %s, and the folder %s", first, pl.Dir, t.Links[linkPrefix(t, first)], what)
	}
	return errs.New(errs.SkillLinked, msg+"; lore does not write through a link without being told to").
		WithHint(fmt.Sprintf("replace the link with lore's own copy: lore skill install --tool %s --link replace; or write lore %s's skill into the link's target: lore skill install --tool %s --link update", tool, version, tool))
}

// linkPrefix is the entry of t.Links that rel is, or sits under.
func linkPrefix(t tree, rel string) string {
	for l := range t.Links {
		if rel == l || strings.HasPrefix(rel, l+"/") {
			return l
		}
	}
	return rel
}

// updateInPlace writes want into the link's target real, file by file:
// only bundle files that differ are written (through any linked file to
// its end), hand-edited ones are copied to .bak first, nothing is removed.
func (ins Installer) updateInPlace(res Installed, real string, t tree, want map[string][]byte, dryRun bool) (Installed, error) {
	have := t.Files
	res.Changed, res.Via = true, viaUpdatedTarget
	backups := ins.editedFiles(have)
	for _, rel := range backups {
		res.Backup = append(res.Backup, filepath.Join(real, filepath.FromSlash(rel))+ins.backupSuffix(have, rel))
	}
	if dryRun {
		return res, nil
	}
	for i, rel := range backups {
		if err := writeFileAt(res.Backup[i], have[rel]); err != nil {
			return res, errs.Wrap(errs.Internal, "keep a copy of "+rel+" in "+real, err)
		}
	}
	rels := make([]string, 0, len(want))
	for rel := range want {
		rels = append(rels, rel)
	}
	slices.Sort(rels)
	for _, rel := range rels {
		if h, ok := have[rel]; ok && bytes.Equal(h, want[rel]) {
			continue
		}
		if err := writeFileAt(filepath.Join(real, filepath.FromSlash(rel)), want[rel]); err != nil {
			return res, errs.Wrap(errs.Internal, "update "+rel+" in "+real, err).
				WithHint("check that " + real + " is writable, or use --link replace")
		}
	}
	return res, nil
}

// writeFileAt writes b at p (following a link at p), making its folder.
// An existing file keeps its mode.
func writeFileAt(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), installDirMode); err != nil {
		return err
	}
	return os.WriteFile(p, b, installFileMode)
}

// installPlain installs into a folder lore owns (no link in the way).
func (ins Installer) installPlain(res Installed, dir string, t tree, want map[string][]byte, dryRun bool) (Installed, error) {
	have := t.Files
	res.Changed = !sameFiles(have, want)
	if !res.Changed {
		return res, nil
	}
	backups := ins.editedFiles(have)
	if dryRun {
		for _, rel := range backups {
			res.Backup = append(res.Backup, filepath.Join(dir, filepath.FromSlash(rel))+ins.backupSuffix(have, rel))
		}
		return res, nil
	}
	// The person's own links (a linked file or folder lore does not ship)
	// are put back as links, never turned into copies.
	links := map[string]string{}
	for l := range t.Links {
		if wantsUnder(want, l) || insideLink(t.Links, l) {
			continue // a bundle path (--link replace asked for lore's copy), or inside a kept link
		}
		raw, err := os.Readlink(filepath.Join(dir, filepath.FromSlash(l)))
		if err != nil {
			return res, errs.Wrap(errs.Internal, "read the link "+l+" in "+dir, err)
		}
		links[l] = raw
	}
	keep := map[string][]byte{}
	for rel, b := range have { // the user's own files and earlier backups survive
		if _, ours := want[rel]; !ours && !underAny(links, rel) {
			keep[rel] = b
		}
	}
	for _, rel := range backups {
		bk := rel + ins.backupSuffix(have, rel)
		keep[bk] = have[rel]
		res.Backup = append(res.Backup, filepath.Join(dir, filepath.FromSlash(bk)))
	}
	for rel, b := range want {
		keep[rel] = b
	}
	if err := swapDir(dir, keep, links); err != nil {
		return res, errs.Wrap(errs.Internal, "install the skill into "+dir, err).
			WithHint("check that " + filepath.Dir(dir) + " is writable, then run lore skill install again")
	}
	return res, nil
}

// editedFiles lists the bundle files the user changed by hand: only when
// the installed SKILL.md carries this binary's version (an older install
// differs because the bundle changed, ONB 4.7.1), each file that differs
// from the embedded one (SKILL.md minus its version line).
func (ins Installer) editedFiles(have map[string][]byte) []string {
	entry, ok := have[skills.EntryFile]
	if !ok {
		return nil
	}
	v, hasLine := toolprofile.ReadSkillVersion(entry)
	if !hasLine || toolprofile.NormVersion(v) != ins.Version {
		return nil
	}
	var out []string
	for rel, b := range have {
		emb, ours := ins.Bundle[rel]
		switch {
		case !ours:
		case rel == skills.EntryFile && toolprofile.SkillModified(b, emb):
			out = append(out, rel)
		case rel != skills.EntryFile && !bytes.Equal(b, emb):
			out = append(out, rel)
		}
	}
	slices.Sort(out)
	return out
}

// backupSuffix is ".bak-<date>", with the time added when that name is
// taken.
func (ins Installer) backupSuffix(have map[string][]byte, rel string) string {
	now := ins.Now()
	s := backupSep + now.Format(backupDateLayout)
	if _, taken := have[rel+s]; taken {
		s += "-" + now.Format(backupTimeLayout)
	}
	return s
}

// wantsUnder reports whether a bundle file is l or sits under it.
func wantsUnder(want map[string][]byte, l string) bool {
	for rel := range want {
		if rel == l || strings.HasPrefix(rel, l+"/") {
			return true
		}
	}
	return false
}

// insideLink reports whether l sits strictly under another link of links.
func insideLink(links map[string]string, l string) bool {
	for o := range links {
		if o != l && strings.HasPrefix(l, o+"/") {
			return true
		}
	}
	return false
}

// underAny reports whether rel is one of links or sits under one.
func underAny(links map[string]string, rel string) bool {
	for l := range links {
		if rel == l || strings.HasPrefix(rel, l+"/") {
			return true
		}
	}
	return false
}

// sameFiles reports whether have holds every wanted file with the same
// bytes (extra files of the user's are allowed).
func sameFiles(have, want map[string][]byte) bool {
	for rel, b := range want {
		if h, ok := have[rel]; !ok || !bytes.Equal(h, b) {
			return false
		}
	}
	return true
}

// swapDir replaces dir with a folder holding exactly files (and links, a
// slash path to the link's own target text): written into a
// temp folder beside dir, then renamed into place (the old folder moved
// aside first and removed after), so a reader never sees half a bundle. A
// failed second rename puts the old folder back.
func swapDir(dir string, files map[string][]byte, links map[string]string) error {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, installDirMode); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, tmpPattern)
	if err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			_ = os.RemoveAll(tmp) // never leave a half-written temp folder
		}
	}()
	for rel, b := range files {
		p := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), installDirMode); err != nil {
			return err
		}
		if err := os.WriteFile(p, b, installFileMode); err != nil {
			return err
		}
	}
	for rel, raw := range links {
		p := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), installDirMode); err != nil {
			return err
		}
		if err := os.Symlink(raw, p); err != nil {
			return err
		}
	}
	var old string
	if _, err := os.Lstat(dir); err == nil { // a link (even a dangling one) moves aside as a link
		o, terr := os.MkdirTemp(parent, oldPattern)
		if terr != nil {
			return terr
		}
		if err := os.Remove(o); err != nil { // only the unique name is wanted
			return err
		}
		if err := os.Rename(dir, o); err != nil {
			return err
		}
		old = o
	}
	if err := os.Rename(tmp, dir); err != nil {
		if old != "" {
			_ = os.Rename(old, dir) // best effort: put the old bundle back
		}
		return err
	}
	done = true
	if old != "" {
		_ = os.RemoveAll(old) // the new bundle is in place; a leftover is harmless
	}
	return nil
}
