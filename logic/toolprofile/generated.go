package toolprofile

import (
	"regexp"
	"strings"
)

// generatedBannerLines is how many lines from the top GeneratedBanner reads:
// generators put their banner first (a title or frontmatter may come before).
const generatedBannerLines = 15

// generatedBannerRe matches the usual "this file is generated" banners
// (Go's "Code generated … DO NOT EDIT.", @generated, "GENERATED FILE",
// "auto-generated", "do not edit directly").
var generatedBannerRe = regexp.MustCompile(`(?i)@generated\b|\bgenerated file\b|\bauto-?generated\b|\bcode generated\b|\bdo not edit\b|\bdon'?t edit\b`)

// GeneratedBanner is the banner line near the top of content saying another
// tool generates the file ("" when none). Lore must not stitch its pointer
// into such a file (D-2): the generator drops it on its next run, and the
// two tools then fight over the file in every diff. Lines inside lore's own
// pointer block and lines naming lore (lore's own generated files) never count.
func GeneratedBanner(content string) string {
	in := false
	for i, line := range strings.SplitN(content, "\n", generatedBannerLines+1) {
		if i == generatedBannerLines {
			break
		}
		switch t := strings.TrimSpace(line); {
		case t == PointerStartMarker:
			in = true
		case t == PointerEndMarker:
			in = false
		case !in && generatedBannerRe.MatchString(t) && !strings.Contains(strings.ToLower(t), "lore"):
			return t
		}
	}
	return ""
}
