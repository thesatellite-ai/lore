// Package errs is the one table of lore's error codes: every E_ code with its
// class, process exit status and HTTP status (STORE_SERVER_SPEC 9.5,
// CLI_REFERENCE 1.5 and 5).
//
// Why it exists: the CLI, the REST surface and agents all branch on the code
// and the exit status, never on message text. Keeping code → class → exit →
// HTTP in one place means a surface can never invent its own number, and a
// test (class_test.go) proves every code's exit and HTTP status match its
// class.
//
// Invariant: codes and exit numbers are a stable contract once released:
// never renumbered, never reused for another meaning, only added.
package errs

import "net/http"

// Class groups codes that a caller handles the same way. The class decides the
// exit status range and the HTTP status (spec 9.5), so a new code only picks
// its class.
type Class string

// The closed set of classes. Adding one is a contract change: it needs a new
// exit number in CLI_REFERENCE 1.5 and a row in classTable.
const (
	// ClassGeneric holds only E_INTERNAL: a real internal failure (500).
	// DOCS O3 + O7-O9: E_INTERNAL is the only generic code, so no input or
	// state error can ever answer HTTP 500 or exit 1.
	ClassGeneric           Class = "generic"
	ClassCoordination      Class = "coordination"
	ClassGuard             Class = "guard"
	ClassBadInput          Class = "bad_input"
	ClassAmbiguous         Class = "ambiguous"
	ClassDefinition        Class = "definition"
	ClassRefused           Class = "refused"
	ClassMigrate           Class = "migrate"
	ClassUpgrade           Class = "upgrade"
	ClassNeedsConfirmation Class = "needs_confirmation"
	ClassNotFound          Class = "not_found"
	// ClassUnauthorized is REST-only (no token, bad token; DOCS O5). The
	// CLI never produces it; it still has its own exit, ExitUnauthorized,
	// so a client that maps a REST error back to an exit status gets a
	// distinct number.
	ClassUnauthorized Class = "unauthorized"
)

// Classes lists every class in table order; tests and docs iterate it.
func Classes() []Class {
	return []Class{
		ClassGeneric, ClassCoordination, ClassGuard, ClassBadInput, ClassAmbiguous,
		ClassDefinition, ClassRefused, ClassMigrate, ClassUpgrade,
		ClassNeedsConfirmation, ClassNotFound, ClassUnauthorized,
	}
}

// Exit statuses per class (CLI_REFERENCE 1.5). Coordination codes carry their
// own number inside [ExitCoordinationMin, ExitCoordinationMax]; every other
// class has exactly one number. RES-OQ5 + BATCH 7: not_found has its own
// exit, 27 (the next free number), distinct from "not visible here" (22).
// ds:def id=team-exit-codes-eqpyx54h owner=@khanakia stability=stable desc="exit statuses per error class"
const (
	ExitGeneric           = 1
	ExitCoordinationMin   = 10
	ExitCoordinationMax   = 18
	ExitGuard             = 19
	ExitBadInput          = 20
	ExitAmbiguous         = 21
	ExitDefinition        = 22
	ExitRefused           = 23
	ExitMigrate           = 24
	ExitUpgrade           = 25
	ExitNeedsConfirmation = 26
	ExitNotFound          = 27
	// ExitUnauthorized is the next free number after every class of
	// CLI_REFERENCE 1.5 (no clash with 1, 10-27).
	ExitUnauthorized = 28
)

// ExitOffline is the coordination exit that means "the remote could not be
// reached"; it is the one coordination number that maps to HTTP 503 instead
// of 409 (spec 9.5).
const ExitOffline = 15

// classInfo is one row of the spec 9.5 table.
type classInfo struct {
	// exit is the class's single exit status; 0 for coordination, whose
	// codes carry their own number.
	exit int
	http int
}

// classTable is the spec 9.5 table. Read-only after package init.
var classTable = map[Class]classInfo{
	ClassGeneric:           {exit: ExitGeneric, http: http.StatusInternalServerError},
	ClassCoordination:      {exit: 0, http: http.StatusConflict},
	ClassGuard:             {exit: ExitGuard, http: http.StatusForbidden},
	ClassBadInput:          {exit: ExitBadInput, http: http.StatusBadRequest},
	ClassAmbiguous:         {exit: ExitAmbiguous, http: http.StatusConflict},
	ClassDefinition:        {exit: ExitDefinition, http: http.StatusUnprocessableEntity},
	ClassRefused:           {exit: ExitRefused, http: http.StatusConflict},
	ClassMigrate:           {exit: ExitMigrate, http: http.StatusConflict},
	ClassUpgrade:           {exit: ExitUpgrade, http: http.StatusUpgradeRequired},
	ClassNeedsConfirmation: {exit: ExitNeedsConfirmation, http: http.StatusPreconditionRequired},
	ClassNotFound:          {exit: ExitNotFound, http: http.StatusNotFound},
	ClassUnauthorized:      {exit: ExitUnauthorized, http: http.StatusUnauthorized},
}

// Valid reports whether c is one of Classes.
func (c Class) Valid() bool {
	_, ok := classTable[c]
	return ok
}

// HTTPFor returns the HTTP status for a code of class c with exit status
// exit. Only coordination looks at exit (15 → 503); an unknown class is 500
// so a missing row fails loudly as a server error, never as a success.
func HTTPFor(c Class, exit int) int {
	info, ok := classTable[c]
	if !ok {
		return http.StatusInternalServerError
	}
	if c == ClassCoordination && exit == ExitOffline {
		return http.StatusServiceUnavailable
	}
	return info.http
}

// classExit returns the class's single exit status (0 for coordination).
func classExit(c Class) int { return classTable[c].exit }
