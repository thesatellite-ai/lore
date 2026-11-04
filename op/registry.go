package op

import (
	"context"
	"errors"
	"slices"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// Registry is an immutable, validated set of operations, built once at
// startup by the binary (NewRegistry) and handed to every surface. It is a
// value, never a package global.
type Registry struct {
	byName map[string]Entry
	names  []string
}

// NewRegistry validates entries together and returns every problem at once:
// duplicate names, unknown error codes, a closed-set field holding a value
// outside its set, a Confirm.When that names no bool input, two
// operations on one REST route, and the word rules of checkWords (XQ8 --by
// is an actor, XQ9 no root-level import/export).
func NewRegistry(entries ...Entry) (*Registry, error) {
	r := &Registry{byName: make(map[string]Entry, len(entries))}
	var problems []error
	routes := map[string]string{}
	for _, e := range entries {
		s := e.Spec()
		if _, dup := r.byName[s.Name]; dup {
			problems = append(problems, errors.New(s.Name+": defined twice"))
			continue
		}
		r.byName[s.Name] = e
		r.names = append(r.names, s.Name)
		problems = append(problems, checkSpec(e)...)
		problems = append(problems, checkWords(e)...)
		if rt, ok := RouteOf(s, e.InputFields()); ok {
			key := rt.Method + " " + rt.Path
			if other, clash := routes[key]; clash {
				problems = append(problems, errors.New(s.Name+": route "+key+" also used by "+other))
			}
			routes[key] = s.Name
		}
	}
	slices.Sort(r.names)
	if len(problems) > 0 {
		return nil, errs.Wrap(errs.ConfigInvalid, "op: invalid operation registry", errors.Join(problems...))
	}
	return r, nil
}

// checkSpec validates one entry's spec.
func checkSpec(e Entry) []error {
	s := e.Spec()
	var p []error
	if !slices.Contains(Permissions(), s.Permission) {
		p = append(p, errors.New(s.Name+": unknown permission "+string(s.Permission)))
	}
	if !slices.Contains(Accesses(), s.Access) {
		p = append(p, errors.New(s.Name+": unknown access "+string(s.Access)))
	}
	if s.Access == AccessWrite && s.Permission == PermRead {
		p = append(p, errors.New(s.Name+": a write operation needs write or admin permission"))
	}
	if s.Summary == "" {
		p = append(p, errors.New(s.Name+": no summary"))
	}
	for _, c := range s.Errors {
		if !c.Known() {
			p = append(p, errors.New(s.Name+": unknown error code "+string(c)))
		}
	}
	if s.Confirm.When != "" {
		found := false
		for _, f := range e.InputFields() {
			if f.Name == s.Confirm.When && f.Type == TypeBool {
				found = true
			}
		}
		if !found {
			p = append(p, errors.New(s.Name+": confirm.when "+s.Confirm.When+" is not a bool input field"))
		}
	}
	return p
}

// Lookup returns the operation named name.
func (r *Registry) Lookup(name string) (Entry, bool) {
	e, ok := r.byName[name]
	return e, ok
}

// Entries returns every operation sorted by name.
func (r *Registry) Entries() []Entry {
	out := make([]Entry, len(r.names))
	for i, n := range r.names {
		out[i] = r.byName[n]
	}
	return out
}

// Caller is who runs an operation, as a surface knows it.
type Caller struct {
	// Actor is the person (or the agent's own actor) the write is stamped
	// with.
	Actor ids.ID[ids.Actor]
	// Agent is the agent's name when an agent runs the command (detected
	// on the CLI, X-Lore-Agent on REST); empty for a person.
	Agent string
	// AgentFixed: the surface decided Agent itself (REST: X-Lore-Agent or
	// none), so Run never fills it from the process's own detection (the
	// `lore api` server's environment is not the HTTP client's).
	AgentFixed bool
	// Interactive: a person can answer a question now (a terminal). A
	// non-interactive caller without Yes gets E_NEEDS_CONFIRM.
	Interactive bool
	// Yes: the caller already confirmed (--yes, "yes": true).
	Yes bool
}

// Runner is the one Run path every surface calls (spec 9.2): decode,
// resolve the store, check permission, scan text, check policy, confirm,
// invoke, stamp, write, audit. The engine implements it.
type Runner interface {
	Run(ctx context.Context, e Entry, d Decoder, c Caller) (Result, error)
}
