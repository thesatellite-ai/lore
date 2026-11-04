package formatgate

import (
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Feature is one format feature a project can depend on.
type Feature struct {
	// Name is the string stored in "features" (stable forever).
	Name string
	// Since is the first lore release that reads and writes it correctly;
	// enabling the feature raises the project's min_lore_version to Since.
	Since string
}

// Feature names (file format: never renamed). The 0.1.18 list is the step
// list of FORMAT_EVOLUTION section 3; every one has Since 0.1.18, so a team
// meets one floor at its first 0.1.18 write.
const (
	Live         = "live"
	Signals      = "signals"
	HLC          = "hlc"
	Meta         = "meta"
	Trash        = "trash"
	TaskTree     = "task_tree"
	Time         = "time"
	PlanStatus   = "plan_status"
	Policy       = "policy"
	Resources    = "resources"
	Refs         = "refs"
	Vars         = "vars"
	PersonalFlat = "personal_flat"
)

// since018 is the release that introduced the 0.1.18 features.
const since018 = "0.1.18"

// known is every feature this binary understands (plus the test step's in
// a loretest_format build). A declared name missing here makes the project
// read-only for this binary.
var known = append(slices.Clone(releaseFeatures), testFeatures...)

// releaseFeatures are the features of released lore.
var releaseFeatures = []Feature{
	{Name: Live, Since: "0.1.0"},
	{Name: Signals, Since: "0.1.0"},
	{Name: HLC, Since: since018},
	{Name: Meta, Since: since018},
	{Name: Trash, Since: since018},
	{Name: TaskTree, Since: since018},
	{Name: Time, Since: since018},
	{Name: PlanStatus, Since: since018},
	{Name: Policy, Since: since018},
	{Name: Resources, Since: since018},
	{Name: Refs, Since: since018},
	{Name: Vars, Since: since018},
	{Name: PersonalFlat, Since: since018},
}

// Known returns a copy of the features this binary understands.
func Known() []Feature { return slices.Clone(known) }

// Lookup returns the known feature named name.
func Lookup(name string) (Feature, bool) {
	i := slices.IndexFunc(known, func(f Feature) bool { return f.Name == name })
	if i < 0 {
		return Feature{}, false
	}
	return known[i], true
}

// IsKnown reports whether name is a feature this binary understands.
func IsKnown(name string) bool { _, ok := Lookup(name); return ok }

// Declared is what a project's _meta.json says.
type Declared struct {
	Features       []string
	MinLoreVersion string
}

// pluginPrefix marks a feature owned by a plugin ("plugin/infra"). An
// unknown plugin feature locks only that plugin's data (decision FQ4).
const pluginPrefix = "plugin/"

// Verdict is the gate's answer.
type Verdict struct {
	// Writable is false when the binary must not change anything.
	Writable bool
	// Unknown lists declared core features this binary does not know.
	Unknown []string
	// LockedPlugins lists plugin features this binary does not have; they
	// do not clear Writable (see LockedTable).
	LockedPlugins []string
	// Floor is the version the project needs ("" when only features block).
	Floor string
	// Binary is the version Check compared against.
	Binary string
}

// Check compares a declaration with this binary. Unknown core features
// always block, even when the floor is unparseable: a hand-mangled floor
// must not unlock a format the binary cannot represent.
func Check(binary string, d Declared) Verdict {
	v := Verdict{Binary: binary}
	for _, f := range d.Features {
		switch {
		case IsKnown(f):
		case strings.HasPrefix(f, pluginPrefix):
			v.LockedPlugins = append(v.LockedPlugins, f)
		default:
			v.Unknown = append(v.Unknown, f)
		}
	}
	if VersionBelow(effective(binary), d.MinLoreVersion) {
		v.Floor = d.MinLoreVersion
	}
	v.Writable = len(v.Unknown) == 0 && v.Floor == ""
	return v
}

// LockedTable reports whether a write to table must be refused because the
// plugin that owns it is not installed. ownerOf names a table's plugin (""
// for core tables, which a plugin feature never locks). Until plugins
// exist the composition root passes nil, so no table is locked (stub).
func (v Verdict) LockedTable(table string, ownerOf func(table string) string) bool {
	if ownerOf == nil {
		return false
	}
	owner := ownerOf(table)
	return owner != "" && slices.Contains(v.LockedPlugins, pluginPrefix+owner)
}

// Err is the E_OLD_LORE refusal for a verdict that is not writable (nil
// when it is).
func (v Verdict) Err() error {
	if v.Writable {
		return nil
	}
	this := v.Binary
	if this == "" {
		this = "unknown"
	}
	var msg string
	switch {
	case len(v.Unknown) > 0:
		msg = "this project uses a lore feature this binary does not know: " + strings.Join(v.Unknown, ", ")
		if v.Floor != "" {
			msg += " (needs lore " + v.Floor + " or later; this is " + this + ")"
		}
	default:
		msg = "this project needs lore " + v.Floor + " or later to write (this is " + this + ")"
	}
	return errs.New(errs.OldLore, msg+". Reading works; writing is off until you upgrade.").WithHint("lore upgrade")
}

// Unreadable is the fail-closed refusal for a _meta.json that cannot be
// read (FORMAT_EVOLUTION section 4): an unreadable declaration counts as
// unknown features, so nothing is written and the file is never replaced.
func Unreadable(path, reason string) error {
	return errs.New(errs.OldLore, "this project's "+path+" cannot be read ("+reason+"); lore is read-only here until it is fixed").
		WithHint("restore " + path + " from git history (git log -p -- " + path + "), then retry; lore doctor shows the features the files need")
}

// Raise adds need to d: each name d does not list yet is added (sorted),
// and min_lore_version is raised to the highest Since of the added names
// (never lowered). added lists the names that were new. Unknown names are
// added without a floor change (the caller only passes known ones).
func Raise(d Declared, need []string) (out Declared, added []string) {
	out = Declared{Features: slices.Clone(d.Features), MinLoreVersion: d.MinLoreVersion}
	for _, n := range need {
		if n == "" || slices.Contains(out.Features, n) {
			continue
		}
		out.Features = append(out.Features, n)
		added = append(added, n)
		if f, ok := Lookup(n); ok && VersionBelow(out.MinLoreVersion, f.Since) {
			out.MinLoreVersion = f.Since
		}
	}
	slices.Sort(out.Features)
	return out, added
}

// Merge is the _meta.json merge rule of both sides: features as a set
// (sorted), min_lore_version the higher version (version-aware, so 0.1.19
// is above 0.1.9). An unparseable floor never wins over a parseable one.
func Merge(a, b Declared) Declared {
	fs := slices.Clone(a.Features)
	for _, f := range b.Features {
		if !slices.Contains(fs, f) {
			fs = append(fs, f)
		}
	}
	slices.Sort(fs)
	return Declared{Features: fs, MinLoreVersion: MaxVersion(a.MinLoreVersion, b.MinLoreVersion)}
}
