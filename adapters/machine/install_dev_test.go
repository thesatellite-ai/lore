package machine

import "testing"

// TestDetectInstallBareDev: the bare "dev" stamp of an unstamped `go build
// ./cmd/lore` is a source build wherever the binary sits (outside a Cellar),
// so `lore upgrade` never swaps it for a release; near misses that are real
// release stamps stay script/other installs.
func TestDetectInstallBareDev(t *testing.T) {
	for _, c := range []struct {
		exe, ver string
		want     InstallMethod
	}{
		{"/usr/local/bin/lore", "dev", InstallSource},
		{"/home/a/.local/bin/lore", "dev", InstallSource},
		{`C:\Users\a\AppData\Local\lore\lore.exe`, "dev", InstallSource},
		{"/home/a/.asdf/installs/lore/0.1.18/bin/lore", "dev", InstallSource},
		{"/opt/homebrew/Cellar/lore/0.1.18/bin/lore", "dev", InstallHomebrew},
		{"/usr/local/bin/lore", "0.1.18", InstallScript},
		{"/usr/local/bin/lore", "v0.1.18", InstallScript},
		{"/usr/local/bin/lore", "0.1.18-dev", InstallSource},
	} {
		if got := DetectInstall(c.exe, c.ver); got != c.want {
			t.Errorf("DetectInstall(%q, %q) = %q, want %q", c.exe, c.ver, got, c.want)
		}
	}
}
