package formatgate

import (
	"regexp"
	"strconv"
	"strings"
)

// versionParts is how many numeric parts a version comparison uses.
const versionParts = 3

// effective is the version this binary counts as: its own, raised to the
// highest Since it knows. A binary that understands a feature is at least
// the release that introduced it, so a development build of 0.1.18 that
// reports "0.1.17-34-gabc" (git describe) or "0.1.0-dev" is never locked
// out of the format it writes itself. DECISION FMT-EFF (ov1-format).
func effective(binary string) string {
	top := ""
	for _, f := range known {
		top = MaxVersion(top, f.Since)
	}
	if _, ok := versionCore(binary); !ok {
		return top
	}
	return MaxVersion(binary, top)
}

// VersionBelow reports whether binary is older than floor, comparing the
// numeric major.minor.patch core only ("0.1.19-dev" and "v0.1.19" count as
// 0.1.19). An unparseable floor is "not below" (a hand-edited floor alone
// must not lock everyone out; unknown features still block in Check); an
// unparseable binary is below any parseable floor (fail closed). A
// development build ("0.1.0-dev", a goreleaser snapshot) is never below:
// its numeric core is a placeholder, not a release, so it must not trip
// team-version warnings, pin checks or upgrade notices. DECISION FMT-DEV
// (ov44). The format gate itself (Check) compares through effective and
// versionBelow, so a dev build is still locked out of a floor above every
// feature it knows.
func VersionBelow(binary, floor string) bool {
	if DevBuild(binary) {
		return false
	}
	return versionBelow(binary, floor)
}

// versionBelow is the plain numeric compare behind VersionBelow.
func versionBelow(binary, floor string) bool {
	f, ok := versionCore(floor)
	if !ok {
		return false
	}
	b, ok := versionCore(binary)
	if !ok {
		return true
	}
	for i := range versionParts {
		if b[i] != f[i] {
			return b[i] < f[i]
		}
	}
	return false
}

// DevBuild reports whether v is an unreleased development build: the
// plain "dev" a `go build` without ldflags stamps (cmd/lore's default
// version, so its saves carry `_w {v: "dev"}`), the "-dev" suffix, or a
// goreleaser snapshot. FMT-DEV: such a stamp is never below a floor, so a
// teammate on a source build is not listed as turning read-only.
//
// A source build stamped by `git describe --tags --always --dirty` from a
// tree whose nearest tag is no release (an archive/… tag: "archive/x-2-
// gabc1234-dirty") or that has no tag at all (a bare "abc1234") has no
// X.Y.Z core either: it is a source build too, not garbage, so its own saves
// never read as "below min_lore_version, read-only here" in doctor.
func DevBuild(v string) bool {
	if v == devVersion || strings.HasSuffix(v, "-dev") || strings.Contains(v, "snapshot") {
		return true
	}
	// Any `git describe` stamp past a tag is a source build, whatever the
	// tag: a 0.1.18 source tree whose nearest tag is v0.1.17 stamps
	// "v0.1.17-100-gabc1234" (effective already lifts it past every
	// feature it knows), so its own saves must not read as "below
	// min_lore_version, read-only here" either. toolprofile.IsDevBuild and
	// machine.DetectInstall read such a stamp as a source build too.
	return gitDescribeRe.MatchString(v) || bareCommitRe.MatchString(v)
}

// gitDescribeRe is a `git describe` suffix past any tag (with --dirty);
// bareCommitRe the abbreviated commit `--always` prints with no tag.
var (
	gitDescribeRe = regexp.MustCompile(`-\d+-g[0-9a-f]{7,40}(-dirty)?$`)
	bareCommitRe  = regexp.MustCompile(`^[0-9a-f]{7,40}(-dirty)?$`)
)

// devVersion is the version an unstamped build reports (cmd/lore main.go).
const devVersion = "dev"

// MaxVersion is the higher of two versions; an unparseable one loses to a
// parseable one, and two unparseable ones keep a.
func MaxVersion(a, b string) string {
	_, aok := versionCore(a)
	_, bok := versionCore(b)
	switch {
	case !bok:
		return a
	case !aok:
		return b
	case versionBelow(a, b):
		return b
	}
	return a
}

// versionCore parses the numeric core of v ("v0.1.18-dev" → 0,1,18).
func versionCore(v string) ([versionParts]int, bool) {
	var out [versionParts]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if v == "" || len(parts) > versionParts {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
