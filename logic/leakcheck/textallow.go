package leakcheck

// textallow.go — the allow for text-rule findings (#129): public text (an
// approved product wording, a release note, an error message) that an
// internal note quotes verbatim. Narrow by design, never a blanket bypass:
// an allow names one internal note and the hash of one exact passage
// (PassageHash of the matched run of words), optionally one repo file. It
// stops applying when the note's wording or the shipped passage changes,
// because the matched run, and so its hash, changes with them.

import (
	"fmt"
	"strings"

	"github.com/thesatellite-ai/lore/logic/scan"
)

// TextAllowRule is the rule_id a secret_allows row carries when it is a
// text allow: the row lives in the internal note's own store (never in the
// client repo), row_id names the note, match_hash the passage, path the
// file when narrowed to one. No secret rule has this id, so the secret
// scanner never matches it.
const TextAllowRule = "internal-note"

// TextAllow is one text allow as the check reads it.
type TextAllow struct {
	// ID is the allow row's id (sal_…), named when it covers a finding.
	ID string
	// RowID is the internal note whose text may appear.
	RowID string
	// Hash is the PassageHash of the allowed passage.
	Hash uint32
	// Path narrows the allow to one repo-relative slash path ("" = any).
	Path string
	// Reason and By are shown with every finding it covers (auditable).
	Reason string
	By     string
}

// PassageHash is the hash a text allow keys a passage by: the normalised
// words (Words) joined by one space, hashed as secret matches are
// (scan.HashMatch), so reflowing, case and punctuation do not change it.
func PassageHash(words []string) uint32 {
	return scan.HashMatch(strings.Join(words, " "))
}

// PassageHashText is PassageHash of a passage given as text (`lore leaks
// allow --phrase`).
func PassageHashText(s string) uint32 { return PassageHash(Words(s)) }

// matchTextAllow is the allow covering a run of note row with hash h in
// the item at path, if any.
func matchTextAllow(allows []TextAllow, row string, h uint32, path string) (TextAllow, bool) {
	for _, a := range allows {
		if a.RowID == row && a.Hash == h && (a.Path == "" || a.Path == path) {
			return a, true
		}
	}
	return TextAllow{}, false
}

// AllowedDetail is the line of a text finding a text allow covers: the
// note, the run, and which allow let it through, with its reason and
// author, so the passage that ships is always accounted for.
func AllowedDetail(rowID string, words int, a TextAllow) string {
	return fmt.Sprintf("matches internal note %s (%d words in a row), allowed by %s", rowID, words, a.ID) + allowedWhy(a)
}

// allowedWhy is " (<reason>; by <author>)" of an allow, "" when it has
// neither.
func allowedWhy(a TextAllow) string {
	var why []string
	if a.Reason != "" {
		why = append(why, a.Reason)
	}
	if a.By != "" {
		why = append(why, "by "+a.By)
	}
	if len(why) == 0 {
		return ""
	}
	return " (" + strings.Join(why, "; ") + ")"
}
