package gitleaks

import (
	"strings"
	"testing"
)

// TestPlaceholderNotASecret (user report): a value that is entirely a
// placeholder (${X}, $X, {{ x }}, <x>, ***) is no finding of a password
// or URL credential rule, quoted or not, while a real-looking value in the
// same place, or a placeholder with real text beside it, still is. Key
// words are joined at run time (CLAUDE.md convention 8).
func TestPlaceholderNotASecret(t *testing.T) {
	t.Parallel()
	s := full(t)
	pw := "pass" + "word"
	real := "Zq7" + strings.Repeat("x9K", 4)
	clean := []string{
		"postgres://app:${DB_PASSWORD}@db/app",
		"postgres://app:$DB_PASSWORD@db/app",
		"postgres://app:{{.Pass}}@db/app",
		"postgres://app:<password>@db/app",
		"postgres://app:***@db/app",
		"postgres://app:" + "%(pw)s" + "@db/app",
		pw + `: "${DB_PASSWORD}"`,
		pw + `: "{{ .Values.dbPassword }}"`,
		pw + `: '<your-` + pw + `>'`,
		pw + `= "$DB_PASSWORD"`,
		pw + `: "${{ secrets.DB }}"`,
		pw + `: "********"`,
		"api_key = \"${API_KEY}\"",
	}
	for _, x := range clean {
		if fs := s.Scan("body", x); len(fs) > 0 {
			t.Errorf("placeholder flagged by %v: %q", ruleIDs(fs), x)
		}
	}
	hits := []struct{ text, rule string }{
		{"postgres://app:" + real + "@db/app", RuleURLWithPassword},
		{pw + `: "` + real + `"`, RulePasswordPair},
		{pw + `: "${A}` + real + `"`, RulePasswordPair},
		{pw + `: "<` + real + `"`, RulePasswordPair},
	}
	for _, h := range hits {
		if !ruleIDs(s.Scan("body", h.text))[h.rule] {
			t.Errorf("real-looking value not flagged by %s (case length %d)", h.rule, len(h.text))
		}
	}
}
