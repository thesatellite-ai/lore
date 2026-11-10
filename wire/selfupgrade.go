package wire

import (
	"context"
	"os"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/selfupdate"
	"github.com/thesatellite-ai/lore/model/errs"
	opsselfupgrade "github.com/thesatellite-ai/lore/ops/selfupgrade"
)

// Upgrader is this App's `lore upgrade` mechanism (opsselfupgrade.Env,
// P3-UPG-1): adapters/selfupdate with the binary's version, the lore home
// and the command's folder (for the project pin). A build without a
// version stamp cannot tell what it would replace, so it is E_UNSUPPORTED.
func (a *App) Upgrader(context.Context) (opsselfupgrade.Upgrader, error) {
	if a.opts.Version == "" {
		return nil, errs.New(errs.Unsupported, "this lore build carries no version, so it cannot upgrade itself").
			WithHint("install a release: https://github.com/thesatellite-ai/lore/releases")
	}
	dir := a.opts.WorkDir
	if dir == "" {
		dir = a.opts.RepoDir
	}
	// No home folder only skips the agent skill bundle copy ("" = skip).
	userHome, herr := os.UserHomeDir()
	if herr != nil {
		userHome = ""
	}
	return selfUpgrader{u: selfupdate.Updater{
		Version: a.opts.Version, Home: a.opts.Home, Dir: dir, UserHome: userHome,
		ReleaseBase: selfupdate.DefaultReleaseBase, VersionProbe: selfupdate.ProbeVersion,
	}}, nil
}

// selfUpgrader adapts selfupdate.Updater to the operation's port.
type selfUpgrader struct{ u selfupdate.Updater }

// Upgrade runs the upgrade and splits what it wrote into lines.
func (s selfUpgrader) Upgrade(ctx context.Context, f opsselfupgrade.Flags, confirmed bool) ([]string, error) {
	var b strings.Builder
	u := s.u
	u.Confirmed = confirmed
	err := u.Run(ctx, &b, selfupdate.Flags{Check: f.Check, Version: f.Version, IgnorePin: f.IgnorePin})
	if err != nil {
		return nil, err
	}
	text := strings.TrimRight(b.String(), "\n")
	if text == "" {
		return []string{}, nil
	}
	return strings.Split(text, "\n"), nil
}
