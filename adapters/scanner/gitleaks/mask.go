package gitleaks

import (
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/logic/scan"
)

// MaskedOpen and MaskedClose wrap the safe preview MaskText puts where a
// secret was: "[redacted github_token sha256:…]". The rule names what kind of
// value it was; the hash (scan.Mask) lets a person match it to their own
// copy without any character of the value being printed.
const (
	MaskedOpen  = "[redacted "
	MaskedClose = "]"
)

// textScanner is lore's default secret scanner (lore's pack plus the
// gitleaks default rules: the rules a write is checked with), built once
// on the first MaskText call; nil when it cannot be built.
var textScanner = sync.OnceValue(func() *Scanner {
	rules, err := Defaults()
	if err != nil {
		return nil
	}
	sc, err := New(rules)
	if err != nil {
		return nil
	}
	return sc
})

// tokenShape is the fallback when the scanner cannot be built (fail
// closed): the well-known token prefixes (GitHub, GitLab, Slack, AWS, npm,
// sk-). Pieces are joined so lore's own source scan does not flag the line.
var tokenShape = regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{16,}|github_` + `pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{16,}|xox[bpas]-[A-Za-z0-9-]{10,}|` + `AK` + `IA[A-Z0-9]{16}|npm_[A-Za-z0-9]{20,}|sk-[A-Za-z0-9_-]{20,})`)

// MaskText returns text with every value the secret rules flag replaced
// by its safe preview (MaskedOpen + rule + " " + scan.Mask + MaskedClose).
//
// Why (devco R-11): errors repeat what a person typed ("<x> is not a row
// id", "no store named <x>", "config key <x>"), so a token pasted into the
// wrong argument landed in terminal scrollback, CI logs and agent
// transcripts. Every surface error and `lore sql` cell goes through this,
// with the same rules a write is checked with, so no message names a value
// lore would have refused to store.
func MaskText(text string) string {
	found := FindSecrets("text", text)
	if len(found) == 0 {
		return text
	}
	return replaceFindings(text, found)
}

// replaceFindings puts each finding's preview over its span, last span
// first so earlier offsets stay valid; overlapping spans are merged into
// the first. A finding without a known offset masks the whole text: the
// value cannot be located, so none of the text is safe to show.
func replaceFindings(text string, found []scan.Finding) string {
	spans := make([]scan.Finding, 0, len(found))
	for _, f := range found {
		if f.Offset < 0 || f.Offset+f.Length > len(text) {
			return MaskedOpen + f.RuleID + " " + f.Preview + MaskedClose
		}
		spans = append(spans, f)
	}
	slices.SortFunc(spans, func(a, b scan.Finding) int { return a.Offset - b.Offset })
	var b strings.Builder
	at := 0
	for _, f := range spans {
		end := f.Offset + f.Length
		if end <= at {
			continue // inside a span already masked
		}
		start := max(f.Offset, at)
		b.WriteString(text[at:start])
		if start == f.Offset {
			b.WriteString(MaskedOpen + f.RuleID + " " + f.Preview + MaskedClose)
		}
		at = end
	}
	b.WriteString(text[at:])
	return b.String()
}

// HasSecret reports whether the secret rules flag anything in text (fail
// closed: without a scanner, whether a well-known token shape is in it).
// A caller with many small texts (`lore sql` cells) checks them joined
// once and masks each only when this is true.
func HasSecret(text string) bool { return len(FindSecrets("text", text)) > 0 }

// FindSecrets is what the default secret rules flag in one field's text
// (hash-only findings, scan.Finding); without a scanner, the well-known
// token shapes (rule "token"). For a value checked outside a row write,
// such as a setup answer saved to config.yaml (devco R-17).
func FindSecrets(field, text string) []scan.Finding {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if sc := textScanner(); sc != nil {
		return sc.Scan(field, text)
	}
	var out []scan.Finding
	for _, loc := range tokenShape.FindAllStringIndex(text, -1) {
		m := text[loc[0]:loc[1]]
		out = append(out, scan.Finding{RuleID: "token", Field: field, Line: strings.Count(text[:loc[0]], "\n") + 1,
			Offset: loc[0], Length: len(m), Preview: scan.Mask(m), MatchHash: scan.HashMatch(m)})
	}
	return out
}
