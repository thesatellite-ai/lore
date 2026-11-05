package refpoint

import (
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// Point is where expanded text goes (SECRETS_AND_REFERENCES 12).
type Point string

// The expansion points this package serves.
const (
	// Show is `lore show --expand`.
	Show Point = "show"
	// Render is a committed render in our own repo or store (LORE.md,
	// LORE.full.md, folder files).
	Render Point = "render"
	// Client is a render in a client repo (13.1).
	Client Point = "client"
	// Private is the uncommitted private render (~/.lore/render/…, CFG6):
	// it also fills private team variables and res/row of internal
	// stores; personal-store targets stay references (BATCH 16, SQ23).
	Private Point = "private"
)

// Mask replaces a secret, or a value the scanner flags, in show (2.7).
const Mask = "••••••"

// Redacted is what every secret reference becomes rendered for a client
// (SQ11): the manager, entry path and holder are internal facts.
const Redacted = "${secret:redacted}"

// State says what happened to one reference.
type State string

// States of a Used reference.
const (
	StateExpanded State = "expanded" // filled in
	StateKept     State = "kept"     // left as written by this point's rule
	StateMasked   State = "masked"   // show: a secret, or a secret-like value
	StateRedacted State = "redacted" // client render: ${secret:redacted}
	StateDangling State = "dangling" // the target does not exist (8.6)
)

// Target is what the caller knows about the row a value came from.
type Target struct {
	// Personal: the row lives in a personal store (`me`).
	Personal bool
	// Shareable: a variable's shareable flag.
	Shareable bool
	// Reaches: the row's store may reach this output (render: the repo or
	// a shareable store; client: the client repo only).
	Reaches bool
}

// Options are one expansion point's inputs.
type Options struct {
	Point Point
	// Resolver fills var/res/row references (wire's resolver); nil fills
	// nothing.
	Resolver refs.Resolver
	// Locate places the row a value came from; nil places nothing, so
	// render keeps every reference (fail closed).
	Locate func(id ids.AnyID) Target
	// Env reads the process environment (show only; nil = empty).
	Env func(name string) (string, bool)
	// Scanner scans the expanded text (13.2); nil skips the scan.
	Scanner scan.Scanner
	// Meta reads a metadata value of the row being expanded, by key name
	// (${meta:<key>}, OV13a); nil fills no meta reference. MetaOf builds
	// it from a row.
	Meta func(key string) (MetaValue, bool)
}

// MetaValue is one metadata value as an expansion sees it.
type MetaValue struct {
	Text      string
	Sensitive bool
}

// Used is one reference as the output reports it (--json "refs").
type Used struct {
	Ref    string `json:"ref"`
	Value  string `json:"value,omitempty"`
	Source string `json:"source,omitempty"`
	State  State  `json:"state"`
	Note   string `json:"note,omitempty"`
}

// Result is one field's expansion.
type Result struct {
	Text string
	Refs []Used
}

// Expand returns text with the references this point may fill filled
// in. field names the text for the scanner and the refusal.
func Expand(o Options, field, text string) (Result, error) {
	x := &expansion{o: o}
	in := x.rewrite(text)
	exp := refs.Expander{Resolver: gate{x: x}, Strict: o.Point == Show}
	res, err := exp.ExpandFull(in)
	if err != nil {
		return Result{}, err
	}
	for _, d := range res.Dangling {
		x.note(Used{Ref: d, State: StateDangling, Note: "no such target; left as written"})
	}
	out := res.Text
	if out != text && o.Scanner != nil {
		if out, err = x.screen(field, text, out); err != nil {
			return Result{}, err
		}
	}
	return Result{Text: out, Refs: x.used}, nil
}

// expansion is one Expand call's state.
type expansion struct {
	o    Options
	used []Used
}

func (x *expansion) note(u Used) {
	if !slices.ContainsFunc(x.used, func(v Used) bool { return v.Ref == u.Ref }) {
		x.used = append(x.used, u)
	}
}

// rewrite turns the secret and env references of s into what this point
// prints, before (or, for a value, after) the resolver runs.
func (x *expansion) rewrite(s string) string {
	var b strings.Builder
	for _, p := range refs.Parse(s) {
		if p.Kind != refs.PartRef {
			b.WriteString(p.Raw)
			continue
		}
		switch p.Ref.Scheme {
		case refs.SchemeSecret:
			b.WriteString(x.secret(p.Raw))
		case refs.SchemeEnv:
			b.WriteString(x.env(p.Ref, p.Raw))
		case refs.SchemeMeta:
			b.WriteString(x.meta(p.Ref, p.Raw))
		default:
			b.WriteString(p.Raw)
		}
	}
	return b.String()
}

// secret: never a value here (D18): masked in show, redacted for a
// client, the reference text (holder and label shown) in our own render.
func (x *expansion) secret(raw string) string {
	switch x.o.Point {
	case Show:
		x.note(Used{Ref: raw, State: StateMasked, Note: "not revealed (add --reveal)"})
		return Mask
	case Client:
		x.note(Used{Ref: raw, State: StateRedacted, Note: "rendered for a client (SQ11)"})
		return Redacted
	case Render, Private:
	}
	x.note(Used{Ref: raw, State: StateKept, Note: "secret references stay as written (SQ11)"})
	return raw
}

// env: show only, read now; a credential-like name is masked (12, 8.4).
// Render never reads env (the output must be the same on every machine).
func (x *expansion) env(r refs.Ref, raw string) string {
	if x.o.Point != Show || x.o.Env == nil {
		return raw
	}
	name := r.Target
	if name == "" {
		name = r.Body
	}
	v, ok := x.o.Env(name)
	if !ok {
		x.note(Used{Ref: raw, State: StateDangling, Note: "not set in this environment"})
		return raw
	}
	if CredentialName(name) || (x.o.Scanner != nil && len(x.o.Scanner.Scan(string(refs.SchemeEnv), v)) > 0) {
		x.note(Used{Ref: raw, State: StateMasked, Note: "secret-like; add --reveal"})
		return Mask
	}
	x.note(Used{Ref: raw, Value: v, Source: "environment", State: StateExpanded})
	return escape(v)
}

// meta: the holding row's own metadata (OV13a; DECISION P2-CTX-4). Show
// and the private render fill it, a sensitive value masked; a committed or
// client render keeps the reference as written (metadata may be scoped or
// internal, and the file must be the same for every reader).
func (x *expansion) meta(r refs.Ref, raw string) string {
	if (x.o.Point != Show && x.o.Point != Private) || x.o.Meta == nil {
		return raw
	}
	name := r.Target
	if name == "" {
		name = r.Body
	}
	v, ok := x.o.Meta(name)
	if !ok {
		x.note(Used{Ref: raw, State: StateDangling, Note: "the row has no such metadata key"})
		return raw
	}
	if v.Sensitive {
		x.note(Used{Ref: raw, State: StateMasked, Note: "sensitive metadata value"})
		return Mask
	}
	x.note(Used{Ref: raw, Value: v.Text, Source: "metadata", State: StateExpanded})
	return escape(v.Text)
}

// escape makes s literal text for the expander ("${" → "$${").
func escape(s string) string { return strings.ReplaceAll(s, "${", "$${") }

// credentialWords mark an environment name that is treated as a secret
// even when no rule fires (12: *_TOKEN, *_SECRET, *_PASSWORD, *_KEY, …).
var credentialWords = []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "KEY", "CREDENTIAL", "PRIVATE"}

