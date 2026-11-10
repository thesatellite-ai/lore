package wire

import (
	"context"
	"io"

	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/adapters/selfupdate"
)

// UpgradeNotice runs the once-a-day new-release notice (and the opt-in
// auto_upgrade) after a command printed its output (FORMAT_EVOLUTION 8.6,
// 8.7; adapters/selfupdate.Notice holds the rules). args is the command
// line after the binary name; w is stderr and stderrTTY says whether it is
// a terminal. It never fails and writes at most one line: an unreadable
// config.yaml keeps the defaults (notice on, no auto upgrade), and an
// agent caller (identity chain) hears nothing.
func (a *App) UpgradeNotice(ctx context.Context, args []string, stderrTTY bool, w io.Writer) {
	settings := selfupdate.DefaultNoticeSettings()
	if cfg, _, err := orgcfg.LoadConfig(a.opts.Home); err == nil { // bad config: `lore config check` reports it
		if cfg.UpgradeNotice != nil {
			settings.Notice = *cfg.UpgradeNotice
		}
		if cfg.AutoUpgrade != nil {
			settings.Auto = *cfg.AutoUpgrade
		}
	}
	dir := a.opts.WorkDir
	if dir == "" {
		dir = a.opts.RepoDir
	}
	n := selfupdate.Notice{
		Updater:   selfupdate.Updater{Version: a.opts.Version, Home: a.opts.Home, Dir: dir, ReleaseBase: selfupdate.DefaultReleaseBase, VersionProbe: selfupdate.ProbeVersion},
		Args:      args,
		StderrTTY: stderrTTY,
		Settings:  settings,
		IsAgent:   func() bool { return a.CallerAgent(ctx) != "" },
	}
	n.Run(ctx, w)
}
