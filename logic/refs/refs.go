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
	"github.com/thesatellite-ai/lore/model/ids"
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
type Ref struct {
	Scheme Scheme
	// Body is everything after the first colon ("db_host",
	// "gopass://acme/hub/db?holder=…").
	Body string
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
func parseRef(s string) (Ref, int, bool) {
	end := strings.Index(s, closer)
	if end < 0 {
		return Ref{}, 0, false
	}
	inner := s[len(open):end]
	scheme, body, ok := strings.Cut(inner, schemeSep)
	if !ok || !validScheme(scheme) {
		return Ref{}, 0, false
	}
	return Ref{Scheme: Scheme(scheme), Body: body}, end + len(closer), true
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

// Resolved is what a Resolver found for one reference.
type Resolved struct {
	// Text replaces the reference in the expanded text.
	Text string
	// ID is the row the value came from (var_, res_, mky_ or the row id),
	// recorded in row_refs so a change to that row refreshes this one.
	ID ids.AnyID
}

// Resolver fills in one expandable reference. It returns an error matching
// ErrUnresolved (errors.Is) when the target does not exist: the reference
// then stays as written. Any other error stops the expansion.
type Resolver interface {
	Resolve(ref Ref) (Resolved, error)
}

// ErrUnresolved is the Resolver's "no such target" answer (E_NOT_FOUND).
var ErrUnresolved error = errs.New(errs.NotFound, "refs: reference target not found")

// Expander fills in the expandable references of a text through Resolver.
// It satisfies the SQLite cache's Expander port (Expand(text) (expanded,
// refIDs, err)) structurally.
type Expander struct {
	Resolver Resolver
}

// Expand returns text with every expandable reference filled in, escapes
// turned into their literal "${…}", and the sorted, distinct ids of the
// rows the filled-in values came from. Secrets, env and unknown schemes
// stay exactly as written; an unresolved reference stays as written and
// adds no id.
func (e Expander) Expand(text string) (expanded string, refIDs []string, err error) {
	var b strings.Builder
	seen := map[string]bool{}
	for _, p := range Parse(text) {
		switch p.Kind {
		case PartText:
			b.WriteString(p.Raw)
		case PartEscaped:
			b.WriteString(p.Raw[1:]) // "$${x}" → "${x}"
		case PartRef:
			got, ok, rerr := e.resolve(p.Ref)
			if rerr != nil {
				return "", nil, rerr
			}
			if !ok {
				b.WriteString(p.Raw)
				continue
			}
			b.WriteString(got.Text)
			if !got.ID.IsZero() && !seen[got.ID.String()] {
				seen[got.ID.String()] = true
				refIDs = append(refIDs, got.ID.String())
			}
		}
	}
	slices.Sort(refIDs)
	return b.String(), refIDs, nil
}

// resolve asks the Resolver for an expandable reference; ok is false when
// the reference must stay as written.
func (e Expander) resolve(r Ref) (Resolved, bool, error) {
	if !r.Scheme.Expandable() || e.Resolver == nil {
		return Resolved{}, false, nil
	}
	got, err := e.Resolver.Resolve(r)
	switch {
	case errs.Has(err, errs.NotFound):
		return Resolved{}, false, nil
	case err != nil:
		return Resolved{}, false, err
	}
	return got, true, nil
}
