package gitleaks

import (
	"regexp"
	"strings"
)

// placeholderRes are values that only stand for a secret: a template or
// shell reference (${DB_PASSWORD}, $DB_PASSWORD, $(DB_PASSWORD),
// ${{ secrets.X }}, {{ .Values.x }}, %DB_PASSWORD%, %(pw)s, %s), an angle
// bracket name (<password>), a mask (***, xxxx, ••••), a token whose body
// is a mask (ghp_xxxx…, AKIAXXXX…, sk-ant-xxxx…: S-4, the .env.example
// shape), and a secret manager reference (gopass://team/db, op://vault/x).
// Each is anchored: the whole value must be one of them, so "${A}hunter2"
// or "<x>tail" stays a hit (user report: a config with
// postgres://app:${DB_PASSWORD}@db/app or password: "${DB_PASSWORD}" was
// refused and people reached for --no-verify).
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
	// a short token prefix (ghp_, sk-ant-, AKIA, xoxb-) then a mask of at
	// least 8; a real token never ends in eight of one mask character.
	regexp.MustCompile(`^[A-Za-z0-9]{0,8}(?:[_-][A-Za-z0-9]{1,8}){0,2}[_-]?(?:x{8,}|X{8,}|\*{8,}|•{8,})$`),
	regexp.MustCompile(`^(?:gopass|pass|op|vault|bw|keychain|sops|ssm|awssm|gcpsm|azkv|secretsmanager)://\S+$`),
}

// placeholderMarkers are words that make a value a stand-in ("your-api-key",
// "REPLACE_ME", "change-this-in-production", "[REDACTED]"); placeholderWords
// are the other words such a phrase is made of. A value whose words are
// all in the two sets, with at least one marker, is a placeholder (S-4).
var (
	placeholderMarkers = wordSet("your yours my replace change changeme insert enter put here example examples sample " +
		"placeholder redacted dummy fake todo fixme tbd xxx xxxx")
	placeholderWords = wordSet("a an the this that it me with in to of for goes value real actual own new " +
		"prod production dev development staging local test testing " +
		"api key keys token tokens access secret secrets password passwords pass passwd pw pwd passphrase " +
		"db database bot app user client id private public auth oauth bearer session signing " +
		"account service webhook admin root master live")
	// placeholderAlone are values that are a stand-in on their own (3.4
	// step 3 stopwords, now for every rule and quoted or not: a
	// "changeme" value or a localhost URL whose password is the word itself in an
	// example config never holds a credential).
	placeholderAlone = wordSet("changeme placeholder redacted example sample dummy password passwd pass " +
		"secret required optional none null nil empty undefined todo fixme tbd")
	phraseSplitRe = regexp.MustCompile(`[-_ .]+`)
)

func wordSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

// isPlaceholderPhrase reports whether v ("YOUR_ACCESS_TOKEN",
// a Slack prefix then "your-bot-token", "[REDACTED]") is made only of placeholder words
// with at least one marker. The first word may be a short token prefix
// (xoxb, sk) when a marker follows it.
func isPlaceholderPhrase(v string) bool {
	if len(v) >= 2 && v[0] == '[' && v[len(v)-1] == ']' {
		v = v[1 : len(v)-1]
	}
	words := phraseSplitRe.Split(strings.ToLower(strings.Trim(v, "-_ .")), -1)
	if len(words) == 1 {
		return placeholderAlone[words[0]]
	}
	marker := false
	for i, w := range words {
		switch {
		case placeholderMarkers[w]:
			marker = true
		case placeholderWords[w]:
		case i == 0 && len(w) <= 5 && len(words) >= 3:
		default:
			return false
		}
	}
	return marker
}

// isPlaceholder reports whether v (quotes, brackets and surrounding
// blanks removed) is entirely a placeholder, never a credential: a
// template reference, a mask or a placeholder phrase. AWS's documented
// AKIA…EXAMPLE key stays a hit on purpose: lore's journeys and team tests
// use it as the stand-in real key that every gate must refuse.
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
	return isPlaceholderPhrase(v)
}

// bearerValueRe finds the token part of an authorization_bearer match.
var bearerValueRe = regexp.MustCompile(`(?i)bearer\s+`)

// credentialValue is the part of a built-in rule's match that holds the
// credential: the value of a password pair, the password of a URL's user
// info, the token of an Authorization header; any other rule's finding
// already is its secret (gitleaks findings are cut to the rule's secret
// group in convert).
func credentialValue(ruleID, match string) string {
	switch ruleID {
	case RulePasswordPair:
		if loc := pairValueRe.FindStringIndex(match); loc != nil {
			return match[loc[1]:]
		}
	case RuleURLWithPassword:
		if _, pw, ok := urlUserinfo(match); ok {
			return pw
		}
	case RuleAuthorizationBearer:
		if loc := bearerValueRe.FindStringIndex(match); loc != nil {
			return match[loc[1]:]
		}
	}
	return match
}

// urlUserinfo splits a URL match's user info into user and password.
func urlUserinfo(match string) (user, pw string, ok bool) {
	_, after, ok := strings.Cut(match, "://")
	if !ok {
		return "", "", false
	}
	info, _, ok := strings.Cut(after, "@")
	if !ok {
		return "", "", false
	}
	return strings.Cut(info, ":")
}

// passIsUser reports whether a url_with_password match's password is its
// user name (user "postgres" with password "postgres", "guest" with "guest"):
// a service's documented default, and the URL already shows that text.
func passIsUser(match string) bool {
	user, pw, ok := urlUserinfo(match)
	return ok && pw != "" && pw == user
}

// codeRefRe is an unquoted password_pair value that is code, not a
// literal: a member access or call (os.Getenv(, process.env.DB_PASS,
// config.Database.Password, getpass.getpass()). Every segment starts
// with a letter, so a value such as Admin.2024 stays a hit.
var codeRefRe = regexp.MustCompile(`^[A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)+(?:\(.*)?$|^[A-Za-z_$][\w$]*\($`)

// typeWords are unquoted password_pair values that are a field's type in a
// schema or struct (a GraphQL String! or a Go string field), never a value.
var typeWords = wordSet("string str text varchar char bytes bytea binary blob secretstr")

// unquotedNonSecret reports whether an unquoted password_pair value is
// code, a type name, a YAML tag (!vault) or a sops-encrypted value (ENC[),
// none of which is a plain-text credential (S-4).
func unquotedNonSecret(v string) bool {
	return codeRefRe.MatchString(v) || typeWords[strings.ToLower(strings.TrimRight(v, "!?"))] ||
		strings.HasPrefix(v, "!") || strings.HasPrefix(v, "ENC[")
}
