package selfupdate

// notice.go — the once-a-day new-release notice and the opt-in automatic
// upgrade (FORMAT_EVOLUTION 8.6, 8.7), moved from the old binary's
// upgrade_notice.go so every binary runs the same rules (B11).
//
//	binary (after the command printed) ─▶ Notice.Run(ctx, stderr)
//	  quiet cases (CI, stderr not a tty, --json, LORE_NO_UPGRADE_NOTICE=1,
//	  upgrade_notice and auto_upgrade both off, source build, `upgrade` or
//	  `version` itself) ─▶ return, no network request
//	  <home>/state/upgrade-check.json younger than 24 h ─▶ return
//	  agent caller ─▶ return (checked last: it resolves identity)
//	  newest release tag (2 s) ─▶ record ─▶ newer? (a pinned project: only a newer pin)
//	    auto_upgrade + brew/script ─▶ Updater.Run, one line
//	    else ─▶ one notice line

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/formatgate"
)

// noticeTimeout bounds the daily check (8.6: 2 seconds).
const noticeTimeout = 2 * time.Second

// NoticeInterval is how often one machine checks (8.6: once a day).
const NoticeInterval = 24 * time.Hour

// UpgradeCheckFile is the state file under <lore home>/state (CONFIG_GUIDE).
const UpgradeCheckFile = "upgrade-check.json"

// upgradeStateDir is the folder of UpgradeCheckFile under the lore home.
const upgradeStateDir = "state"

// EnvNoUpgradeNotice set to 1 turns the notice off for one shell or CI job.
const EnvNoUpgradeNotice = "LORE_NO_UPGRADE_NOTICE"

// envValueOn is the "on" value of the boolean environment switches.
const envValueOn = "1"

// jsonFlag in a command line means machine output: never add a line.
const jsonFlag = "--json"

// quietCommands never carry the notice: the upgrade itself, and version
// (scripts parse it).
var quietCommands = []string{"upgrade", "version"}

// noticeStateMode / noticeStateDirMode: the state file is the person's own.
const (
	noticeStateMode    os.FileMode = 0o600
	noticeStateDirMode os.FileMode = 0o700
)

// NoticeSettings are the two ~/.lore/config.yaml keys of 8.6 and 8.7.
type NoticeSettings struct {
	// Notice is upgrade_notice (default true).
	Notice bool
	// Auto is auto_upgrade (default false).
	Auto bool
}

// DefaultNoticeSettings is what an absent or unreadable config means: the
// notice on, no automatic upgrade.
func DefaultNoticeSettings() NoticeSettings { return NoticeSettings{Notice: true} }

// upgradeCheck is upgrade-check.json.
type upgradeCheck struct {
	CheckedAt time.Time `json:"checked_at"`
	Newest    string    `json:"newest,omitempty"`
}

// Notice is one daily check. The caller names everything (no globals):
// the Updater (version, lore home, folder for the pin, release base), the
// command line, whether stderr is a terminal, the settings and how to
// tell an agent caller.
type Notice struct {
	Updater
	// Args is the command line after the binary name.
	Args []string
	// StderrTTY is true when the notice's stream is a terminal.
	StderrTTY bool
	// Settings are upgrade_notice and auto_upgrade.
	Settings NoticeSettings
	// IsAgent reports an agent caller (no notice for agents); nil = never.
	IsAgent func() bool
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Exe is the running binary's path for the install method; nil means
	// os.Executable with symlinks resolved. Tests fake it.
	Exe func() (string, error)
}

// IsDevBuild reports a source or snapshot build (FORMAT_EVOLUTION 8.1): an
// unstamped "dev", "…-dev" or a snapshot never hears about releases.
func IsDevBuild(v string) bool {
	return v == "" || v == "dev" || strings.HasSuffix(v, "-dev") || strings.Contains(v, "snapshot")
}

