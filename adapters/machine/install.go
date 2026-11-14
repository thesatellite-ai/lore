package machine

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/formatgate"
)

// InstallMethod is the closed set of ways lore was installed, the
// profile's install_method field (AGENT_IDENTITY_DESIGN 6.8, detection of
// FORMAT_EVOLUTION_DESIGN 8.1).
type InstallMethod string

// The install methods.
const (
	InstallHomebrew InstallMethod = "homebrew"
	InstallScript   InstallMethod = "script"
	InstallSource   InstallMethod = "source"
	// InstallOther is a version manager's folder (asdf, mise, nix, …).
	InstallOther InstallMethod = "other"
)

// brewMarkers are the path pieces of a Homebrew-installed lore, every
// prefix: a formula's <prefix>/Cellar/lore/<version>/bin/lore (releases up
// to 0.1.18) and a cask's <prefix>/Caskroom/lore/<version>/lore (the
// release tool moved the tap entry to a cask, GR-1). Without the cask
// path a brew install looked like a script install, and `lore upgrade`
// overwrote the file brew owns instead of running brew.
var brewMarkers = []string{"/Cellar/lore/", "/Caskroom/lore/"}

// versionManagerMarkers are folders of tools that own the binary themselves
// (the same list `lore upgrade` uses, 8.1).
var versionManagerMarkers = []string{"/.asdf/", "/mise/installs/", "/.local/share/aquaproj", "/nix/store/", "/.proto/tools/"}

// gitDescribe is a `git describe` version (task lore:build of a checkout:
// v0.1.17-450-g74e4fe6, maybe -dirty): a source build, never a release.
var gitDescribe = regexp.MustCompile(`-[0-9]+-g[0-9a-f]{4,}(-dirty)?$|-dirty$`)

// DetectInstall decides the install method from the resolved binary path
// and the stamped version (8.1): a Cellar or Caskroom path is Homebrew, a dev or
// snapshot version (or none) a source build, a version manager's folder
// other, anything else the install script or a downloaded archive.
func DetectInstall(exe, version string) InstallMethod {
	p := filepath.ToSlash(exe)
	if slices.ContainsFunc(brewMarkers, func(m string) bool { return strings.Contains(p, m) }) {
		return InstallHomebrew
	}
	switch {
	// formatgate.DevBuild (FMT-DEV) also covers the bare "dev" stamp of an
	// unstamped `go build ./cmd/lore`: seen as a script install, `lore
	// upgrade` swapped such a build for the newest release without asking,
	// even in a project the release cannot write.
	case version == "" || formatgate.DevBuild(version) || gitDescribe.MatchString(version):
		return InstallSource
	}
	for _, m := range versionManagerMarkers {
		if strings.Contains(p, m) {
			return InstallOther
		}
	}
	return InstallScript
}

// ThisInstall is DetectInstall of the running binary (os.Executable with
// symlinks resolved); "" when the path cannot be found (never guessed).
func ThisInstall(version string) InstallMethod {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = r
	}
	return DetectInstall(exe, version)
}
