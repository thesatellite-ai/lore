package orgresolve

import (
	"errors"
	"strings"
)

// Kind classifies a refusal so the CLI can map it to its error code without
// matching message text.
type Kind int

const (
	// KindAmbiguous: two entries match at the same rule (E_ORG_AMBIGUOUS).
	KindAmbiguous Kind = iota + 1
	// KindDisagrees: an explicit choice contradicts a store link
	// (E_ORG_DISAGREES), raised by Result.CheckWrite.
	KindDisagrees
	// KindLinkUnknown: links could not be checked and the write has no
	// explicit --to (E_LINK_UNKNOWN), raised by Result.CheckWrite.
	KindLinkUnknown
	// KindUnknownEntry: LORE_ORG names an entry that does not exist.
	KindUnknownEntry
)

// Error is an org-resolution refusal.
type Error struct {
	Kind       Kind
	Message    string
	Hint       string
	Candidates []string
}

// Error implements error. Candidates are part of the message: a refusal is
// only useful if it names the choices.
func (e *Error) Error() string {
	if len(e.Candidates) == 0 {
		return e.Message
	}
	return e.Message + " (candidates: " + strings.Join(e.Candidates, ", ") + ")"
}

// KindOf returns err's Kind, or 0 when err is not an *Error.
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return 0
}