// Quiet reports the cases where no network request is made at all.
func (n Notice) Quiet() bool {
	return os.Getenv(envCI) != "" || !n.StderrTTY || slices.Contains(n.Args, jsonFlag) ||
		os.Getenv(EnvNoUpgradeNotice) == envValueOn || IsDevBuild(n.Version) ||
		(len(n.Args) > 0 && slices.Contains(quietCommands, n.Args[0])) ||
		(!n.Settings.Notice && !n.Settings.Auto)
}

// now is the notice's clock.
func (n Notice) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

// statePath is <home>/state/upgrade-check.json.
func (n Notice) statePath() string {
	return filepath.Join(n.Home, upgradeStateDir, UpgradeCheckFile)
}

// Run runs after a command finished and printed its output. It never
// fails the command and never writes more than one line to w.
func (n Notice) Run(ctx context.Context, w io.Writer) {
	if n.Quiet() || n.Home == "" {
		return
	}
	path := n.statePath()
	if !checkDue(path, n.now()) || (n.IsAgent != nil && n.IsAgent()) {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, noticeTimeout)
	defer cancel()
	newest, err := newReleaseClient(n.ReleaseBase, noticeTimeout, n.Version).latestTag(cctx)
	// A failed check is silent and retried after 24 hours.
	recordCheck(path, upgradeCheck{CheckedAt: n.now().UTC(), Newest: newest})
	if err != nil {
		return
	}
	target := newest
	if _, m, ok, merr := findProjectMeta(n.Dir); merr == nil && ok {
		if p := effectivePin(m); p.pin != "" {
			target = p.pin // a pinned project hears only about a newer pin
		}
	}
	if !formatgate.VersionBelow(n.Version, target) {
		return
	}
	// The notice's only channel is w (stderr): a failed write there has
	// nowhere else to go.
	_ = n.noticeOrAuto(ctx, w, target)
}

// noticeOrAuto prints the notice line, or with auto_upgrade upgrades and
// prints one line saying it did or why not (8.7: never past a pin, never
// older, only brew and script installs; any failure leaves the binary).
func (n Notice) noticeOrAuto(ctx context.Context, w io.Writer, target string) error {
	have, want := normVersion(n.Version), normVersion(target)
	if n.Settings.Auto {
		exeOf := n.Exe
		if exeOf == nil {
			exeOf = resolvedExe
		}
		exe, err := exeOf()
		if err == nil {
			if m := detectInstall(exe, n.Version); m != installBrew && m != installScript {
				_, werr := fmt.Fprintf(w, "%s %s is available; not upgraded automatically (installed by %s). Run: %s upgrade\n", BinaryName, want, m, BinaryName)
				return werr
			}
			err = n.Updater.Run(ctx, io.Discard, Flags{Version: tagOf(want)})
		}
		if err != nil {
			_, werr := fmt.Fprintf(w, "%s %s is available; automatic upgrade failed (%v). Run: %s upgrade\n", BinaryName, want, err, BinaryName)
			return werr
		}
		_, werr := fmt.Fprintf(w, "%s upgraded %s → %s\n", BinaryName, have, want)
		return werr
	}
	if !n.Settings.Notice {
		return nil
	}
	_, werr := fmt.Fprintf(w, "%s %s is available (you have %s). Run: %s upgrade    (turn off: upgrade_notice: false in ~/.lore/config.yaml)\n",
		BinaryName, want, have, BinaryName)
	return werr
}

// checkDue reports whether NoticeInterval passed since the last check (no
// or unreadable state: due).
func checkDue(path string, now time.Time) bool {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return true
	}
	var c upgradeCheck
	if json.Unmarshal(b, &c) != nil {
		return true
	}
	return now.Sub(c.CheckedAt) >= NoticeInterval
}

// recordCheck writes upgrade-check.json atomically; failures are ignored
// (the next command simply checks again).
func recordCheck(path string, c upgradeCheck) {
	b, err := json.Marshal(c)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(path), noticeStateDirMode) != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, noticeStateMode) != nil {
		return
	}
	_ = os.Rename(tmp, path) // best effort; a stale file only means one more check
}