// CredentialName reports a credential-looking environment name.
func CredentialName(name string) bool {
	up := strings.ToUpper(name)
	return slices.ContainsFunc(credentialWords, func(w string) bool { return strings.Contains(up, w) })
}

// gate wraps the caller's Resolver with this point's rules.
type gate struct{ x *expansion }

// Resolve implements refs.Resolver.
func (g gate) Resolve(r refs.Ref) (refs.Resolved, error) {
	x := g.x
	if x.o.Resolver == nil {
		return refs.Resolved{}, refs.ErrUnresolved
	}
	got, err := x.o.Resolver.Resolve(r)
	if err != nil {
		return got, err
	}
	if why := x.blocked(r.Scheme, got.ID); why != "" {
		x.note(Used{Ref: r.String(), Source: idText(got.ID), State: StateKept, Note: why})
		// Final: the reference text goes out exactly as written, not its
		// :- fallback (a kept reference is not a dangling one).
		return refs.Resolved{Text: r.String(), ID: got.ID, Final: true}, nil
	}
	text := x.rewrite(got.Text) // a secret_ref var brings a secret reference (11.5)
	x.note(Used{Ref: r.String(), Value: text, Source: idText(got.ID), State: StateExpanded})
	return refs.Resolved{Text: text, ID: got.ID, Final: got.Final}, nil
}

