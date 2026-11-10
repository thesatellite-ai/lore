// Package refs parses and expands the one placeholder syntax lore allows in
// any text field (DECISIONS_LOG 14, REFERENCES):
//
//	${env:X}  ${var:x}  ${meta:k}  ${res:name.field}  ${row:id}  ${secret:…}  $${escaped}
//
// Row text always keeps the references (SQ3); the filled-in text lives only
// in the cache (row_expanded), built by Expander, with row_refs listing the
// ids each row uses (spec 4.5).
//
// Layer L1: pure; the values come from a Resolver the caller supplies.
//
// Invariants:
//   - Parse never fails and never loses text: joining every Part's Raw
//     gives back the input byte for byte (the fuzz test pins this).
//   - Expander never expands a secret (SQ3, SQ4) and never expands env
//     (BATCH 10): both stay exactly as written, whatever the Resolver does.
package refs

import (
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Scheme is the word before the first colon of a reference.
type Scheme string

// Lore's built-in schemes. Other schemes (plugins, later) parse as
// references with an unknown scheme and are never expanded here.
const (
	SchemeEnv    Scheme = "env"
	SchemeVar    Scheme = "var"
	SchemeMeta   Scheme = "meta"
	SchemeRes    Scheme = "res"
	SchemeRow    Scheme = "row"
	SchemeSecret Scheme = "secret"
)

// Schemes lists the built-in schemes.
func Schemes() []Scheme {
	return []Scheme{SchemeEnv, SchemeVar, SchemeMeta, SchemeRes, SchemeRow, SchemeSecret}
}

// Known reports a built-in scheme.
func (s Scheme) Known() bool { return slices.Contains(Schemes(), s) }

// Expandable reports whether Expander may fill in s: every built-in scheme
// except secret (never expanded, SQ3) and env (machine-local, BATCH 10).
func (s Scheme) Expandable() bool { return s.Known() && s != SchemeSecret && s != SchemeEnv }

// Syntax pieces.
const (
	open      = "${"
	escapeLit = "$${"
	closer    = "}"
	schemeSep = ":"
)

// Unstored is the reference that replaces a detected secret that has no
// vault entry yet (secrets.on_detect: redact, DECISIONS_LOG 14).
const Unstored = open + string(SchemeSecret) + schemeSep + "unstored" + closer

// Ref is one parsed reference.
//
// Body is the raw text after the first colon; the other fields are its URI
// reading (SECRETS_AND_REFERENCES 8.1), filled by Parse:
//
//	${var:api_host:-x?y}       Target "api_host", Modifier ModFallback, Fallback "x?y"
//	${res:hub_prod/ssh?kind=host}  Target "hub_prod/ssh", Options [{kind host}]
//	${secret:gopass://a/b?holder=mch_…&label=m | env://K}  Alternates (≤3, SQ5)
//
// Problem is non-empty when the body is not a valid reference of its
// scheme (Validate turns it into E_REF_SYNTAX / E_REF_SCHEME); Parse itself
// never fails.
type Ref struct {
	Scheme Scheme
	// Body is everything after the first colon ("db_host",
	// "gopass://acme/hub/db?holder=…").
	Body string

	Target     string
	Options    []Option
	Modifier   Modifier
	Fallback   string
	Alternates []SecretTarget
	Problem    string
	// ProblemCode is the error code Problem maps to (E_REF_SYNTAX or
	// E_REF_SCHEME).
	ProblemCode errs.Code
}

// String renders the reference as written.
func (r Ref) String() string { return open + string(r.Scheme) + schemeSep + r.Body + closer }

// PartKind says what a Part is.
type PartKind string

// Part kinds.
const (
	// PartText is literal text, including a "${" that never closes or has
	// no scheme (left alone, never an error).
	PartText PartKind = "text"
	// PartRef is a reference.
	PartRef PartKind = "ref"
	// PartEscaped is "$${…}": text that expands to "${…}" literally.
	PartEscaped PartKind = "escaped"
)

// Part is one piece of a parsed text.
//
// Shapes by Kind: PartText — Raw is the text, Ref zero; PartRef — Raw is
// "${scheme:body}", Ref filled; PartEscaped — Raw is "$${…}", Ref zero.
type Part struct {
	Kind PartKind
	Raw  string
	Ref  Ref
}

// Parse splits text into literal text, references and escapes.
func Parse(text string) []Part {
	var out []Part
	lit := strings.Builder{}
	flush := func() {
		if lit.Len() > 0 {
			out = append(out, Part{Kind: PartText, Raw: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(text); {
		if strings.HasPrefix(text[i:], escapeLit) {
			if end := strings.Index(text[i+len(escapeLit):], closer); end >= 0 {
				flush()
				n := len(escapeLit) + end + len(closer)
				out = append(out, Part{Kind: PartEscaped, Raw: text[i : i+n]})
				i += n
				continue
			}
		}
		if strings.HasPrefix(text[i:], open) {
			if ref, n, ok := parseRef(text[i:]); ok {
				flush()
				out = append(out, Part{Kind: PartRef, Raw: text[i : i+n], Ref: ref})
				i += n
				continue
			}
		}
		lit.WriteByte(text[i])
		i++
	}
	flush()
	return out
}

// parseRef reads "${scheme:body}" at the start of s; n is its length.
// Braces nest: a "${" inside the body opens a level, so a fallback may hold
// references (${var:a:-${var:b}}, SECRETS_AND_REFERENCES 8.3).
func parseRef(s string) (Ref, int, bool) {
	end := closingBrace(s)
	if end < 0 {
		return Ref{}, 0, false
	}
	inner := s[len(open):end]
	scheme, body, ok := strings.Cut(inner, schemeSep)
	if !ok || !validScheme(scheme) {
		return Ref{}, 0, false
	}
	r := Ref{Scheme: Scheme(scheme), Body: body}
	readURI(&r)
	return r, end + len(closer), true
}

// closingBrace is the index of the "}" that closes the "${" at s[0], or -1.
func closingBrace(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch {
		case strings.HasPrefix(s[i:], open):
			depth++
			i++
		case s[i] == '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// validScheme: a lowercase word, letters, digits and dashes, starting with
// a letter (URI scheme shape; "aws-secrets" style plugin names fit).
func validScheme(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// References returns the references in text, in order (lint, the secret
// check of lore move, BATCH 5).
func References(text string) []Ref {
	var out []Ref
	for _, p := range Parse(text) {
		if p.Kind == PartRef {
			out = append(out, p.Ref)
		}
	}
	return out
}
