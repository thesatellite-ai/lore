package setupweb

import (
	"runtime"
	"runtime/debug"
)

// commitLen is how many hex digits of the VCS revision the page shows
// (enough to find the commit, short enough for the About popover).
const commitLen = 12

// vcsRevision is the build setting the Go toolchain stamps with the
// commit when the binary is built inside a git checkout.
const vcsRevision = "vcs.revision"

// buildJSON is the binary's build facts the page shows in its About
// popover: the commit ("" when the toolchain stamped none, e.g. a build
// outside git or with -buildvcs=false), the Go version and the platform.
type buildJSON struct {
	Commit string `json:"commit"`
	Go     string `json:"go"`
	OS     string `json:"os"`
	Arch   string `json:"arch"`
}

// buildFacts reads this binary's build facts; it never fails (a missing
// fact is an empty string).
func buildFacts() buildJSON {
	b := buildJSON{Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return b
	}
	for _, s := range info.Settings {
		if s.Key == vcsRevision {
			b.Commit = s.Value
			if len(b.Commit) > commitLen {
				b.Commit = b.Commit[:commitLen]
			}
		}
	}
	return b
}
