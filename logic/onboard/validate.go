package onboard

import (
	"net/mail"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/model/row"
)

// Answer validation (ONB 2.3: pure and instant; while any FieldError
// exists nothing is probed). The same rules re-validate every value a fix
// is filled with (fix.go), so a value that could not be typed into the
// page cannot reach a fix argv either.

// Rule names reported in FieldError.Rule besides the Question rules.
const (
	// FieldRuleRequired: a required question has no answer.
	FieldRuleRequired = "required"
	// FieldRuleType: the answer's shape does not fit the QType.
	FieldRuleType = "type"
	// FieldRuleChoice: the answer is not one of the static choices.
	FieldRuleChoice = "choice"
	// FieldRuleUnknown: an answer for no question of the step, or a
	// catalog rule this package does not know.
	FieldRuleUnknown = "unknown"
)

// snakeRe is RuleSnakeCase.
var snakeRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// scpURLRe is the scp-like git form user@host:path (no scheme).
var scpURLRe = regexp.MustCompile(`^([A-Za-z0-9._-]+)@([A-Za-z0-9.-]+):([^/\\][^\\]*)$`)

// GitURL is a parsed store url (ONB 4.4 store.url).
type GitURL struct {
	Scheme string // "scp", "ssh", "https", "file"
	Host   string
	// Owner and Repo are the first two path segments for a host url
	// (Repo without .git); empty for file://.
	Owner string
	Repo  string
}

// Git url schemes ParseGitURL accepts.
const (
	GitSchemeSCP   = "scp"
	GitSchemeSSH   = "ssh"
	GitSchemeHTTPS = "https"
	GitSchemeFile  = "file"
)

// ParseGitURL parses a store url; why is the reason when ok is false. A
// url carrying a password or token (user:token@, or any userinfo on
// https) is refused: lore never stores a token inside a url (4.4).
func ParseGitURL(s string) (u GitURL, why string, ok bool) {
	if s == "" {
		return GitURL{}, "is empty", false
	}
	// A local store is a folder, and a folder name may hold spaces
	// ("~/My Stores/me.git"): a file:// url may carry them as typed or as
	// %20. A hosted url never has one, so a space there is still refused.
	if hasControl(s) || strings.Contains(s, "\t") || (strings.Contains(s, " ") && !isFileURL(s)) {
		return GitURL{}, "has spaces or control characters", false
	}
	if strings.HasPrefix(s, "-") {
		return GitURL{}, "must not start with -", false
	}
	if !strings.Contains(s, "://") {
		if user, _, found := strings.Cut(s, "@"); found && strings.Contains(user, ":") {
			return GitURL{}, "carries a password or token; a token inside a url is never stored", false
		}
	}
	if m := scpURLRe.FindStringSubmatch(s); m != nil && !strings.Contains(s, "://") {
		o, r := ownerRepo(m[3])
		return GitURL{Scheme: GitSchemeSCP, Host: m[2], Owner: o, Repo: r}, "", true
	}
	p, err := url.Parse(s)
	if err != nil {
		return GitURL{}, "is not a url", false
	}
	switch p.Scheme {
	case GitSchemeFile:
		if p.User != nil {
			return GitURL{}, "file urls take no user", false
		}
		if p.Path == "" && p.Host == "" {
			return GitURL{}, "names no folder", false
		}
		return GitURL{Scheme: GitSchemeFile}, "", true
	case GitSchemeSSH, GitSchemeHTTPS:
	default:
		return GitURL{}, "must be git@host:owner/repo, ssh://, https:// or file://", false
	}
	if p.User != nil {
		if _, hasPw := p.User.Password(); hasPw || p.Scheme == GitSchemeHTTPS {
			return GitURL{}, "carries a password or token; a token inside a url is never stored", false
		}
	}
	if p.Host == "" {
		return GitURL{}, "names no host", false
	}
	if p.RawQuery != "" || p.Fragment != "" {
		return GitURL{}, "must not carry a query or fragment", false
	}
	o, r := ownerRepo(strings.TrimPrefix(p.Path, "/"))
	return GitURL{Scheme: p.Scheme, Host: p.Hostname(), Owner: o, Repo: r}, "", true
}