// blocked is why this point keeps a reference to the row id ("" = fill it).
func (x *expansion) blocked(s refs.Scheme, id ids.AnyID) string {
	if x.o.Point == Show {
		return "" // show fills personal variables too (SQ23)
	}
	var t Target // nil Locate or no id: nothing reaches a render (fail closed)
	if x.o.Locate != nil && !id.IsZero() {
		t = x.o.Locate(id)
	} else if x.o.Point == Private {
		return "unplaced target: kept as written (fail closed)"
	}
	if x.o.Point == Private {
		// The private file is this machine's local output (CONTEXT_BUDGET
		// 6.13, SECRETS 12): every non-personal var/res/row this machine
		// sees is filled; personal-store targets never (BATCH 16, SQ23).
		if t.Personal {
			return "personal-store target: stays a reference in every render (BATCH 16, SQ23)"
		}
		return ""
	}
	switch s {
	case refs.SchemeVar:
		switch {
		case t.Personal:
			return "personal-store variable: stays a reference in every render (BATCH 16, SQ23)"
		case !t.Shareable:
			return "private variable: committed files fill only shareable variables (BATCH 1, 13.1)"
		}
	case refs.SchemeRes, refs.SchemeRow:
		if t.Personal || !t.Reaches {
			return "its home is not visible to the readers of this file (13.1)"
		}
	case refs.SchemeEnv, refs.SchemeMeta, refs.SchemeSecret:
		// never reach the resolver (refs.Expander, rewrite)
	}
	return ""
}

func idText(id ids.AnyID) string {
	if id.IsZero() {
		return ""
	}
	return id.String()
}

// screen scans the expanded text (13.2). Findings the stored text already
// had passed the write scan (or its allow entries) and are left alone;
// a new one came in through a reference: show masks it, render refuses.
func (x *expansion) screen(field, stored, out string) (string, error) {
	had := map[uint32]bool{}
	for _, f := range x.o.Scanner.Scan(field, stored) {
		had[f.MatchHash] = true
	}
	var fresh []scan.Finding
	for _, f := range x.o.Scanner.Scan(field, out) {
		if !had[f.MatchHash] {
			fresh = append(fresh, f)
		}
	}
	if len(fresh) == 0 {
		return out, nil
	}
	if x.o.Point != Show {
		return "", x.refusal(field, fresh)
	}
	slices.SortFunc(fresh, func(a, b scan.Finding) int { return b.Offset - a.Offset })
	end := len(out) + 1
	for _, f := range fresh {
		if f.Offset < 0 || f.Offset+f.Length > len(out) || f.Offset+f.Length > end {
			continue // overlaps one already masked
		}
		out = out[:f.Offset] + Mask + out[f.Offset+f.Length:]
		end = f.Offset
	}
	for i := range x.used {
		if x.used[i].State == StateExpanded && len(x.o.Scanner.Scan(field, x.used[i].Value)) > 0 {
			x.used[i].Value, x.used[i].State, x.used[i].Note = Mask, StateMasked, "the value looks like a secret; add --reveal"
		}
	}
	return out, nil
}

// refusal names the references whose values hold the finding (13.2).
func (x *expansion) refusal(field string, fs []scan.Finding) error {
	var by []string
	for _, u := range x.used {
		if u.State == StateExpanded && len(x.o.Scanner.Scan(field, u.Value)) > 0 {
			by = append(by, u.Ref)
		}
	}
	if len(by) == 0 {
		for _, u := range x.used {
			if u.State == StateExpanded {
				by = append(by, u.Ref)
			}
		}
	}
	rules := make([]string, 0, len(fs))
	for _, f := range fs {
		if !slices.Contains(rules, f.RuleID) {
			rules = append(rules, f.RuleID)
		}
	}
	return errs.New(errs.SecretDetected, field+": the expanded text holds a secret ("+strings.Join(rules, ", ")+
		") brought in by "+strings.Join(by, ", ")+"; nothing written").
		WithHint("keep the value in your vault and point at it with ${secret:…} (a secret_ref variable), or mark the variable sensitive")
}
