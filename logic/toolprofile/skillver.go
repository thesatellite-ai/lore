package toolprofile

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The skill version line (ONBOARDING_DESIGN 4.7.1 "The version line"): one
// `lore_version: <v>` line in the frontmatter of the installed SKILL.md.
const (
	// SkillVersionKey is the frontmatter key of the version line.
	SkillVersionKey = "lore_version"
	// SkillVersionDev is the value the source skills/SKILL.md carries; an
	// installed copy carrying it is linked from a source checkout.
	SkillVersionDev = "dev"
)

// frontmatterFence opens and closes a SKILL.md frontmatter block.
const frontmatterFence = "---"

// SkillStatus is the outcome of one skill check, the doctor/setup check
// levels (ONB 4.7: tools.skill_version is warn-level).
type SkillStatus string

// Skill check outcomes.
const (
	SkillOK   SkillStatus = "ok"
	SkillWarn SkillStatus = "warn"
	SkillSkip SkillStatus = "skip"
)

// SkillStatuses lists every skill check outcome.
func SkillStatuses() []SkillStatus { return []SkillStatus{SkillOK, SkillWarn, SkillSkip} }

// EnumValues lists every outcome (op.Enumerated): the OpenAPI document's
// enum for a field of this type. internal/layers pins it to the constants.
func (SkillStatus) EnumValues() []string {
	out := make([]string, 0, len(SkillStatuses()))
	for _, s := range SkillStatuses() {
		out = append(out, string(s))
	}
	return out
}

// SkillVerdict is a status plus its detail text (empty for ok).
type SkillVerdict struct {
	Status SkillStatus `json:"status"`
	Detail string      `json:"detail,omitempty"`
}

// Detail texts of the 4.7.1 check table, word for word.
const (
	detailOlderFmt = "skill from lore %s, binary %s"
	detailNewer    = "skill is newer than the binary; upgrade lore (lore upgrade) or reinstall the skill"
	// detailNoLineSameFmt: no version line, yet the files are this binary's
	// skill (a folder linked to lore's source skills/, which carries no
	// line because install stamps it in).
	detailNoLineSameFmt = "current (same as lore %s's skill; no version line)"
	// detailNoLineDiffersFmt: no version line and the files differ: an
	// older skill or a hand edit, and nothing says which.
	detailNoLineDiffersFmt = "no version line, and it differs from lore %s's skill (older, or edited by hand)"
	detailDevSkill         = "skill is linked from a source checkout"
	detailDevBinary        = "binary is a development build"
	detailBadVersion       = "skill version %q is not a lore release version"
)

// frontmatterLines splits b into lines and returns the index range
// (start, end) of the frontmatter body: lines start..end-1 lie between the
// opening and closing fences. ok is false when b has no frontmatter.
func frontmatterLines(b []byte) (lines []string, start, end int, ok bool) {
	lines = strings.SplitAfter(string(b), "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r\n") != frontmatterFence {
		return lines, 0, 0, false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r\n") == frontmatterFence {
			return lines, 1, i, true
		}
	}
	return lines, 0, 0, false
}

// versionLine reports whether line is the top-level version line and
// returns its value with quotes removed.
func versionLine(line string) (string, bool) {
	k, v, found := strings.Cut(strings.TrimRight(line, "\r\n"), ":")
	if !found || k != SkillVersionKey { // top level only: no indent
		return "", false
	}
	return strings.Trim(strings.TrimSpace(v), `"'`), true
}

// ReadSkillVersion returns the lore_version of a SKILL.md; ok is false when
// the frontmatter has no such line (a skill installed by lore 0.1.17 or by
// hand) or there is no frontmatter.
func ReadSkillVersion(skillMD []byte) (string, bool) {
	lines, start, end, ok := frontmatterLines(skillMD)
	if !ok {
		return "", false
	}
	for _, l := range lines[start:end] {
		if v, isVer := versionLine(l); isVer {
			return v, true
		}
	}
	return "", false
}

// StampSkillVersion returns skillMD with its version line set to v: the
// existing line is replaced, or a new one is added as the last frontmatter
// line. Nothing else changes, so StripSkillVersion of the result equals
// StripSkillVersion of the input (ONB 4.7.1: a byte compare minus that one
// line tells a hand edit from an old install). A file without frontmatter
// is returned unchanged.
func StampSkillVersion(skillMD []byte, v string) []byte {
	lines, start, end, ok := frontmatterLines(skillMD)
	if !ok {
		return append([]byte(nil), skillMD...)
	}
	stamp := SkillVersionKey + ": " + v + "\n"
	var b strings.Builder
	done := false
	for i, l := range lines {
		if i >= start && i < end {
			if _, isVer := versionLine(l); isVer {
				if !done {
					b.WriteString(stamp)
					done = true
				}
				continue
			}
		}
		if i == end && !done {
			b.WriteString(stamp)
			done = true
		}
		b.WriteString(l)
	}
	return []byte(b.String())
}

// StripSkillVersion returns skillMD without its frontmatter version line.
func StripSkillVersion(skillMD []byte) []byte {
	lines, start, end, ok := frontmatterLines(skillMD)
	if !ok {
		return append([]byte(nil), skillMD...)
	}
	var b strings.Builder
	for i, l := range lines {
		if i >= start && i < end {
			if _, isVer := versionLine(l); isVer {
				continue
			}
		}
		b.WriteString(l)
	}
	return []byte(b.String())
}