// isFileURL reports a file:// url (the local-folder scheme).
func isFileURL(s string) bool { return strings.HasPrefix(s, GitSchemeFile+"://") }

// ownerRepo splits "owner/repo(.git)" (more segments: the first two).
func ownerRepo(path string) (owner, repo string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		return "", ""
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git")
}

// OwnerRepo is the owner and repo of a host store url (the {owner} and
// {repo} fix placeholders); false for file:// and bad urls.
func OwnerRepo(s string) (owner, repo string, ok bool) {
	u, _, valid := ParseGitURL(s)
	if !valid || u.Owner == "" || u.Repo == "" {
		return "", "", false
	}
	return u.Owner, u.Repo, true
}

// hasControl reports a control character (newline, NUL, escape, …).
func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// ValidateValue applies one rule to one value; why says what is wrong
// when ok is false. An unknown rule kind is never ok (a catalog bug must
// not let values through).
func ValidateValue(r Rule, v string) (why string, ok bool) {
	switch r.Kind() {
	case RuleEmail:
		a, err := mail.ParseAddress(v)
		if err != nil || a.Address != v || a.Name != "" {
			return "is not a bare email address (like you@company.example)", false
		}
	case RuleSnakeCase:
		if !snakeRe.MatchString(v) {
			return "must be snake_case: a-z, 0-9 and _, starting with a letter", false
		}
	case RuleSetupName:
		if !ValidSetupName(v) {
			return "must be snake_case or kebab-case (a-z, 0-9, _ or -, starting with a letter), at most " + strconv.Itoa(SetupNameMaxLen) + " characters", false
		}
	case RuleOrgName:
		// The team name is both an orgs[] entry (orgconf: a-z, 0-9, '.',
		// '-') and the team store's name in .lore/stores.yaml (row: snake
		// case): a name only one of them takes was accepted here and then
		// refused by the save (E_CONFIG_INVALID).
		if err := orgconf.ValidateOrgName(v); err != nil || !row.ValidName(v) {
			return "must be lowercase letters and digits, starting with a letter (it names the team and its store)", false
		}
	case RuleGitURL:
		if _, w, good := ParseGitURL(v); !good {
			return w, false
		}
	case RuleMaxLen:
		n, err := strconv.Atoi(r.Arg())
		if err != nil || n < 1 {
			return "rule " + string(r) + " has no valid length", false
		}
		if utf8.RuneCountInString(v) > n {
			return "is longer than " + strconv.Itoa(n) + " characters", false
		}
	case RuleNotIn:
		for w := range strings.SplitSeq(r.Arg(), ",") {
			if w != "" && strings.EqualFold(v, w) {
				return strconv.Quote(v) + " is reserved (" + r.Arg() + ")", false
			}
		}
	case RuleKeyPath:
		switch {
		case hasControl(v) || strings.HasPrefix(v, "-"):
			return "must be a key file path (no control characters, not starting with -)", false
		case !(strings.HasPrefix(v, "~/") || strings.HasPrefix(v, "/") || filepath.IsAbs(v)):
			return "must be a key file path that starts with / or ~/, like ~/.ssh/id_ed25519", false
		}
	case RuleAbsGlob:
		if hasControl(v) || !(v == "~" || strings.HasPrefix(v, "~/") || strings.HasPrefix(v, "/") || filepath.IsAbs(v)) {
			return "must be absolute (start with / or ~/)", false
		}
	default:
		return "unknown rule " + strconv.Quote(string(r)), false
	}
	return "", true
}

// knownRuleKinds is every RuleKind ValidateValue implements.
func knownRuleKinds() []RuleKind {
	return []RuleKind{RuleEmail, RuleSnakeCase, RuleSetupName, RuleOrgName, RuleGitURL, RuleMaxLen, RuleNotIn, RuleKeyPath, RuleAbsGlob}
}

