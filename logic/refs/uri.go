package refs

import (
	"net/url"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// URI reading of a reference body (SECRETS_AND_REFERENCES 5.1, 8.1):
//
//	body ─▶ secret? ─yes─▶ split "|" (≤3) ─▶ each: "unstored" | "redacted" | manager://locator?opts
//	          │no
//	          ▼
//	        split modifier (first ":-" / ":?") ─▶ net/url on "scheme:target?opts"
//
// Every problem is kept on the Ref (Problem, ProblemCode) for Validate;
// reading never fails, so prose with a broken reference still renders.

// Modifier is the optional tail of a non-secret reference (8.1).
type Modifier string

// Modifiers.
const (
	ModNone     Modifier = ""
	ModFallback Modifier = ":-" // ${var:x:-fallback}: used when x is dangling
	ModRequired Modifier = ":?" // ${var:x:?message}: a dangling x is E_REF_REQUIRED
)

// Option is one name=value of a reference's query, in written order.
type Option struct {
	Name  string
	Value string
}

// SecretTarget is one alternate of a secret reference: a manager URI, or
// one of the two fixed targets (5.4).
type SecretTarget struct {
	// Fixed is "unstored" or "redacted"; empty for a manager URI.
	Fixed    string
	Manager  string
	Locator  string
	Fragment string
	Options  []Option
}

// Fixed secret targets (5.4).
const (
	TargetUnstored = "unstored"
	TargetRedacted = "redacted"
)

// Caps of SECRETS_AND_REFERENCES 5.1 and 8.3.
const (
	MaxAlternates  = 3         // SQ5: at most 3 targets in a secret reference
	MaxDepth       = 8         // D14: nesting deeper is E_REF_DEPTH
	MaxResolutions = 256       // D17: resolutions per text, E_REF_LIMIT
	MaxExpanded    = 64 * 1024 // D17: bytes of expanded output per field, E_REF_LIMIT
	maxSchemeLen   = 32        // 8.1: scheme at most 32 chars
)

// OptKind is the res option naming the kind when a resource name is not
// unique across kinds (9).
const OptKind = "kind"

// Secret holder options, lore's own (5.2, SQ1).
const (
	OptHolder = "holder"
	OptLabel  = "label"
)

// Managers lists the built-in secret manager schemes and the options each
// registers besides holder and label (5.1 table, BATCH 9, SQ14). Plugin
// managers are a later release.
func Managers() map[string][]string {
	return map[string][]string{
		"gopass":         nil,
		"op":             nil,
		"bitwarden":      nil,
		"vault":          nil,
		"aws-secrets":    {"region"},
		"gcp-secrets":    {"project"},
		"azure-keyvault": nil,
		"env":            nil,
	}
}

// schemeOptions are the option names each built-in scheme accepts (8.1:
// an unknown option is E_REF_SYNTAX).
func schemeOptions(s Scheme) []string {
	if s == SchemeRes {
		return []string{OptKind}
	}
	return nil
}

// readURI fills r's URI fields from r.Body.
func readURI(r *Ref) {
	if len(r.Scheme) > maxSchemeLen {
		r.problem(errs.RefSyntax, "scheme longer than 32 characters")
		return
	}
	if r.Scheme == SchemeSecret {
		readSecret(r)
		return
	}
	body := r.Body
	if strings.HasPrefix(body, "//") {
		// plugin form ${jira://HUB-412?field=status}: kept whole, plugins
		// are a later release.
		r.Target = body
		return
	}
	if i := modifierAt(body); i >= 0 {
		r.Modifier = Modifier(body[i : i+2])
		r.Fallback = body[i+2:]
		body = body[:i]
	}
	target, query, _ := strings.Cut(body, "?")
	t, err := url.PathUnescape(target)
	if err != nil {
		r.problem(errs.RefSyntax, "bad percent-encoding in "+quoteRef(target))
		return
	}
	r.Target = t
	if strings.Contains(target, open) {
		r.problem(errs.RefSyntax, "a reference inside a path ("+quoteRef(target)+")")
		return
	}
	if r.Target == "" && r.Scheme.Known() {
		r.problem(errs.RefSyntax, "empty target")
		return
	}
	opts, perr := readOptions(query)
	if perr != "" {
		r.problem(errs.RefSyntax, perr)
		return
	}
	r.Options = opts
	if r.Scheme.Known() {
		allowed := schemeOptions(r.Scheme)
		for _, o := range opts {
			if !slices.Contains(allowed, o.Name) {
				r.problem(errs.RefSyntax, "unknown option "+quoteRef(o.Name)+" for ${"+string(r.Scheme)+":…}")
				return
			}
		}
	}
	if r.Modifier == ModRequired && strings.Contains(r.Fallback, open) {
		r.problem(errs.RefSyntax, "a :? message may not hold references")
	}
}

// modifierAt is the index of the first ":-" or ":?" in body, or -1 (8.1:
// the modifier is split off before the URI is parsed).
func modifierAt(body string) int {
	a, b := strings.Index(body, string(ModFallback)), strings.Index(body, string(ModRequired))
	switch {
	case a < 0:
		return b
	case b < 0:
		return a
	}
	return min(a, b)
}

// readSecret reads the "|"-separated targets of a secret reference.
func readSecret(r *Ref) {
	parts := strings.Split(r.Body, "|")
	if len(parts) > MaxAlternates {
		r.problem(errs.RefSyntax, "more than 3 secret alternates")
		return
	}
	for _, p := range parts {
		p = strings.Trim(p, " \t") // 5.3: spaces and tabs around | are accepted
		if p == TargetUnstored || p == TargetRedacted {
			r.Alternates = append(r.Alternates, SecretTarget{Fixed: p})
			continue
		}
		u, err := url.Parse(p)
		if err != nil || u.Scheme == "" || u.Opaque != "" {
			r.problem(errs.RefSyntax, "secret target "+quoteRef(p)+" is not manager://locator")
			return
		}
		extra, known := Managers()[u.Scheme]
		if !known {
			r.problem(errs.RefScheme, "no secret manager "+quoteRef(u.Scheme))
			return
		}
		opts, perr := readOptions(u.RawQuery)
		if perr != "" {
			r.problem(errs.RefSyntax, perr)
			return
		}
		allowed := append([]string{OptHolder, OptLabel}, extra...)
		for _, o := range opts {
			if !slices.Contains(allowed, o.Name) {
				r.problem(errs.RefSyntax, "unknown option "+quoteRef(o.Name)+" for secret manager "+u.Scheme)
				return
			}
		}
		r.Alternates = append(r.Alternates, SecretTarget{Manager: u.Scheme, Locator: u.Host + u.Path, Fragment: u.Fragment, Options: opts})
	}
}

// readOptions parses a query in written order; a repeated name is refused.
func readOptions(query string) ([]Option, string) {
	if query == "" {
		return nil, ""
	}
	var out []Option
	for kv := range strings.SplitSeq(query, "&") {
		k, v, _ := strings.Cut(kv, "=")
		name, err1 := url.QueryUnescape(k)
		val, err2 := url.QueryUnescape(v)
		if err1 != nil || err2 != nil || name == "" {
			return nil, "bad option " + quoteRef(kv)
		}
		if slices.ContainsFunc(out, func(o Option) bool { return o.Name == name }) {
			return nil, "option " + quoteRef(name) + " given twice"
		}
		out = append(out, Option{Name: name, Value: val})
	}
	return out, ""
}

// Option returns the value of option name, if given.
func (r Ref) Option(name string) (string, bool) {
	for _, o := range r.Options {
		if o.Name == name {
			return o.Value, true
		}
	}
	return "", false
}

func (r *Ref) problem(code errs.Code, msg string) {
	r.Problem, r.ProblemCode = msg, code
}

func quoteRef(s string) string { return `"` + s + `"` }

// Validate checks every reference of a built-in scheme in text, as a write
// does (5.1, 8.1): a malformed body, an unknown option, a reference inside
// a path or an unknown secret manager is refused. Unknown schemes stay
// prose (8.2) and pass.
func Validate(text string) error {
	for _, p := range Parse(text) {
		if p.Kind != PartRef || !p.Ref.Scheme.Known() {
			continue
		}
		if p.Ref.Problem != "" {
			return errs.New(p.Ref.ProblemCode, "reference "+p.Raw+": "+p.Ref.Problem)
		}
		if p.Ref.Modifier == ModFallback {
			if err := Validate(p.Ref.Fallback); err != nil {
				return err
			}
		}
	}
	return nil
}
