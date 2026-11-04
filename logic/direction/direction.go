// Package direction is LORE_TEAM_SPEC F15's direction rule for pointers
// between rows: a pointer must never reveal a hidden row. The pointer alone
// (its target id) tells every reader of the pointing row that the target
// exists, so a pointer may only be stored where every reader of it could
// read the target too.
//
//	from \ to   client   internal   personal
//	client      yes      NO         NO
//	internal    yes      yes        NO
//	personal    yes      yes        yes (the same person's store only)
//
// Layer L1: pure. The caller classifies the two stores (ClassOf) and asks
// Check before writing anything.
//
// Invariant: fail closed. A store this package cannot place is a client
// store (the widest audience), and the repo store counts as client-class
// until wire can tell an org's own repo from a customer's (Attached.Client
// is unset for the repo store), so "client → internal" is refused there.
package direction

import (
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Class is who can read a store's rows.
type Class string

// The three classes of F15.
const (
	// Client is a store the client can read (a client repo).
	Client Class = "client"
	// Internal is an org store (lore-store.yaml visibility internal or
	// shareable: either way the client does not read it directly).
	Internal Class = "internal"
	// Personal is one person's own store (`me`, `me/<folder>`).
	Personal Class = "personal"
)

// Store names ClassOf knows (engine/attach reserved names; repeated here
// so this layer imports nothing above model).
const (
	repoStore     = "repo"
	personalStore = "me"
	folderSep     = "/"
)

// ClassOf places a store by its attached name. client is the engine's
// Attached.Client flag for that store. The repo store is client-class
// whatever the flag says (fail closed, see the package doc); the personal
// store and its folders are personal; any other attached store is internal
// unless flagged client.
func ClassOf(name string, client bool) Class {
	switch {
	case name == "" || name == repoStore:
		return Client
	case name == personalStore || strings.HasPrefix(name, personalStore+folderSep):
		return Personal
	case client:
		return Client
	}
	return Internal
}

// Check applies the direction rule to a pointer stored in a row of class
// from, naming a row of class to. A refusal is E_LEAK_BLOCKED naming the
// direction; it carries no text of either row. Two personal stores are
// always the same person's (only one `me` is ever attached), so personal →
// personal is allowed.
func Check(from, to Class) error {
	switch {
	case to == Client:
		return nil
	case from == Client:
		return refuse("a client row cannot point at "+article(to)+" row (the pointer alone reveals it)", hintFor(to))
	case to == Personal && from != Personal:
		return refuse("a shared row cannot point at a personal row (the pointer alone reveals it)", hintPersonal)
	}
	return nil
}

// Refusal hints: where the pointer may be written instead.
const (
	hintInternal = "write the pointer in an internal store instead (--to <org store>), whose readers can see the target"
	hintPersonal = "write the pointer in your personal store instead (--to me): only you can see the target"
)

// hintFor is the hint for a pointer at a row of class to.
func hintFor(to Class) string {
	if to == Personal {
		return hintPersonal
	}
	return hintInternal
}

// refuse builds the F15 refusal.
func refuse(msg, hint string) error {
	return errs.New(errs.LeakBlocked, "F15: "+msg+"; nothing written").WithHint(hint)
}

// article is "an internal" / "a personal".
func article(c Class) string {
	if c == Internal {
		return "an " + string(c)
	}
	return "a " + string(c)
}
