package selfupdate

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestUpgradeNoticeNeverInCI (B11 port of saas/cmd/cli upgrade_test.go):
// the quiet matrix (CI, no tty, --json, LORE_NO_UPGRADE_NOTICE, a dev
// build, `upgrade`/`version` themselves, both settings off) makes no
// request; the 24 h window; the notice line and upgrade_notice false.
func TestUpgradeNoticeNeverInCI(t *testing.T) {
	t.Setenv(envCI, "")
	t.Setenv(EnvNoUpgradeNotice, "")
	base := Notice{Updater: Updater{Version: "0.1.19"}, Args: []string{"task", "list"}, StderrTTY: true, Settings: DefaultNoticeSettings()}
	if base.Quiet() {
		t.Fatal("quiet on a plain terminal command")
	}
	with := func(f func(*Notice)) Notice { n := base; f(&n); return n }
	for name, n := range map[string]Notice{
		"no tty":   with(func(n *Notice) { n.StderrTTY = false }),
		"json":     with(func(n *Notice) { n.Args = []string{"task", "list", "--json"} }),
		"upgrade":  with(func(n *Notice) { n.Args = []string{"upgrade"} }),
		"version":  with(func(n *Notice) { n.Args = []string{"version"} }),
		"dev":      with(func(n *Notice) { n.Version = "dev" }),
		"-dev":     with(func(n *Notice) { n.Version = "0.1.0-dev" }),
		"snapshot": with(func(n *Notice) { n.Version = "0.0.0-snapshot-abc" }),
		"no stamp": with(func(n *Notice) { n.Version = "" }),
		"both off": with(func(n *Notice) { n.Settings = NoticeSettings{} }),
	} {
		if !n.Quiet() {
			t.Errorf("%s: notice not quiet", name)
		}
	}
	if with(func(n *Notice) { n.Settings = NoticeSettings{Auto: true} }).Quiet() {
		t.Error("auto_upgrade alone still checks")
	}
	t.Setenv(EnvNoUpgradeNotice, envValueOn)
	if !base.Quiet() {
		t.Error("LORE_NO_UPGRADE_NOTICE=1: not quiet")
	}
	t.Setenv(EnvNoUpgradeNotice, "")
	t.Setenv(envCI, "true")
	if !base.Quiet() {
		t.Error("CI: notice not quiet")
	}
	t.Setenv(envCI, "")

	now := time.Now()
	p := filepath.Join(t.TempDir(), UpgradeCheckFile)
	if !checkDue(p, now) {
		t.Error("no state file: not due")
	}
	recordCheck(p, upgradeCheck{CheckedAt: now})
	if checkDue(p, now.Add(time.Hour)) || !checkDue(p, now.Add(25*time.Hour)) {
		t.Error("24 h window wrong")
	}

	var buf bytes.Buffer
	if err := base.noticeOrAuto(context.Background(), &buf, "v0.1.20"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "lore 0.1.20 is available (you have 0.1.19)") || strings.Count(buf.String(), "\n") != 1 {
		t.Errorf("notice: %q", buf.String())
	}
	buf.Reset()
	if err := with(func(n *Notice) { n.Settings = NoticeSettings{} }).noticeOrAuto(context.Background(), &buf, "v0.1.20"); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("upgrade_notice false printed %q", buf.String())
	}
	buf.Reset()
	auto := with(func(n *Notice) {
		n.Settings = NoticeSettings{Auto: true}
		n.Exe = func() (string, error) { return "/nix/store/abc-lore/bin/lore", nil }
	})
	if err := auto.noticeOrAuto(context.Background(), &buf, "v0.1.20"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "not upgraded automatically (installed by") {
		t.Errorf("auto_upgrade on an other-manager install: %q", buf.String())
	}
}

// TestUpgradeNoticeRun: against a local release server, a due check prints
// one line and records the check; the next command within 24 h makes no
// request; an agent caller, a pinned project at the pin, and a binary
// already at the newest release print nothing.
func TestUpgradeNoticeRun(t *testing.T) {
	t.Setenv(envCI, "")
	t.Setenv(EnvNoUpgradeNotice, "")
	t.Setenv(envVersionPin, "")
	rel := &fakeRelease{tag: "v0.1.20"}
	hits := 0
	inner := rel.handler()
	srv := newCountingServer(t, inner, &hits)
	home, dir := t.TempDir(), t.TempDir()
	n := Notice{Updater: Updater{Version: "0.1.19", Home: home, Dir: dir, ReleaseBase: srv}, Args: []string{"task", "list"},
		StderrTTY: true, Settings: DefaultNoticeSettings()}
	var buf bytes.Buffer
	n.Run(context.Background(), &buf)
	if !strings.Contains(buf.String(), "lore 0.1.20 is available (you have 0.1.19)") || hits != 1 {
		t.Fatalf("first run: %q, %d request(s)", buf.String(), hits)
	}
	if _, err := os.Stat(filepath.Join(home, upgradeStateDir, UpgradeCheckFile)); err != nil {
		t.Fatalf("check not recorded: %v", err)
	}
	buf.Reset()
	n.Run(context.Background(), &buf)
	if buf.Len() != 0 || hits != 1 {
		t.Fatalf("within 24 h: %q, %d request(s)", buf.String(), hits)
	}

	for name, m := range map[string]Notice{
		"agent": func() Notice {
			c := n
			c.Home = t.TempDir()
			c.IsAgent = func() bool { return true }
			return c
		}(),
		"newest already": func() Notice {
			c := n
			c.Home = t.TempDir()
			c.Version = "0.1.20"
			return c
		}(),
	} {
		buf.Reset()
		m.Run(context.Background(), &buf)
		if buf.Len() != 0 {
			t.Errorf("%s printed %q", name, buf.String())
		}
	}
	// A project pinning the running version hears nothing of 0.1.20.
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, filepath.Dir(metaRelPath)), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, metaRelPath), []byte(`{"lore_version_pin":"0.1.19"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	pinned := n
	pinned.Home = t.TempDir()
	pinned.Dir = proj
	buf.Reset()
	pinned.Run(context.Background(), &buf)
	if buf.Len() != 0 {
		t.Errorf("a project pinned at the running version printed %q", buf.String())
	}
}

// newCountingServer serves h, counting requests into hits; it returns the
// server's base URL and closes it with the test.
func newCountingServer(t *testing.T, h http.Handler, hits *int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}
