package formatgate

import (
	"regexp"
	"strings"
)

// CIPin is one lore version a CI workflow file pins (FORMAT_EVOLUTION
// section 4 "CI on an old pinned lore version": doctor warns when a
// workflow under .github/workflows/ pins a version below min_lore_version).
type CIPin struct {
	// Line is the 1-based line of the pin in the file.
	Line int
	// Version is the pinned version as written ("v0.1.17" or "0.1.17").
	Version string
}

// ciPinPatterns are the ways a workflow pins lore: `lore upgrade
// --version X` (8.4's fix), the LORE_VERSION_PIN env (8.4 CI pin), and a
// release download URL of the lore repo. Each has one capture group: the
// version. Matching is per line and textual: a workflow is YAML, but the
// pin may sit inside a `run:` shell block, which a YAML parser would hand
// back as an opaque string anyway.
var ciPinPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\blore\s+upgrade\b.*--version[=\s]+["']?(v?\d+\.\d+\.\d+[^\s"']*)`),
	regexp.MustCompile(`\bLORE_VERSION_PIN["']?\s*[:=]\s*["']?(v?\d+\.\d+\.\d+[^\s"']*)`),
	regexp.MustCompile(`thesatellite-ai/lore/releases/download/(v?\d+\.\d+\.\d+[^/\s"']*)/`),
}

// CIPins lists every lore pin in one workflow file's text, in line order.
// A comment line (first non-space byte '#') is skipped.
func CIPins(text string) []CIPin {
	var out []CIPin
	for i, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, re := range ciPinPatterns {
			if m := re.FindStringSubmatch(line); m != nil {
				out = append(out, CIPin{Line: i + 1, Version: m[1]})
				break // one pin per line
			}
		}
	}
	return out
}

// CIPinsBelow keeps the pins below floor (VersionBelow: a dev-build pin is
// never below, FMT-DEV; an empty or unparseable floor keeps none).
func CIPinsBelow(pins []CIPin, floor string) []CIPin {
	var out []CIPin
	for _, p := range pins {
		if VersionBelow(p.Version, floor) {
			out = append(out, p)
		}
	}
	return out
}
