// Package security provides secret-pattern detection for lore.
//
// Every text-input write (memory.add, rule.add, decision.add, hotfix.add,
// learn-from import) routes through Scan before INSERT. If any registered
// pattern matches, the write is refused with E_SECRET_DETECTED unless the
// user explicitly opts out via --allow-secrets (logged loud).
//
// Patterns cover common credential formats:
//
//   - AWS access keys (AKIA / ASIA / AGPA / etc.)
//   - GitHub tokens (ghp_ / gho_ / ghu_ / ghs_ / ghr_)
//   - JWTs (header.payload.signature with eyJ-prefix)
//   - Stripe live keys (sk_live_)
//   - OpenAI keys (sk-)
//   - PEM private key blocks (-----BEGIN ... PRIVATE KEY-----)
//   - Slack tokens (xox[abp]-)
//   - Google API keys (AIza)
//
// Catches: R16 #12, R23 #17, R20 (sanitize default ON, gitleaks-style ruleset).
//
// Users can extend with custom patterns via ~/.lore/pii-patterns.txt
// (one regex per line, # for comments). Loaded by LoadUserPatterns.
package security

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Pattern names a single secret detector.
type Pattern struct {
	Name    string         // human-friendly identifier shown in error message
	Regex   *regexp.Regexp // compiled detector
	Builtin bool           // true for built-in patterns (false for user-loaded)
}

// Match describes a single secret hit found in scanned content.
type Match struct {
	PatternName string // which pattern matched
	Offset      int    // byte offset of first match
	Length      int    // length of matched substring
	Preview     string // redacted preview (first 4 chars + ****)
}

// Built-in patterns. Order matters slightly — most-specific first reduces
// false-positive overlap when multiple patterns match the same substring.
var builtinPatterns = []Pattern{
	{
		Name:    "aws_access_key",
		Regex:   regexp.MustCompile(`\b(AKIA|ASIA|AGPA|AIDA|AROA|AIPA|ANPA|ANVA|ASCA)[A-Z0-9]{16}\b`),
		Builtin: true,
	},
	{
		Name:    "github_token",
		Regex:   regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9_]{36,251}\b`),
		Builtin: true,
	},
	{
		Name:    "jwt",
		Regex:   regexp.MustCompile(`\beyJ[A-Za-z0-9_=-]+\.eyJ[A-Za-z0-9_=-]+\.[A-Za-z0-9_=.+/-]+\b`),
		Builtin: true,
	},
	{
		Name:    "stripe_live_key",
		Regex:   regexp.MustCompile(`\bsk_live_[A-Za-z0-9]{24,}\b`),
		Builtin: true,
	},
	{
		Name:    "stripe_test_key",
		Regex:   regexp.MustCompile(`\bsk_test_[A-Za-z0-9]{24,}\b`),
		Builtin: true,
	},
	{
		Name:    "openai_key",
		Regex:   regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`),
		Builtin: true,
	},
	{
		Name:    "slack_token",
		Regex:   regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}\b`),
		Builtin: true,
	},
	{
		Name:    "google_api_key",
		Regex:   regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`),
		Builtin: true,
	},
	{
		Name:    "pem_private_key",
		Regex:   regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
		Builtin: true,
	},
	{
		Name:    "ssh_private_key",
		Regex:   regexp.MustCompile(`-----BEGIN OPENSSH PRIVATE KEY-----`),
		Builtin: true,
	},
}

// Scanner holds the active set of patterns. Use NewScanner to construct.
type Scanner struct {
	patterns []Pattern
}

// NewScanner returns a scanner with the built-in patterns loaded.
func NewScanner() *Scanner {
	patterns := make([]Pattern, len(builtinPatterns))
	copy(patterns, builtinPatterns)
	return &Scanner{patterns: patterns}
}

// AddPattern adds a custom pattern. Use for user-defined regexes loaded from
// ~/.lore/pii-patterns.txt. Returns error on regex compile failure.
func (s *Scanner) AddPattern(name, expr string) error {
	re, err := regexp.Compile(expr)
	if err != nil {
		return fmt.Errorf("security: compile %q: %w", name, err)
	}
	s.patterns = append(s.patterns, Pattern{
		Name:    name,
		Regex:   re,
		Builtin: false,
	})
	return nil
}

// Scan returns all matches in s. Returns an empty slice (not nil) if clean.
//
// Each pattern's first match is recorded; subsequent matches of the same
// pattern are NOT — this keeps error messages bounded. If a caller needs
// full enumeration, use Pattern.Regex.FindAllIndex directly.
func (s *Scanner) Scan(content string) []Match {
	var matches []Match
	for _, p := range s.patterns {
		loc := p.Regex.FindStringIndex(content)
		if loc == nil {
			continue
		}
		matches = append(matches, Match{
			PatternName: p.Name,
			Offset:      loc[0],
			Length:      loc[1] - loc[0],
			Preview:     redact(content[loc[0]:loc[1]]),
		})
	}
	return matches
}

// HasSecrets returns true if any pattern matches. Cheaper than Scan when
// the caller only needs a yes/no.
func (s *Scanner) HasSecrets(content string) bool {
	for _, p := range s.patterns {
		if p.Regex.MatchString(content) {
			return true
		}
	}
	return false
}

// LoadUserPatterns reads patterns from ~/.lore/pii-patterns.txt (one regex
// per line, blank lines and lines starting with # are ignored). Each line is
// added with name "user:<line-number>".
//
// If the file does not exist, returns nil (not an error — common case).
func (s *Scanner) LoadUserPatterns(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("security: open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := s.AddPattern(fmt.Sprintf("user:%d", lineNum), line); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// PatternCount reports how many patterns are loaded (built-in + user).
func (s *Scanner) PatternCount() int {
	return len(s.patterns)
}

// redact returns a safe-for-log preview of a matched substring:
// first 4 chars + "****". For very short matches, returns "****".
func redact(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[:4] + "****"
}
