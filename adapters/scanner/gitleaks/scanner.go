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
	"strconv"
	"strings"

	"github.com/zricethezav/gitleaks/v8/config"
	"github.com/zricethezav/gitleaks/v8/detect"
	"github.com/zricethezav/gitleaks/v8/detect/codec"

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
	// detector is shared by concurrent scans without a lock (P3-PERF-1,
	// BUG-TIME-BENCH: `lore lint` scans 100k texts in parallel): gitleaks
	// scans files on many goroutines with one Detector itself, its byte
	// counters are atomic and its findings list and commit map have their
	// own mutexes; TestScanConcurrent pins this under -race.
	detector *detect.Detector // nil when no gitleaks rule is on
	// plain is detector's twin with decoding off (MaxDecodeDepth 0), used
	// for a text mayHoldEncoding rules out: the decode passes would find
	// no segment there, so the findings are the same at about half the
	// cost (SECRETS 3.8, OWNERQ-13). nil exactly when detector is.
	plain *detect.Detector
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
		s.detector = newDetector(config.Config{
			Rules: glRules, Keywords: kw, OrderedRules: order, Allowlists: base.Allowlists,
		}, maxDecodeDepth)
		s.plain = newDetector(config.Config{
			Rules: glRules, Keywords: kw, OrderedRules: order, Allowlists: base.Allowlists,
		}, 0)
	}
	return s, nil
}

// newDetector builds one gitleaks detector over cfg with the 3.1
// settings and the given decode depth.
func newDetector(cfg config.Config, decodeDepth int) *detect.Detector {
	d := detect.NewDetector(cfg)
	d.MaxDecodeDepth = decodeDepth
	d.IgnoreGitleaksAllow = ignoreGitleaksAllow
	return d
}

// Scan implements scan.Scanner. It drops the gate outcome; the engine
// should call ScanReport so a fallback is never silent.
func (s *Scanner) Scan(field, text string) []scan.Finding {
	return s.ScanReport(field, text).Findings
}

// ScanReport scans text and reports whether the size gate fell back.
func (s *Scanner) ScanReport(field, text string) Report {
	rep := Report{Findings: s.keep(text, s.regex.Scan(field, text))}
	rep.Findings = append(rep.Findings, s.decodedOwn(field, text)...)
	if s.detector == nil {
		return rep
	}
	if len(text) > GitleaksMaxBytes {
		rep.Degraded, rep.Note = true, NoteOversize
		return rep
	}
	var gl []scan.Finding
	d := s.detector
	if !mayHoldEncoding(text) {
		d = s.plain
	}
	found := d.DetectString(text)
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

// decodedOwn runs lore's own rules (the regex scanner) on the decoded forms
// of text (3.6: base64, hex, percent, unicode, up to maxDecodeDepth), the
// way gitleaks re-scans its own rules (OV4: before, only gitleaks rules
// saw decoded text). A hit counts only where it overlaps a decoded
// segment (the plain text was scanned already) and is reported at the
// encoded span of the original text, so allows and masking key on what is
// stored.
//
//	text ─▶ codec.Decoder.Decode (depth 1..3) ─▶ regex.Scan(decoded)
//	     ─▶ SegmentsWithDecodedOverlap ─▶ AdjustMatchIndex ─▶ finding on the original
func (s *Scanner) decodedOwn(field, text string) []scan.Finding {
	if len(text) > GitleaksMaxBytes {
		return nil // the same size gate as the detector (a degraded scan is reported there)
	}
	if !mayHoldEncoding(text) {
		return nil // no decodable segment: the passes below would find nothing
	}
	dec := codec.NewDecoder()
	cur := text
	var segs []*codec.EncodedSegment
	var out []scan.Finding
	seen := map[string]bool{}
	for range maxDecodeDepth {
		cur, segs = dec.Decode(cur, segs)
		if len(segs) == 0 {
			break
		}
		for _, f := range s.regex.Scan(field, cur) {
			if f.Offset < 0 {
				continue
			}
			over := codec.SegmentsWithDecodedOverlap(segs, f.Offset, f.Offset+f.Length)
			if len(over) == 0 {
				continue
			}
			idx := codec.AdjustMatchIndex(over, []int{f.Offset, f.Offset + f.Length})
			lo, hi := idx[0], idx[1]
			if lo < 0 || hi > len(text) || lo >= hi {
				continue
			}
			key := f.RuleID + "\x00" + strconv.Itoa(lo)
			if seen[key] {
				continue
			}
			seen[key] = true
			secret := cur[f.Offset : f.Offset+f.Length] // the decoded value: masked and hashed like a plain hit
			out = append(out, scan.Finding{RuleID: f.RuleID, Field: field, Line: strings.Count(text[:lo], "\n") + 1,
				Offset: lo, Length: hi - lo, Preview: scan.Mask(secret), MatchHash: scan.HashMatch(secret)})
		}
	}
	return out
}
