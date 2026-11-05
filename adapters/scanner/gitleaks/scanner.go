// Package gitleaks is the secret scanner adapter (layer adapters): it
// implements scan.Scanner with gitleaks v8 as a library for gitleaks'
// default rules and lore's stdlib-regexp engine (scan.RegexScanner) for
// lore's own pack and every secret_rules row (SQ7, SQ8, D2/D9).
//
// Invariant: no Finding, error or Report produced here carries a matched
// value; previews go through scan.Mask (D5 / BATCH 14). Gitleaks logs only
// at debug/trace level, below its default info level, and this package
// never changes that global.
package gitleaks

import (
	"regexp"
	"strings"
	"sync"

	"github.com/zricethezav/gitleaks/v8/config"
	"github.com/zricethezav/gitleaks/v8/detect"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
)

// GitleaksMaxBytes is the size gate (SQ7). Gitleaks runs ~200 rules, many
// with keyword-free or wide regexes, so its cost grows with text size much
// faster than lore's keyword-gated pack; a field above this size (256 KiB:
// far above any note, below the 4 MiB row cap) is scanned by lore's engine
// only, and the Report says so. The text is still scanned: never skipped.
const GitleaksMaxBytes = 256 << 10

// NoteOversize is the Report.Note set when the gate fell back.
const NoteOversize = "text above the gitleaks size gate: scanned with the lore rule pack and store rules only"

// Report is one Scan with the gate outcome (the port's scan.Report).
// Degraded is true when gitleaks' rules did not run on this text (see
// Note); callers must surface it.
type Report = scan.Report

// Compile-time proof that the adapter offers the optional upgrade the
// engine looks for.
var _ scan.Reporter = (*Scanner)(nil)

// Scanner implements scan.Scanner. Build it with New from a rule set made
// by Rules; it is immutable afterwards and safe for concurrent use.
type Scanner struct {
	regex *scan.RegexScanner // lore pack + store rules
	// mu serialises the gitleaks detector: its Detect is not documented as
	// safe for concurrent use (it updates counters on the detector).
	mu       sync.Mutex
	detector *detect.Detector // nil when no gitleaks rule is on
	// builtin: rule ids still in their built-in form (post-filters run
	// only on these); allow: the repo .gitleaks.toml allow (D8).
	builtin map[string]bool
	allow   []compiledAllow
	noProse bool // Options.NoProseFilter
}

// Detector settings (3.1). D7: a "gitleaks:allow" comment pasted into
// lore text must not switch the check off. 3.6: decoded segments
// (base64, hex, percent, unicode) are re-scanned up to depth 3.
const (
	maxDecodeDepth      = 3
	ignoreGitleaksAllow = true
)

// Options are the optional scanner inputs.
type Options struct {
	// RepoAllow is the repo .gitleaks.toml allow (D8), read by the caller
	// only while secrets.use_repo_gitleaks is on.
	RepoAllow RepoAllow
	// NoProseFilter turns the password_pair prose post-filter off (policy
	// secrets.password_pair_prose_filter: off, SECRETS 3.4): every match
	// is kept, the stricter choice.
	NoProseFilter bool
}

var _ scan.Scanner = (*Scanner)(nil)

// New builds a scanner for rules. A gitleaks-sourced rule must name a
// gitleaks default (a changed one arrives as a store rule from Rules).
func New(rules []scan.Rule) (*Scanner, error) { return NewWith(rules, Options{}) }

