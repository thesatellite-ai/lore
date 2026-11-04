// Package scan is the secret scanner port and its pure helpers (layer L1,
// imports model only). The engine runs a Scanner on every text field of a
// write; adapters (gitleaks, phase 1) implement it. This package also holds
// a small stdlib-regexp Scanner used for store-defined rules and tests.
//
// Invariant: no Finding ever carries the secret. Preview shows at most
// min(PreviewMax, len/PreviewDivisor) leading characters followed by "…"
// (D5 / BATCH 14), never the length of the match.
package scan

import (
	"hash/fnv"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// Source says where a rule comes from.
type Source string

// Rule sources.
const (
	SourceBuiltin  Source = "builtin"  // compiled into lore
	SourceGitleaks Source = "gitleaks" // gitleaks' default rule set
	SourceStore    Source = "store"    // a secret_rules row
)

// Sources lists every source.
func Sources() []Source { return []Source{SourceBuiltin, SourceGitleaks, SourceStore} }

// Rule is one detection rule (the shape of a secret_rules row).
type Rule struct {
	ID          string // "aws-access-token", "url_with_password"
	Description string
	Regex       string
	// Keywords are cheap prefilters: the rule runs only when the text
	// contains one of them (case-insensitive). Empty = always run.
	Keywords []string
	// Entropy is the minimum Shannon entropy (bits per char) of a match;
	// 0 disables the check.
	Entropy float64
	Source  Source
}

// Finding is one detected secret, safe to print.
type Finding struct {
	RuleID string
	Field  string
	// Line is 1-based; Offset is the byte offset in the field text.
	Line   int
	Offset int
	Length int
	// Preview is the masked match (see package doc).
	Preview string
	// MatchHash is HashMatch of the matched text, the key an Allow entry
	// stores instead of the secret.
	MatchHash uint32
}

// Scanner finds secrets in one field's text.
type Scanner interface {
	Scan(field, text string) []Finding
}

// Allow is one "not a secret" entry (a secret_allows row).
type Allow struct {
	RowID     ids.AnyID
	Field     string
	RuleID    string
	MatchHash uint32
}

// Preview limits (D5 / BATCH 14).
const (
	PreviewMax     = 8
	PreviewDivisor = 4
	previewMark    = "…"
)

// Mask returns the safe preview of a match.
func Mask(match string) string {
	r := []rune(match)
	n := min(PreviewMax, len(r)/PreviewDivisor)
	return string(r[:n]) + previewMark
}

// HashMatch is the 32-bit FNV-1a hash of a match (secret_allows.match_hash).
// 32 bits identify one known value among a row's findings without making
// the secret recoverable by lookup.
func HashMatch(match string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(match)) // hash.Hash.Write never returns an error
	return h.Sum32()
}

// Filter drops the findings of row rowID that an allow entry covers (same
// row, field, rule and match hash; every part must match: fail closed).
func Filter(rowID ids.AnyID, findings []Finding, allows []Allow) []Finding {
	var out []Finding
	for _, f := range findings {
		allowed := false
		for _, a := range allows {
			if a.RowID == rowID && a.Field == f.Field && a.RuleID == f.RuleID && a.MatchHash == f.MatchHash {
				allowed = true
				break
			}
		}
		if !allowed {
			out = append(out, f)
		}
	}
	return out
}

// RegexScanner is a Scanner over rules compiled with the stdlib regexp
// engine (RE2: linear time, so a hostile rule cannot hang a write).
type RegexScanner struct {
	rules []compiled
}

type compiled struct {
	rule     Rule
	re       *regexp.Regexp
	keywords []string
}

// NewRegexScanner compiles rules; a rule with an empty id, a bad regex or an
// unknown source is an error naming the rule.
func NewRegexScanner(rules []Rule) (*RegexScanner, error) {
	s := &RegexScanner{}
	for _, r := range rules {
		if r.ID == "" || r.Regex == "" {
			return nil, errs.New(errs.ConfigInvalid, "scan: a rule needs an id and a regex")
		}
		valid := false
		for _, src := range Sources() {
			valid = valid || src == r.Source
		}
		if !valid {
			return nil, errs.New(errs.ConfigInvalid, "scan: rule "+r.ID+" has an unknown source")
		}
		re, err := regexp.Compile(r.Regex)
		if err != nil {
			return nil, errs.Wrap(errs.ConfigInvalid, "scan: rule "+r.ID+" regex", err)
		}
		kw := make([]string, len(r.Keywords))
		for i, k := range r.Keywords {
			kw[i] = strings.ToLower(k)
		}
		s.rules = append(s.rules, compiled{rule: r, re: re, keywords: kw})
	}
	return s, nil
}

// Scan implements Scanner.
func (s *RegexScanner) Scan(field, text string) []Finding {
	var out []Finding
	lower := strings.ToLower(text)
	for _, c := range s.rules {
		if !hasKeyword(lower, c.keywords) {
			continue
		}
		for _, loc := range c.re.FindAllStringIndex(text, -1) {
			m := text[loc[0]:loc[1]]
			if m == "" || (c.rule.Entropy > 0 && Entropy(m) < c.rule.Entropy) {
				continue
			}
			out = append(out, Finding{
				RuleID: c.rule.ID, Field: field,
				Line: strings.Count(text[:loc[0]], "\n") + 1, Offset: loc[0], Length: loc[1] - loc[0],
				Preview: Mask(m), MatchHash: HashMatch(m),
			})
		}
	}
	return out
}

// hasKeyword reports whether lower contains a keyword (none = yes).
func hasKeyword(lower string, kw []string) bool {
	if len(kw) == 0 {
		return true
	}
	for _, k := range kw {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}

// Entropy is the Shannon entropy of s in bits per character.
func Entropy(s string) float64 {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return 0
	}
	counts := map[rune]int{}
	for _, r := range s {
		counts[r]++
	}
	var e float64
	for _, c := range counts {
		p := float64(c) / float64(n)
		e -= p * math.Log2(p)
	}
	return e
}
