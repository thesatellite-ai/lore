package machine

import "testing"

// 8.1: every install method from (path, version).
func TestDetectInstall(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		exe, ver string
		want     InstallMethod
	}{
		{"/opt/homebrew/Cellar/lore/0.1.18/bin/lore", "0.1.18", InstallHomebrew},
		{"/home/linuxbrew/.linuxbrew/Cellar/lore/0.1.18/bin/lore", "0.1.0-dev", InstallHomebrew},
		{"/usr/local/bin/lore", "0.1.18", InstallScript},
		{`C:\Users\a\AppData\Local\lore\lore.exe`, "0.1.18", InstallScript},
		{"/usr/local/bin/lore", "0.1.0-dev", InstallSource},
		{"/usr/local/bin/lore", "0.1.18-snapshot", InstallSource},
		{"/usr/local/bin/lore", "", InstallSource},
		{"/space/x/tmp/bin/lore", "v0.1.17-450-g74e4fe6-dirty", InstallSource},
		{"/space/x/tmp/bin/lore", "v0.1.17-450-g74e4fe6", InstallSource},
		{"/home/a/.asdf/installs/lore/0.1.18/bin/lore", "0.1.18", InstallOther},
		{"/nix/store/abc-lore/bin/lore", "0.1.18", InstallOther},
	} {
		if got := DetectInstall(c.exe, c.ver); got != c.want {
			t.Errorf("DetectInstall(%q, %q) = %q, want %q", c.exe, c.ver, got, c.want)
		}
	}
}
