package gitleaks

import (
	"regexp"
	"strings"
)

// placeholderRes are values that only stand for a secret: a template or
// shell reference (${DB_PASSWORD}, $DB_PASSWORD, $(DB_PASSWORD),
// ${{ secrets.X }}, {{ .Values.x }}, %DB_PASSWORD%, %(pw)s, %s), an angle
// bracket name (<password>) and a mask (***, xxxx, ••••). Each is anchored:
// the whole value must be one of them, so "${A}hunter2" or "<x>tail" stays
// a hit (user report: a config with postgres://app:${DB_PASSWORD}@db/app
// or password: "${DB_PASSWORD}" was refused and people reached for
// --no-verify).
var placeholderRes = []*regexp.Regexp{
	regexp.MustCompile(`^\$\{[^{}]*\}$`),
	regexp.MustCompile(`^\$\{\{[^{}]*\}\}$`),
	regexp.MustCompile(`^\$[A-Za-z_][A-Za-z0-9_]*$`),
	regexp.MustCompile(`^\$\([A-Za-z_][A-Za-z0-9_.]*\)$`),
	regexp.MustCompile(`^\{\{\{?[^{}]*\}?\}\}$`),
	regexp.MustCompile(`^<[^<>\s][^<>]*>$`),
	regexp.MustCompile(`^%[A-Za-z_][A-Za-z0-9_]*%$`),
	regexp.MustCompile(`^%(?:\([A-Za-z_][A-Za-z0-9_]*\))?s$`),
	regexp.MustCompile(`^(?:\*{3,}|[xX]{3,}|•{3,})$`),
}

// isPlaceholder reports whether v (quotes and surrounding blanks removed)
// is entirely a placeholder, never a credential.
func isPlaceholder(v string) bool {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		v = strings.TrimSpace(v[1 : len(v)-1])
	}
	if v == "" {
		return false
	}
	for _, re := range placeholderRes {
		if re.MatchString(v) {
			return true
		}
	}
	return false
}

// credentialValue is the part of a built-in rule's match that holds the
// credential: the value of a password pair, the password of a URL's user
// info; any other rule's finding already is its secret (gitleaks findings
// are cut to the rule's secret group in convert).
func credentialValue(ruleID, match string) string {
	switch ruleID {
	case RulePasswordPair:
		if loc := pairValueRe.FindStringIndex(match); loc != nil {
			return match[loc[1]:]
		}
	case RuleURLWithPassword:
		if _, after, ok := strings.Cut(match, "://"); ok {
			if info, _, ok := strings.Cut(after, "@"); ok {
				if _, pw, ok := strings.Cut(info, ":"); ok {
					return pw
				}
			}
		}
	}
	return match
}
