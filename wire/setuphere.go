package wire

// setuphere.go — the current folder's context for guided setup (fix-all
// item 3): gathers onboard.HereFacts for the folder the App runs in and
// answers onboard.ResolveHere. `lore setup` / `lore setup list` print its
// one line (onboard.HereLine); the setup page's session answer carries it
// for the Home banner.
//
//	git toplevel ─▶ LoreRepoAt ─▶ org (orgcfg.Gather, no setup hint, no
//	store links) ─▶ .lore/stores.yaml names ─▶ identity chain ─▶ git
//	user.email ─▶ 3.3 resolution ─▶ ResolveHere(setups[])
//
// Reads only. Outside a lore repo nothing past the git toplevel is read:
// opening the repo store there would write .lore/, hooks and
// .gitattributes before the person chose anything (A13 bug 3).

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/onboard"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

var _ opssetup.HereSource = (*App)(nil)

// SetupHere implements opssetup.HereSource: the context of the folder the
// App runs in. Every fact is best effort: one that cannot be read is left
// empty (the line and the banner say "no org", "unknown identity") rather
// than failing `lore setup list`; only an unreadable config.yaml is an
// error, since every other command refuses it too.
func (a *App) SetupHere(ctx context.Context) (onboard.Here, error) {
	home, dir := a.opts.Home, a.projectDir()
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return onboard.Here{}, errs.Wrap(errs.Internal, "setup here: working folder", err)
		}
		dir = wd
	}
	cfg, err := ReadSetupsConfig(home)
	if err != nil {
		return onboard.Here{}, err
	}
	f := onboard.HereFacts{Dir: dir, Root: gitTopLevel(ctx, dir)}
	f.Git = f.Root != ""
	f.Lore = f.Git && opssetup.LoreRepoAt(dir)
	if uh, uerr := os.UserHomeDir(); uerr == nil {
		f.UserHome = uh
	}
	if !f.Lore {
		return onboard.ResolveHere(f, cfg.Enabled()), nil
	}
	f.Pointers = herePointers(f.Root)
	if s, gerr := orgcfg.Gather(ctx, orgcfg.In{Home: home, Dir: dir, EnvOrg: strings.TrimSpace(os.Getenv(orgcfg.EnvOrg))}); gerr == nil {
		if s.ResolveErr == nil {
			f.Org, f.OrgSource = s.Result.OrgName(), s.Result.MatchedBy
		}
		if len(s.Remotes) > 0 {
			f.Remote = s.Remotes[0]
		}
	}
	f.Stores, f.StoresSource = hereStores(f.Root)
	if id, ierr := resolveIdentity(ctx, a.opts); ierr == nil {
		f.IdentityName, f.IdentityKey, f.IdentitySource = id.DisplayName, id.StableKey, string(id.Step)
	}
	if out, gerr := (gitRun{dir: f.Root, args: []string{"config", "user.email"}}).output(ctx); gerr == nil {
		f.GitEmail = strings.TrimSpace(string(out))
	}
	res, rerr := a.SetupResolution(ctx, "")
	f.ActiveName, f.ActiveWhy, f.ActiveDetail = res.Active.Name, res.Active.Why, res.Active.Detail
	if rerr != nil {
		f.ActiveError = errs.EnvelopeOf(rerr).Error.Message
	}
	return onboard.ResolveHere(f, cfg.Enabled()), nil
}

// herePointers lists the built-in tool pointer files in root that point
// at .lore/LORE.md (toolprofile.CanonicalOutput), in the profiles' order.
// A user profile's pointer file is not looked for: the this_folder step
// only needs to say whether any tool reads lore here.
func herePointers(root string) []string {
	var out []string
	for _, p := range toolprofile.Builtins() {
		if !p.HasPointerFile() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p.PointerFile)))
		if err == nil && strings.Contains(string(b), toolprofile.CanonicalOutput) {
			out = append(out, p.PointerFile)
		}
	}
	return out
}

// hereStores lists the store names of root's stores.yaml and the file they
// came from; none when the file is absent or does not parse (lore doctor
// reports an invalid file).
func hereStores(root string) ([]string, string) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(attach.FileName)))
	if err != nil {
		return nil, ""
	}
	c, err := attach.Parse(b)
	if err != nil {
		return nil, ""
	}
	names := make([]string, 0, len(c.Stores))
	for _, s := range c.Stores {
		names = append(names, string(s.Name))
	}
	return names, attach.FileName
}
