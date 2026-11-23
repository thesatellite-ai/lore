package skill

// refresh.go — keep the installed skill as current as the binary (H-185).
// `lore skill install` stamps the skill once; `brew upgrade lore` and a
// source checkout that lags never touch it again, so agents kept reading a
// skill without `lore search rebuild` or `lore ask` and said lore had no
// such command. skill.refresh re-installs every installed skill that is
// older than this release binary:
//
//	plain folder lore owns   ─▶ re-installed now (the install rules: the
//	                            whole folder swapped in, own files kept)
//	link into a lore source  ─▶ never overwritten: "git -C <checkout> pull"
//	  checkout
//	any other link           ─▶ never written through: the install command
//	                            with --link (E_SKILL_LINKED's two choices)
//
// Every lore command runs it at most once a day and right after the binary
// changed (wire.SkillAutoRefresh); `lore upgrade` runs the new binary's,
// and `lore doctor --fix` runs it too.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// OpRefresh is the operation name (`lore skill refresh`).
const OpRefresh = "skill.refresh"

// statusStale is the status column of `lore skill status` for a stale
// skill (the JSON keeps status warn/skip and sets stale).
const statusStale = "stale"

// FixRefresh is the fix of a stale skill in a folder lore owns.
const FixRefresh = "lore skill refresh"

// detailStaleCheckoutFmt replaces "skill is linked from a source
// checkout" when that checkout's skill differs from this release's
// (checkout, binary version). Other stale skills keep their version detail.
const detailStaleCheckoutFmt = "the source checkout %s holds a skill that differs from lore %s's (the checkout is behind this release)"

// markStale sets ts.Stale, ts.Checkout and ts.Fix: an installed skill
// whose version line is a lower release (even with the same files: the
// line is restamped, so doctor stops warning after an upgrade that did not
// change the skill), one with no line that differs from this release's
// bundle, or a source checkout's `dev` skill that differs. A development
// binary cannot say which skill matches it, so it never marks.
func (ins Installer) markStale(ts *ToolStatus, pl place, entry []byte) {
	if !ts.Installed || toolprofile.IsDevBuild(ins.Version) {
		return
	}
	v, hasLine := toolprofile.ReadSkillVersion(entry)
	var stale bool
	switch {
	case !hasLine, toolprofile.NormVersion(v) == toolprofile.SkillVersionDev:
		stale = !ts.Same // no line or a source skill: only the files can say
	default:
		stale = toolprofile.SkillOlder(entry, ins.Version) // a newer skill, or this release edited by hand: not stale
	}
	if !stale {
		return
	}
	ts.Stale = true
	if pl.linked() && isLoreSource(pl.Real) {
		ts.Checkout = gitWorkTree(filepath.Dir(pl.Real))
	}
	switch {
	case ts.Checkout != "":
		ts.Detail = fmt.Sprintf(detailStaleCheckoutFmt, ts.Checkout, ins.Version)
		ts.Fix = "git -C " + ts.Checkout + " pull"
	case ts.Linked:
		ts.Fix = InstallFix(*ts)
	default:
		ts.Fix = FixRefresh
	}
}

// InstallFix is the install command for one tool's skill. A linked folder
// needs --link: replace puts lore's own copy in place of the link and
// leaves its target as it is (the safer of the two; update writes into the
// target).
func InstallFix(ts ToolStatus) string {
	fix := "lore skill install --tool " + ts.Tool
	if ts.Linked {
		fix += " --link " + string(LinkReplace)
	}
	return fix
}

