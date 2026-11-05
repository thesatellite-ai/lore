// upgrade_notice.go — the once-a-day new-release notice and the opt-in
// automatic upgrade (FORMAT_EVOLUTION 8.6, 8.7).
//
//	main (after the command printed) ─▶ maybeUpgradeNotice(args)
//	  quiet cases (CI, stderr not a tty, --json, LORE_NO_UPGRADE_NOTICE,
//	  upgrade_notice: false, source build, `upgrade` itself) ─▶ return
//	  ~/.lore/state/upgrade-check.json younger than 24 h ─▶ return
//	  agent caller ─▶ return (checked last: it resolves identity)
//	  latestTag (2 s) ─▶ record ─▶ newer? (a pinned project: only a newer pin)
//	    auto_upgrade: true + brew/script ─▶ runUpgrade, one line
//	    else ─▶ one notice line on stderr
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"saas/pkg/aicoder/lorecfg"

	"github.com/thesatellite-ai/lore/logic/formatgate"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// noticeTimeout bounds the daily check (8.6: 2 seconds).
const noticeTimeout = 2 * time.Second

// noticeInterval is how often one machine checks.
const noticeInterval = 24 * time.Hour

// upgradeCheckFile is the state file under ~/.lore/state (CONFIG_GUIDE).
const upgradeCheckFile = "upgrade-check.json"

// envNoUpgradeNotice turns the notice off for one shell or CI job.
const envNoUpgradeNotice = "LORE_NO_UPGRADE_NOTICE"

// upgradeCheck is upgrade-check.json.
type upgradeCheck struct {
	CheckedAt time.Time `json:"checked_at"`
	Newest    string    `json:"newest,omitempty"`
}

// upgradeSettings are the two ~/.lore/config.yaml keys of 8.6 and 8.7.
type upgradeSettings struct{ notice, auto bool }

// readUpgradeSettings: upgrade_notice defaults on, auto_upgrade off. An
// unreadable config keeps the defaults (the notice must never fail a command).
func readUpgradeSettings(home string) upgradeSettings {
	s := upgradeSettings{notice: true}
	b, err := os.ReadFile(filepath.Clean(filepath.Join(home, "config.yaml")))
	if err != nil {
		return s
	}
	var c lorecfg.Config
	if yaml.Unmarshal(b, &c) != nil {
		return s
	}
	if c.UpgradeNotice != nil {
		s.notice = *c.UpgradeNotice
	}
	if c.AutoUpgrade != nil {
		s.auto = *c.AutoUpgrade
	}
	return s
}

// noticeQuiet reports the cases where no network request is made at all.
func noticeQuiet(args []string, stderrTTY bool) bool {
	return os.Getenv(envCI) != "" || !stderrTTY || slices.Contains(args, "--json") ||
		os.Getenv(envNoUpgradeNotice) == envValueOn || isDevBuild(version) ||
		(len(args) > 0 && (args[0] == "upgrade" || args[0] == "version"))
}

// maybeUpgradeNotice runs after a command finished and printed its output.
// It never fails and never prints more than one line.
func maybeUpgradeNotice(args []string) {
	if noticeQuiet(args, term.IsTerminal(int(os.Stderr.Fd()))) {
		return
	}
	home, err := loreHome()
	if err != nil {
		return
	}
	settings := readUpgradeSettings(home)
	if !settings.notice && !settings.auto {
		return
	}
	statePath := filepath.Join(home, "state", upgradeCheckFile)
	if !checkDue(statePath, time.Now()) || callerIsAgent() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), noticeTimeout)
	defer cancel()
	newest, err := newReleaseClient(noticeTimeout).latestTag(ctx)
	// A failed check is silent and retried after 24 hours.
	recordCheck(statePath, upgradeCheck{CheckedAt: time.Now().UTC(), Newest: newest})
	if err != nil {
		return
	}
	target := newest
	if cwd, werr := os.Getwd(); werr == nil {
		if _, m, ok, _ := findProjectMeta(cwd); ok {
			if p := effectivePin(m); p.pin != "" {
				target = p.pin // a pinned project hears only about a newer pin
			}
		}
	}
	if !formatgate.VersionBelow(version, target) {
		return
	}
	noticeOrAuto(os.Stderr, settings, target)
}

// noticeOrAuto prints the notice line, or with auto_upgrade upgrades and
// prints one line saying it did or why not (8.7: never past a pin, never
// older, only brew and script installs; any failure leaves the binary).
func noticeOrAuto(w io.Writer, s upgradeSettings, target string) {
	have, want := normVersion(version), normVersion(target)
	if s.auto {
		exe, err := resolvedExe()
		if err == nil {
			if m := detectInstall(exe, version); m != installBrew && m != installScript {
				fmt.Fprintf(w, "%s %s is available; not upgraded automatically (installed by %s). Run: %s upgrade\n", BinaryName, want, m, BinaryName)
				return
			}
		}
		if err == nil {
			err = runUpgrade(context.Background(), io.Discard, upgradeFlags{version: tagOf(want), ignorePin: false})
		}
		if err != nil {
			fmt.Fprintf(w, "%s %s is available; automatic upgrade failed (%v). Run: %s upgrade\n", BinaryName, want, err, BinaryName)
			return
		}
		fmt.Fprintf(w, "%s upgraded %s → %s\n", BinaryName, have, want)
		return
	}
	if !s.notice {
		return
	}
	fmt.Fprintf(w, "%s %s is available (you have %s). Run: %s upgrade    (turn off: upgrade_notice: false in ~/.lore/config.yaml)\n",
		BinaryName, want, have, BinaryName)
}

// checkDue reports whether 24 hours passed since the last check.
func checkDue(path string, now time.Time) bool {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return true
	}
	var c upgradeCheck
	if json.Unmarshal(b, &c) != nil {
		return true
	}
	return now.Sub(c.CheckedAt) >= noticeInterval
}

// recordCheck writes upgrade-check.json atomically; failures are ignored
// (the next command simply checks again).
func recordCheck(path string, c upgradeCheck) {
	b, err := json.Marshal(c)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) != nil {
		return
	}
	_ = os.Rename(tmp, path) // best effort; a stale file only means one more check
}