// SkillModified reports whether an installed SKILL.md differs from the
// embedded one other than in its version line (ONB 4.7.1
// tools.skill_modified). Only meaningful when both carry the same version:
// an older install differs because the bundle changed, not by hand.
func SkillModified(installed, embedded []byte) bool {
	return !bytes.Equal(StripSkillVersion(installed), StripSkillVersion(embedded))
}

// gitDescribeRe matches `git describe` output past a tag
// ("0.1.17-34-gabc1234"); bareHashRe a commit hash alone.
var (
	gitDescribeRe = regexp.MustCompile(`-[0-9]+-g[0-9a-f]{4,40}`)
	bareHashRe    = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

// devMarkers are version substrings of an unreleased build: an unstamped
// "dev", the "-dev" suffix, a goreleaser snapshot, a dirty tree.
var devMarkers = []string{"-dev", "snapshot", "-dirty"}

// IsDevBuild reports whether a binary version is a development build (ONB
// 4.7.1 last row: `git describe` with -dirty or a hash, an unstamped "dev",
// a snapshot), or is not a release version at all. Such a build cannot say
// which skill matches it, so the check is skipped.
func IsDevBuild(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" || v == SkillVersionDev || bareHashRe.MatchString(v) || gitDescribeRe.MatchString(v) {
		return true
	}
	for _, m := range devMarkers {
		if strings.Contains(v, m) {
			return true
		}
	}
	_, ok := releaseCore(v)
	return !ok
}

// releaseParts is major.minor.patch.
const releaseParts = 3

// releaseCore parses a release version "0.1.18" / "v0.1.18" into its three
// numbers; anything else (a suffix, two parts, letters) is not a release.
func releaseCore(v string) ([releaseParts]int, bool) {
	var out [releaseParts]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(parts) != releaseParts {
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

// compareRelease is -1, 0 or 1 as a is below, equal to or above b; both
// must be parsed release cores.
func compareRelease(a, b [releaseParts]int) int {
	for i := range releaseParts {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return 0
}

// NormVersion drops a leading "v" and spaces ("v0.1.18" → "0.1.18"), the
// form the version line carries.
func NormVersion(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }

// CheckSkillVersion is the tools.skill_version check (ONB 4.7.1 table):
// installed is the SKILL.md lore_version (hasLine false when the line is
// missing), binary the running lore's version, and same whether the
// installed files equal this binary's bundle apart from the version line:
// the one compare tools.skill_modified reads too, so the two rows never
// contradict each other. Precedence when two rows could apply: a missing
// line decides first (ok when the files are this binary's skill, else a
// warn for every binary), then a dev skill, then a dev binary skip, then
// the numeric compare. A line that is there decides by itself: same files
// under an older line still warn, and a reinstall restamps it.
func CheckSkillVersion(installed string, hasLine bool, binary string, same bool) SkillVerdict {
	installed = NormVersion(installed)
	binary = NormVersion(binary)
	switch {
	case same && !hasLine:
		return SkillVerdict{Status: SkillOK, Detail: fmt.Sprintf(detailNoLineSameFmt, binary)}
	case !hasLine:
		return SkillVerdict{Status: SkillWarn, Detail: fmt.Sprintf(detailNoLineDiffersFmt, binary)}
	case installed == SkillVersionDev:
		return SkillVerdict{Status: SkillSkip, Detail: detailDevSkill}
	case IsDevBuild(binary):
		return SkillVerdict{Status: SkillSkip, Detail: detailDevBinary}
	}
	iv, ok := releaseCore(installed)
	if !ok {
		return SkillVerdict{Status: SkillWarn, Detail: fmt.Sprintf(detailBadVersion, installed)}
	}
	bv, _ := releaseCore(binary) // IsDevBuild above refused an unparseable binary
	switch compareRelease(iv, bv) {
	case -1:
		return SkillVerdict{Status: SkillWarn, Detail: fmt.Sprintf(detailOlderFmt, installed, binary)}
	case 1:
		return SkillVerdict{Status: SkillWarn, Detail: detailNewer}
	}
	return SkillVerdict{Status: SkillOK}
}

// SkillOlder reports whether an installed SKILL.md is older than binary:
// a missing version line, or a release below it. A dev skill, a dev binary
// or an unreadable version is not "older" (`lore upgrade`'s hint, ONB
// 4.7.1, must not nag a source checkout).
func SkillOlder(skillMD []byte, binary string) bool {
	v, ok := ReadSkillVersion(skillMD)
	if !ok {
		return true
	}
	iv, iok := releaseCore(NormVersion(v))
	bv, bok := releaseCore(NormVersion(binary))
	if !iok || !bok || IsDevBuild(NormVersion(binary)) {
		return false
	}
	return compareRelease(iv, bv) < 0
}

// UpgradeSkillHint is the one line `lore upgrade` prints when any
// installed skill is older than the new binary (ONB 4.7.1).
func UpgradeSkillHint(version string) string {
	return "skills are older than lore " + NormVersion(version) + ": lore skill install --all"
}