// gitWorkTree is the top folder of the git work tree holding dir (a
// folder with a .git entry, dir or one of its parents), else "". File
// system only: no git process on this path.
func gitWorkTree(dir string) string {
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// RefreshIn is the input of skill.refresh.
type RefreshIn struct {
	DryRun bool `json:"dry_run,omitempty" flag:"dry-run" desc:"show which skills would be refreshed; write nothing"`
}

// Refreshed is one tool whose skill was re-installed.
type Refreshed struct {
	Tool string `json:"tool"`
	Dir  string `json:"dir"`
	// From is the version the old skill carried; empty when it had no line.
	From   string   `json:"from,omitempty" desc:"the lore_version the old skill carried (empty: no version line)"`
	To     string   `json:"to" desc:"the lore_version stamped now"`
	Backup []string `json:"backup,omitempty" desc:"copies of hand-edited files kept before overwriting"`
}

// StaleLeft is a stale skill lore did not touch (a link), with the one
// command that makes it current.
type StaleLeft struct {
	Tool   string `json:"tool"`
	Dir    string `json:"dir"`
	Detail string `json:"detail"`
	Fix    string `json:"fix"`
}

// RefreshOut is the output of skill.refresh.
type RefreshOut struct {
	BinaryVersion string      `json:"binary_version"`
	Refreshed     []Refreshed `json:"refreshed"`
	Left          []StaleLeft `json:"left"`
	DryRun        bool        `json:"dry_run,omitempty"`
}

// Text prints one line per refreshed or left skill, or the one line that
// says nothing was stale.
func (o RefreshOut) Text() string {
	var b strings.Builder
	if len(o.Refreshed) == 0 && len(o.Left) == 0 {
		if toolprofile.IsDevBuild(o.BinaryVersion) {
			fmt.Fprintf(&b, "lore %s is a development build: no skill is compared (lore skill install --all installs this build's skill)\n", o.BinaryVersion)
		} else {
			fmt.Fprintf(&b, "every installed lore skill is current (lore %s)\n", o.BinaryVersion)
		}
		return b.String()
	}
	verb := "refreshed"
	if o.DryRun {
		verb = "would refresh"
	}
	for _, r := range o.Refreshed {
		fmt.Fprintf(&b, "%s: %s the lore skill in %s from %s to lore %s\n", r.Tool, verb, r.Dir, fromText(r.From), r.To)
		for _, bk := range r.Backup {
			fmt.Fprintf(&b, "  kept your edited copy as %s\n", bk)
		}
	}
	for _, l := range o.Left {
		fmt.Fprintf(&b, "%s: not refreshed: %s; run %s\n", l.Tool, l.Detail, l.Fix)
	}
	return b.String()
}

// fromText names the old skill's version.
func fromText(v string) string {
	if v == "" {
		return "a skill with no version line"
	}
	return "lore " + v
}

// refreshOp is the registry entry of skill.refresh.
func refreshOp(env InstallEnv) op.Entry {
	return op.Define(op.Spec{
		Name: OpRefresh, Summary: "Re-install every installed lore skill that is older than this lore (a link into a source checkout gets the git pull to run)",
		Permission: op.PermWrite, Access: op.AccessRead, Local: true, RESTNoun: restNoun,
		Errors:   []errs.Code{errs.ConfigInvalid, errs.SymlinkLoop, errs.Internal},
		Examples: []op.Example{{Title: "see which skills are behind this lore", CLI: "lore skill refresh --dry-run", Want: ""}},
	}, func(ctx context.Context, in RefreshIn) (RefreshOut, error) {
		ins, err := newInstaller(ctx, env)
		if err != nil {
			return RefreshOut{}, err
		}
		return ins.Refresh(ctx, in)
	})
}

// Refresh is skill.refresh over explicit inputs: every tool's skill that
// status marks stale is re-installed when lore owns its folder, else left
// with its fix. One tool's failure is left with the install command; the
// rest still run.
func (ins Installer) Refresh(ctx context.Context, in RefreshIn) (RefreshOut, error) {
	ins, err := ins.prepared()
	if err != nil {
		return RefreshOut{}, err
	}
	st, err := ins.Status(ctx, StatusIn{})
	if err != nil {
		return RefreshOut{}, err
	}
	out := RefreshOut{BinaryVersion: ins.Version, Refreshed: []Refreshed{}, Left: []StaleLeft{}, DryRun: in.DryRun}
	want := ins.want()
	for _, ts := range st.Tools {
		if !ts.Stale {
			continue
		}
		p, ok := ins.Registry.Lookup(ts.Tool)
		if ts.Linked || !ok {
			out.Left = append(out.Left, StaleLeft{Tool: ts.Tool, Dir: ts.Where(), Detail: ts.Detail, Fix: ts.Fix})
			continue
		}
		res, ierr := ins.installOne(p, want, InstallIn{DryRun: in.DryRun})
		if ierr != nil {
			out.Left = append(out.Left, StaleLeft{Tool: ts.Tool, Dir: ts.Where(), Detail: skipText(ierr), Fix: InstallFix(ts)})
			continue
		}
		out.Refreshed = append(out.Refreshed, Refreshed{Tool: ts.Tool, Dir: res.Dir, From: ts.Version, To: ins.Version, Backup: res.Backup})
	}
	return out, nil
}
