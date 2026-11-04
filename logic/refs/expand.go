package refs

import (
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// Expansion (SECRETS_AND_REFERENCES 8.3-8.6):
//
//	text ─▶ Parse ─▶ each ref ─▶ expandable? ─no─▶ as written (secret, env, unknown, malformed)
//	                                  │yes
//	                     depth ≤ 8, resolutions ≤ 256 (D14, D17)
//	                                  ▼
//	                       Resolver.Resolve ─NotFound─▶ :- fallback | :? E_REF_REQUIRED | as written (dangling)
//	                                  │
//	                     cycle check on scheme+id (E_REF_CYCLE, chain printed)
//	                                  ▼
//	                     Final? as is : expand the value again (chaining)
//
// Output over 64 KB is E_REF_LIMIT. Lenient (the default, used by the
// cache and lore_expand) leaves a reference that hit a cycle, the depth or
// the caps, or a :? message, as written and lists it as dangling: one bad
// variable never fails a cache refresh. Strict returns the error (show
// --expand, a later batch).

// Resolved is what a Resolver found for one reference.
type Resolved struct {
	// Text replaces the reference in the expanded text.
	Text string
	// ID is the row the value came from (var_, res_, mky_ or the row id),
	// recorded in row_refs so a change to that row refreshes this one; it
	// is also the cycle key (an alias and its name count as one, 8.3).
	ID ids.AnyID
	// Final marks text that is never expanded again (a revealed secret,
	// 8.3); every other value is expanded like any text (chaining).
	Final bool
}

// Resolver fills in one expandable reference. It returns an error matching
// errs.NotFound when the target does not exist: the reference then stays
// as written (dangling). Any other error stops the expansion.
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
	// Strict returns E_REF_CYCLE / E_REF_DEPTH / E_REF_LIMIT /
	// E_REF_REQUIRED instead of leaving the reference as written.
	Strict bool
}

// Result is one expansion.
type Result struct {
	Text string
	// IDs are the sorted, distinct ids of every row a filled-in value came
	// from, chained ones included, so a change to any of them refreshes
	// the text through row_refs.
	IDs []string
	// Dangling lists the references left as written because they did not
	// resolve (8.6), in order, each once.
	Dangling []string
}

// Expand returns text with every expandable reference filled in, escapes
// turned into their literal "${…}", and the ids of the rows the values
// came from (see Result).
func (e Expander) Expand(text string) (expanded string, refIDs []string, err error) {
	res, err := e.ExpandFull(text)
	if err != nil {
		return "", nil, err
	}
	return res.Text, res.IDs, nil
}

// ExpandFull is Expand with the dangling list.
func (e Expander) ExpandFull(text string) (Result, error) {
	x := &run{e: e, seen: map[string]bool{}}
	out, err := x.text(text, nil)
	if err == nil && len(out) > MaxExpanded {
		err = errs.New(errs.RefLimit, "expanded text is over 64 KB")
	}
	if err != nil {
		if e.Strict || !refErr(err) {
			return Result{}, err
		}
		out = text // lenient: the text stays as stored
	}
	slices.Sort(x.ids)
	return Result{Text: out, IDs: x.ids, Dangling: x.dangling}, nil
}

// refErr reports the reference errors lenient expansion absorbs.
func refErr(err error) bool {
	return errs.Has(err, errs.RefCycle) || errs.Has(err, errs.RefDepth) ||
		errs.Has(err, errs.RefLimit) || errs.Has(err, errs.RefRequired)
}

// run is one expansion's counters.
type run struct {
	e        Expander
	n        int
	ids      []string
	seen     map[string]bool
	dangling []string
}

func (x *run) text(s string, chain []string) (string, error) {
	var b strings.Builder
	for _, p := range Parse(s) {
		switch p.Kind {
		case PartText:
			b.WriteString(p.Raw)
		case PartEscaped:
			b.WriteString(p.Raw[1:]) // "$${x}" → "${x}"
		case PartRef:
			got, err := x.ref(p, chain)
			if err != nil {
				if x.e.Strict || len(chain) > 0 || !refErr(err) {
					return "", err
				}
				x.dangle(p.Raw) // lenient, top level: this one stays as written
				got = p.Raw
			}
			b.WriteString(got)
		}
		if b.Len() > MaxExpanded {
			return "", errs.New(errs.RefLimit, "expanded text is over 64 KB")
		}
	}
	return b.String(), nil
}

func (x *run) ref(p Part, chain []string) (string, error) {
	r := p.Ref
	if !r.Scheme.Expandable() || x.e.Resolver == nil || r.Problem != "" {
		return p.Raw, nil
	}
	if len(chain) >= MaxDepth {
		return "", errs.New(errs.RefDepth, "references nest deeper than 8: "+strings.Join(chain, " -> "))
	}
	if x.n++; x.n > MaxResolutions {
		return "", errs.New(errs.RefLimit, "more than 256 references in one text")
	}
	got, err := x.e.Resolver.Resolve(r)
	switch {
	case errs.Has(err, errs.NotFound):
		return x.unresolved(p, chain)
	case err != nil:
		return "", err
	}
	key := string(r.Scheme) + ":" + r.Target
	if !got.ID.IsZero() {
		key = string(r.Scheme) + ":" + got.ID.String()
	}
	if slices.Contains(chain, key) {
		return "", errs.New(errs.RefCycle, "references form a loop: "+strings.Join(append(slices.Clone(chain), key), " -> "))
	}
	if !got.ID.IsZero() && !x.seen[got.ID.String()] {
		x.seen[got.ID.String()] = true
		x.ids = append(x.ids, got.ID.String())
	}
	if got.Final {
		return got.Text, nil
	}
	return x.text(got.Text, append(slices.Clone(chain), key))
}

// unresolved applies the modifier of a dangling reference (8.6).
func (x *run) unresolved(p Part, chain []string) (string, error) {
	switch p.Ref.Modifier {
	case ModFallback:
		return x.text(p.Ref.Fallback, chain)
	case ModRequired:
		return "", errs.New(errs.RefRequired, p.Ref.Fallback).WithHint(p.Raw + " did not resolve")
	}
	x.dangle(p.Raw)
	return p.Raw, nil
}

func (x *run) dangle(raw string) {
	if !slices.Contains(x.dangling, raw) {
		x.dangling = append(x.dangling, raw)
	}
}