// NewWith is New with Options (the one constructor every scan path uses,
// D4).
func NewWith(rules []scan.Rule, opts Options) (*Scanner, error) {
	builtin := map[string]bool{}
	for _, r := range rules {
		if r.Source == scan.SourceBuiltin {
			builtin[r.ID] = true
		}
	}
	var own []scan.Rule
	glRules := map[string]config.Rule{}
	var order []string
	for _, r := range rules {
		if r.Source != scan.SourceGitleaks {
			own = append(own, r)
			continue
		}
		cfg, err := gitleaksDefaults()
		if err != nil {
			return nil, err
		}
		gr, ok := cfg.Rules[r.ID]
		if !ok {
			return nil, errs.New(errs.ConfigInvalid, "scan: rule "+r.ID+" is not a gitleaks default")
		}
		glRules[r.ID] = gr
		order = append(order, r.ID)
	}
	// Compile first so a bad regex is refused by rule id only: regexp's
	// own error quotes the expression, which may hold a literal value.
	for _, r := range own {
		if _, err := regexp.Compile(r.Regex); err != nil {
			return nil, errs.New(errs.ConfigInvalid, "scan: rule "+r.ID+" has an invalid regex")
		}
	}
	rs, err := scan.NewRegexScanner(own)
	if err != nil {
		return nil, err
	}
	s := &Scanner{regex: rs, builtin: builtin, allow: compileAllow(opts.RepoAllow), noProse: opts.NoProseFilter}
	if len(glRules) > 0 {
		base, err := gitleaksDefaults()
		if err != nil {
			return nil, err
		}
		kw := map[string]struct{}{}
		for _, r := range glRules {
			for _, k := range r.Keywords {
				kw[strings.ToLower(k)] = struct{}{}
			}
		}
		s.detector = detect.NewDetector(config.Config{
			Rules: glRules, Keywords: kw, OrderedRules: order, Allowlists: base.Allowlists,
		})
		s.detector.MaxDecodeDepth = maxDecodeDepth
		s.detector.IgnoreGitleaksAllow = ignoreGitleaksAllow
	}
	return s, nil
}

// Scan implements scan.Scanner. It drops the gate outcome; the engine
// should call ScanReport so a fallback is never silent.
func (s *Scanner) Scan(field, text string) []scan.Finding {
	return s.ScanReport(field, text).Findings
}

// ScanReport scans text and reports whether the size gate fell back.
func (s *Scanner) ScanReport(field, text string) Report {
	rep := Report{Findings: s.keep(text, s.regex.Scan(field, text))}
	if s.detector == nil {
		return rep
	}
	if len(text) > GitleaksMaxBytes {
		rep.Degraded, rep.Note = true, NoteOversize
		return rep
	}
	var gl []scan.Finding
	s.mu.Lock()
	found := s.detector.DetectString(text)
	s.mu.Unlock()
	for _, f := range found {
		gl = append(gl, convert(field, text, f.RuleID, f.StartLine, f.Match, f.Secret))
	}
	rep.Findings = append(rep.Findings, s.keep(text, gl)...)
	return rep
}

// convert maps a gitleaks finding to a safe scan.Finding. Gitleaks gives a
// 0-based line and the match text; the byte offset is found by searching
// the match from that line's start. Secret (the rule's secret group) is
// what is masked and hashed, so an allow entry keys on the secret itself.
func convert(field, text, ruleID string, line0 int, match, secret string) scan.Finding {
	if secret == "" {
		secret = match
	}
	from := lineStart(text, line0)
	off := -1
	if i := strings.Index(text[from:], match); i >= 0 {
		off = from + i
		if j := strings.Index(match, secret); j >= 0 {
			off += j
		}
	} else if i := strings.Index(text, secret); i >= 0 {
		off = i
	}
	line := line0 + 1
	if off >= 0 {
		line = strings.Count(text[:off], "\n") + 1
	}
	return scan.Finding{
		RuleID: ruleID, Field: field, Line: line, Offset: off, Length: len(secret),
		Preview: scan.Mask(secret), MatchHash: scan.HashMatch(secret),
	}
}

// lineStart is the byte offset of 0-based line n (0 when out of range).
func lineStart(text string, n int) int {
	off := 0
	for range n {
		i := strings.IndexByte(text[off:], '\n')
		if i < 0 {
			return 0
		}
		off += i + 1
	}
	return off
}
