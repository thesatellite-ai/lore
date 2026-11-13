package gitleaks

import (
	"bytes"
	"path"
	"regexp"
	"strings"

	"github.com/spf13/viper"
	glconfig "github.com/zricethezav/gitleaks/v8/config"

	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// postFilter decides whether one finding of a built-in lore rule is kept
// (3.2: checks RE2 cannot express, registered by rule id; a TOML or row
// rule can never name code). match is the whole regex match, rest the
// text after it up to the end of its line.
type postFilter func(match, rest string) bool

// postFilters are lore's registered post-filters. They run only while the
// rule is the built-in one: a secret_rules row that changes the rule
// replaces its regex, and the filter no longer fits it.
var postFilters = map[string]postFilter{
	RulePasswordPair:  proseValue,
	RuleURLUserinfoID: tokenUser,
}

// passwordPairStopwords are placeholder values that are never a hit
// (3.4 step 3; gitleaks stopword semantics: they match the value).
var passwordPairStopwords = map[string]bool{
	"changeme": true, "placeholder": true, "redacted": true, "example": true, "password": true,
	"secret": true, "xxxxxx": true, "required": true, "optional": true, "none": true, "null": true, "empty": true,
}

var (
	plainWordRe = regexp.MustCompile(`^[A-Za-z]+$`)
	// pairValueRe finds the value part of a password_pair match.
	pairValueRe = regexp.MustCompile(`[=:]\s*`)
	twoLetterRe = regexp.MustCompile(`[A-Za-z]{2,}`)
)

// proseValue is the D10 post-filter of password_pair (3.4): a quoted value
// is a hit; an unquoted placeholder (stopword) is not; an unquoted plain
// word followed on the same line by more prose is not (a sentence such as
// "change that pass-word, credentials shouldn't sit in Slack" with the key
// spelt plainly); anything else is a hit.
func proseValue(match, rest string) bool {
	loc := pairValueRe.FindStringIndex(match)
	if loc == nil {
		return true
	}
	v := match[loc[1]:]
	if strings.HasPrefix(v, `"`) || strings.HasPrefix(v, `'`) {
		return true
	}
	word := strings.TrimRight(v, ".!?")
	if passwordPairStopwords[strings.ToLower(word)] || unquotedNonSecret(v) {
		return false
	}
	if !plainWordRe.MatchString(word) {
		return true
	}
	after := strings.TrimLeft(rest, " \t.,;:!?-–—()")
	if after == "" || !isLetter(after[0]) {
		return true // a plain word with nothing after it may be a weak password
	}
	return !twoLetterRe.MatchString(after)
}

// placeholderValue is password_pair's filter with the prose step off
// (policy secrets.password_pair_prose_filter: off): only an unquoted
// placeholder (3.4 step 3) is dropped; every other match is a hit.
func placeholderValue(match, _ string) bool {
	loc := pairValueRe.FindStringIndex(match)
	if loc == nil {
		return true
	}
	v := match[loc[1]:]
	if strings.HasPrefix(v, `"`) || strings.HasPrefix(v, `'`) {
		return true
	}
	return !passwordPairStopwords[strings.ToLower(strings.TrimRight(v, ".!?"))] && !unquotedNonSecret(v)
}

// valueOf is the credential part of a finding's text: credentialValue
// while the rule is still the built-in one (a changed rule's regex may
// cut its match differently), else the whole text.
func (s *Scanner) valueOf(ruleID, text string) string {
	if s.builtin[ruleID] {
		return credentialValue(ruleID, text)
	}
	return text
}

func isLetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

// tokenUser is the url_token_user post-filter (14.6): the URL's user name
// must be token-shaped (repourl.TokenShaped, the same rule the URL
// normaliser warns on), so https://mira@host is not a finding.
func tokenUser(match, _ string) bool {
	_, after, ok := strings.Cut(match, "://")
	if !ok {
		return false
	}
	user, _, ok := strings.Cut(after, "@")
	return ok && repourl.TokenShaped(user)
}

// RepoAllow is what lore takes from a repo's .gitleaks.toml while
// secrets.use_repo_gitleaks is on (D8): global allow patterns (regexes)
// and stopwords only. [[rules]], disabledRules, paths and commits are
// ignored, so the file can widen what is allowed but never switch a rule
// off. An entry with TargetRules applies only to those rule ids.
type RepoAllow struct {
	Entries []AllowEntry
}

// AllowEntry is one [[allowlists]] table of the repo file.
type AllowEntry struct {
	TargetRules []string
	Regexes     []string
	Stopwords   []string
}

// ParseRepoGitleaks reads a .gitleaks.toml (D8): its [[allowlists]] tables.
// The deprecated single [allowlist] table is not read (gitleaks drops it
// in 9.x); lore says nothing about it, so a repo moving to [[allowlists]]
// gets the same allow. A file that does not
// parse, or a regex that does not compile, is E_CONFIG_INVALID (fail
// closed: writes are refused until the file is fixed); the error never
// quotes a pattern.
func ParseRepoGitleaks(data []byte) (RepoAllow, error) {
	v := viper.New()
	v.SetConfigType("toml")
	if err := v.ReadConfig(bytes.NewReader(data)); err != nil {
		return RepoAllow{}, errs.Wrap(errs.ConfigInvalid, "gitleaks: .gitleaks.toml does not parse", err)
	}
	var vc glconfig.ViperConfig
	if err := v.Unmarshal(&vc); err != nil {
		return RepoAllow{}, errs.Wrap(errs.ConfigInvalid, "gitleaks: .gitleaks.toml cannot be read", err)
	}
	var out RepoAllow
	add := func(target, regexes, stop []string) error {
		for _, re := range regexes {
			if _, err := regexp.Compile(re); err != nil {
				return errs.New(errs.ConfigInvalid, "gitleaks: .gitleaks.toml has an allow regex that does not compile")
			}
		}
		if len(regexes)+len(stop) > 0 {
			out.Entries = append(out.Entries, AllowEntry{TargetRules: target, Regexes: regexes, Stopwords: stop})
		}
		return nil
	}
	for _, a := range vc.Allowlists {
		if a == nil {
			continue
		}
		if err := add(a.TargetRules, a.Regexes, a.StopWords); err != nil {
			return RepoAllow{}, err
		}
	}
	return out, nil
}

// compiledAllow is a RepoAllow ready to test findings.
type compiledAllow struct {
	targets map[string]bool // empty: every rule
	res     []*regexp.Regexp
	stop    []string // lower case
}

func compileAllow(a RepoAllow) []compiledAllow {
	out := make([]compiledAllow, 0, len(a.Entries))
	for _, e := range a.Entries {
		c := compiledAllow{targets: map[string]bool{}}
		for _, t := range e.TargetRules {
			c.targets[t] = true
		}
		for _, re := range e.Regexes {
			c.res = append(c.res, regexp.MustCompile(re)) // compiled once in ParseRepoGitleaks
		}
		for _, s := range e.Stopwords {
			c.stop = append(c.stop, strings.ToLower(s))
		}
		out = append(out, c)
	}
	return out
}

// allowed reports whether a finding's secret text is allowed by the repo
// file (regexTarget "secret", gitleaks' default).
func allowed(as []compiledAllow, ruleID, secret string) bool {
	low := strings.ToLower(secret)
	for _, a := range as {
		if len(a.targets) > 0 && !a.targets[ruleID] {
			continue
		}
		for _, re := range a.res {
			if re.MatchString(secret) {
				return true
			}
		}
		for _, s := range a.stop {
			if strings.Contains(low, s) {
				return true
			}
		}
	}
	return false
}

// loreIDTrail is punctuation a rule may capture after an id in prose
// ("see tsk_….").
const loreIDTrail = ".,;:)'\""

// isLoreID reports whether a finding's secret is exactly one lore row id
// (a registered prefix + UUIDv7 hex, ids.ParseAny), maybe followed by
// prose punctuation. OV40 measured switch: gitleaks' generic-api-key
// flags "task key tsk_<32 hex>" in v0.1.17 stores (1 of 4 hits over a
// 4727-file store). A row id is public by design (every row file, commit
// and leak index carries it) and never a credential, so this drops only
// that exact shape; every rule keeps running on everything else (D4:
// never weaken the rules).
func isLoreID(secret string) bool {
	_, err := ids.ParseAny(strings.TrimRight(secret, loreIDTrail))
	return err == nil
}

// RuleGenericAPIKey is gitleaks' keyword-and-entropy catch-all rule, the
// one that fires on lore's own identifiers next to a word such as "key",
// "auth" or "token" (item 30: 879 false hits on one migration commit).
const RuleGenericAPIKey = "generic-api-key"

// Shapes of lore's own non-secret values that generic-api-key was shown
// to match (item 30, fixture in filters_lore_test.go): hex digests and
// UUIDs. Hex is lower case: lore and git print digests that way. Lore row
// ids are already dropped by isLoreID; an HLC, a short hex prefix and an
// algorithm-tagged digest never matched, so they get no exemption.
var (
	// hexDigestRe: sha1 (40) and sha256 (64) hex digests.
	hexDigestRe = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	// uuidHexRe: a UUID's 32 hex digits without dashes, version 1..8 and
	// RFC 4122 variant (the body of every lore id). A random 32-hex key
	// fits this by chance 1 time in 64, so most bare hex keys still hit.
	uuidHexRe = regexp.MustCompile(`^[0-9a-f]{12}[1-8][0-9a-f]{3}[89ab][0-9a-f]{15}$`)
	// uuidRe: the canonical dashed UUID, version 1..8, RFC 4122 variant.
	uuidRe = regexp.MustCompile(`^(?i)[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

// isLoreShaped reports whether a generic-api-key secret is exactly a hex
// digest (sha1/sha256) or a UUID (dashed or bare hex), maybe followed by
// prose punctuation. Only generic-api-key gets this: it has no fixed token
// format, so it flags these high-entropy digests next to a word such as
// "key" or "token"; every named-format rule still runs on them (D4).
func isLoreShaped(secret string) bool {
	v := strings.TrimRight(secret, loreIDTrail)
	return hexDigestRe.MatchString(v) || uuidHexRe.MatchString(v) || uuidRe.MatchString(v)
}

// moduleSumFiles are the Go checksum files (by base name) whose "h1:"
// lines are module hashes.
var moduleSumFiles = map[string]bool{"go.sum": true, "go.work.sum": true}

// moduleSumLine is one well-formed checksum line: a module path, a version
// (with an optional /go.mod suffix) and an "h1:" hash, which is a base64
// SHA-256 (43 characters and one "=").
var moduleSumLine = regexp.MustCompile(`^\S+ \S+ h1:[A-Za-z0-9+/]{43}=$`)

// moduleSumHashPrefix starts the hash on a checksum line.
const moduleSumHashPrefix = " h1:"

// isModuleSumHash reports whether the finding at text[off:off+n] lies
// inside the hash of a well-formed checksum line of a go.sum or
// go.work.sum file (field is the file's path). A module hash is a public
// digest of published source, never a credential, but gitleaks'
// generic-api-key matches one whose module path holds a keyword such as
// "token" (modernc.org/token). Only that exact shape in those files is
// dropped: any other line of the file, a malformed line, or the same line
// in another file is scanned as usual (D4: never weaken the rules).
func isModuleSumHash(field, text string, off, n int) bool {
	if !moduleSumFiles[path.Base(strings.ReplaceAll(field, "\\", "/"))] {
		return false
	}
	start := strings.LastIndexByte(text[:off], '\n') + 1
	end := off + n
	if i := strings.IndexByte(text[end:], '\n'); i >= 0 {
		end += i
	} else {
		end = len(text)
	}
	line := strings.TrimSuffix(text[start:end], "\r")
	if !moduleSumLine.MatchString(line) {
		return false
	}
	return off-start >= strings.LastIndex(line, moduleSumHashPrefix)+len(moduleSumHashPrefix)
}

// lineRest is the text after end up to the end of its line.
func lineRest(text string, end int) string {
	if end < 0 || end > len(text) {
		return ""
	}
	rest := text[end:]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// keep applies the post-filters and the repo allow to findings of text.
// builtin names the rules still in their built-in form.
func (s *Scanner) keep(text string, fs []scan.Finding) []scan.Finding {
	out := fs[:0]
	for _, f := range fs {
		in := f.Offset >= 0 && f.Offset+f.Length <= len(text)
		if in && s.builtin[f.RuleID] {
			pf, ok := postFilters[f.RuleID]
			if ok && s.noProse && f.RuleID == RulePasswordPair {
				pf = placeholderValue // prose filter off: 3.4 step 2 goes, the stopwords (step 3) stay
			}
			if ok && !pf(text[f.Offset:f.Offset+f.Length], lineRest(text, f.Offset+f.Length)) {
				continue
			}
		}
		if in && s.builtin[f.RuleID] && f.RuleID == RuleURLWithPassword && passIsUser(text[f.Offset:f.Offset+f.Length]) {
			continue // postgres:postgres, guest:guest: no secret beyond the user name
		}
		if in && isPlaceholder(s.valueOf(f.RuleID, text[f.Offset:f.Offset+f.Length])) {
			continue // a template reference or mask, never a credential (every rule)
		}
		if in && len(s.allow) > 0 && allowed(s.allow, f.RuleID, text[f.Offset:f.Offset+f.Length]) {
			continue
		}
		if in && isLoreID(text[f.Offset:f.Offset+f.Length]) {
			continue
		}
		if in && f.RuleID == RuleGenericAPIKey && isLoreShaped(text[f.Offset:f.Offset+f.Length]) {
			continue
		}
		if in && isModuleSumHash(f.Field, text, f.Offset, f.Length) {
			continue
		}
		out = append(out, f)
	}
	return out
}