// questionApplies reports whether q is asked for kind.
func questionApplies(q Question, kind Kind) bool {
	return len(q.Kinds) == 0 || slices.Contains(q.Kinds, kind)
}

// ValidateAnswers checks one step's answers for kind: shape, required,
// static choices, then every Rule (on Text, or on each List element).
// Empty means valid. Results are in question order, unknown keys last
// (sorted), so the page shows them stably.
func ValidateAnswers(step Step, kind Kind, a Answers) []FieldError {
	var out []FieldError
	asked := map[string]bool{}
	for _, q := range step.Questions {
		if !questionApplies(q, kind) {
			continue
		}
		asked[q.ID] = true
		ans, given := a[q.ID]
		if fe, bad := shapeError(q, ans, given); bad {
			out = append(out, fe)
			continue
		}
		values := answerValues(q, ans, given)
		if len(values) == 0 {
			if q.Required {
				out = append(out, FieldError{Question: q.ID, Rule: FieldRuleRequired, Message: q.Label + " is required"})
			}
			continue
		}
		out = append(out, choiceErrors(q, values)...)
		for _, r := range q.Rules {
			for _, v := range values {
				if why, ok := ValidateValue(r, v); !ok {
					out = append(out, FieldError{Question: q.ID, Rule: string(r), Message: fieldMsg(q, v, why)})
					break
				}
			}
		}
	}
	var extra []string
	for k := range a {
		if !asked[k] {
			extra = append(extra, k)
		}
	}
	slices.Sort(extra)
	for _, k := range extra {
		out = append(out, FieldError{Question: k, Rule: FieldRuleUnknown, Message: strconv.Quote(k) + " is not a question of step " + string(step.ID)})
	}
	return out
}

// fieldMsg names the offending value for list answers.
func fieldMsg(q Question, v, why string) string {
	if q.Type.IsList() {
		return q.Label + ": " + strconv.Quote(v) + " " + why
	}
	return q.Label + " " + why
}

// shapeError reports an answer whose JSON form does not fit the type.
func shapeError(q Question, ans Answer, given bool) (FieldError, bool) {
	if !given {
		return FieldError{}, false
	}
	var want string
	switch {
	case q.Type == QBool && !ans.IsBool:
		want = "true or false"
	case q.Type.IsList() && !ans.IsList:
		want = "a list"
	case q.Type != QBool && !q.Type.IsList() && (ans.IsList || ans.IsBool):
		want = "a single value"
	default:
		return FieldError{}, false
	}
	return FieldError{Question: q.ID, Rule: FieldRuleType, Message: q.Label + " must be " + want}, true
}

// answerValues is the answer's non-empty string values; a bool answer
// counts as one value (it was given).
func answerValues(q Question, ans Answer, given bool) []string {
	if !given {
		return nil
	}
	switch {
	case q.Type == QBool:
		return []string{strconv.FormatBool(ans.Bool)}
	case q.Type.IsList():
		var vs []string
		for _, v := range ans.List {
			if s := strings.TrimSpace(v); s != "" {
				vs = append(vs, v)
			}
		}
		return vs
	}
	if strings.TrimSpace(ans.Text) == "" {
		return nil
	}
	return []string{ans.Text}
}

// choiceErrors refuses a value outside a purely static choice list (a
// prefill-backed list is only known to the Prober, so it is not checked
// here; the Rules still are).
func choiceErrors(q Question, values []string) []FieldError {
	if (q.Type != QChoice && q.Type != QMulti) || len(q.Choices.Static) == 0 || q.Choices.Prefill != PrefillNone {
		return nil
	}
	for _, v := range values {
		if !slices.ContainsFunc(q.Choices.Static, func(c Choice) bool { return c.Value == v }) {
			return []FieldError{{Question: q.ID, Rule: FieldRuleChoice, Message: q.Label + ": " + strconv.Quote(v) + " is not one of the choices"}}
		}
	}
	return nil
}
