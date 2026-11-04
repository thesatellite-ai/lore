package formatgate

import (
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
// unparseable binary is below any parseable floor (fail closed).
func VersionBelow(binary, floor string) bool {
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
	case VersionBelow(a, b):
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
