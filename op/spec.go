// Package op is the single definition of every lore operation
// (STORE_SERVER_SPEC 9). The CLI commands and the REST routes are built from
// these specs at startup, the CLI reference and the OpenAPI file are written
// from them, and every surface calls a Runner, so all surfaces take the same
// inputs, give the same errors and apply the same checks.
//
// Type safety: an operation is Define[In, Out](spec, run) with real structs.
// Surfaces only ever hold the type-erased Entry, and they never hold an
// `any`: inputs reach the struct through a Decoder (JSON bytes or CLI args)
// and results leave as a Result that marshals itself.
//
// Layering: op imports model and schema only. The engine (L4) implements
// Runner and imports op; op never imports the engine.
package op

import (
	"regexp"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Permission is what the caller needs on the target store.
type Permission string

// Permissions.
const (
	PermRead  Permission = "read"
	PermWrite Permission = "write"
	PermAdmin Permission = "admin"
)

// Permissions lists every permission.
func Permissions() []Permission { return []Permission{PermRead, PermWrite, PermAdmin} }

// Access says whether an operation changes rows. The Run path uses it to
// decide whether the write steps run (scanner, policy, confirm, stamping,
// backend write, audit); a read skips them. Permission is separate: it is
// what the caller must hold, and a write access needs write or admin.
type Access string

// Accesses.
const (
	AccessRead  Access = "read"
	AccessWrite Access = "write"
)

// Accesses lists every access.
func Accesses() []Access { return []Access{AccessRead, AccessWrite} }

// ConfirmRule says when an operation asks first.
type ConfirmRule struct {
	// Always: the operation always asks (purge, trash empty).
	Always bool `json:"always"`
	// When names a bool input field (its json name) whose true value makes
	// it ask ("reveal" on secrets.show, "force" on move).
	When string `json:"when"`
	// PersonOnlyEligible: the team policy agents.person_only may list it.
	PersonOnlyEligible bool `json:"person_only_eligible"`
	// MayAsk: nothing is asked up front, but the write path may answer
	// E_NEEDS_CONFIRM (a secret finding under secrets.on_detect: ask), so
	// surfaces must still offer the yes answer (--yes, REST "yes"). Run never
	// pre-confirms on MayAsk alone.
	MayAsk bool `json:"may_ask"`
}

// CanAsk reports whether the operation can answer E_NEEDS_CONFIRM at all,
// up front or from its write path. Every surface decides from this one
// method whether to offer --yes / "yes", so CLI, REST and OpenAPI agree.
func (c ConfirmRule) CanAsk() bool { return c.Always || c.When != "" || c.MayAsk }

// Example is shown in --help and the docs, and run by the docs test, so an
// example can never drift from what the operation does.
type Example struct {
	Title string `json:"title"`
	CLI   string `json:"cli"`
	Want  string `json:"want"`
}

// Spec is the type-erased description of one operation.
type Spec struct {
	// Name is "noun.verb" (or "noun.sub.verb"): snake_case parts, at least
	// two. The CLI path and the REST route derive from it (route.go).
	Name    string
	Summary string
	// Errors lists every E_ code the operation can return; each must be
	// registered in model/errs (checked by NewRegistry).
	Errors     []errs.Code
	Permission Permission
	Access     Access
	Confirm    ConfirmRule
	Examples   []Example
	// Hidden marks internal plumbing (merge-driver): CLI only, no REST
	// route, not in the public docs.
	Hidden bool
	// RESTNoun overrides the REST collection segment when the default
	// plural of the last noun is wrong ("policy" → "policy" instead of
	// "policies"). Empty means the default.
	RESTNoun string
}

// namePart is one snake_case segment of an operation name.
var namePart = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// minNameParts is "noun.verb".
const minNameParts = 2

// nameSep separates the parts of an operation name.
const nameSep = "."

// parts splits a valid name; ok is false for an invalid one.
func parts(name string) ([]string, bool) {
	p := strings.Split(name, nameSep)
	if len(p) < minNameParts {
		return nil, false
	}
	for _, s := range p {
		if !namePart.MatchString(s) {
			return nil, false
		}
	}
	return p, true
}

// ValidName reports whether name is a valid operation name.
func ValidName(name string) bool {
	_, ok := parts(name)
	return ok
}

// Texter is the optional human form of an operation's output. When an Out
// type implements it, the CLI prints Text() instead of the generic
// `name: value` dump, so a migrated command keeps its v0.1.17 human output
// line for line (the --json and REST forms are unaffected). Text must end
// with a newline when it prints anything, and never contain a secret.
type Texter interface {
	Text() string
}
