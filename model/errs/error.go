package errs

import (
	"errors"
	"fmt"
)

// Error is lore's one error type: a registered Code plus text for people.
//
// Why it exists: every surface (CLI, REST, later MCP) prints the same object
// for the same failure, so the code, exit and HTTP status come from the code
// table and never from the surface.
//
// Invariant: Message and Hint never contain a secret or a sensitive value;
// they are printed verbatim to terminals, logs and HTTP clients.
type Error struct {
	Code    Code
	Message string
	// Hint is one line telling a person what to do next; may be empty.
	Hint string
	// Cause is the wrapped lower-level error, kept for errors.Is/As chains;
	// it is not part of the JSON shape (it may hold paths or driver text).
	Cause error
}

// New builds an Error. It does not check that code is registered: DefOf
// falls back to the generic class for an unknown code, and a test walks
// every constant of this package.
func New(code Code, msg string) *Error { return &Error{Code: code, Message: msg} }

// Wrap builds an Error that keeps cause in the chain.
func Wrap(code Code, msg string, cause error) *Error {
	return &Error{Code: code, Message: msg, Cause: cause}
}

// WithHint returns a copy of e with the hint set, so a shared sentinel is
// never mutated.
func (e *Error) WithHint(hint string) *Error {
	c := *e
	c.Hint = hint
	return &c
}

// Error renders "[E_CODE] message: cause".
func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %s", e.Code, e.Message, e.Cause.Error())
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// Unwrap exposes Cause to errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.Cause }

// Is matches another *Error by code alone, so errors.Is(err, store.ErrStale)
// holds for any E_STALE error whatever its message.
func (e *Error) Is(target error) bool {
	var t *Error
	if !errors.As(target, &t) {
		return false
	}
	return t.Code == e.Code
}

// Def returns the registry entry of e's code (generic for an unknown code).
func (e *Error) Def() Def { return DefOf(e.Code) }

// CodeOf returns the code of the first *Error in err's chain, Internal for
// any other non-nil error, and "" for nil. Surfaces use it to pick the exit
// status of errors that did not come from lore code (fail closed: unknown
// failures are internal errors, never success).
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return Internal
}

// Has reports whether err's chain holds an *Error with code.
func Has(err error, code Code) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// Envelope is the JSON error object of spec 9.4 (the CLI's --json error and
// every REST error body). Field names are a stable contract: only added,
// never renamed.
type Envelope struct {
	Error Body `json:"error"`
}

// Body is the inner object of Envelope.
type Body struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Exit    int    `json:"exit"`
	Class   Class  `json:"class"`
}

// EnvelopeOf builds the JSON shape for any error: an *Error in the chain
// gives its own fields, anything else becomes E_INTERNAL with the error text.
func EnvelopeOf(err error) Envelope {
	var e *Error
	if !errors.As(err, &e) {
		e = Wrap(Internal, "internal error", err)
	}
	d := e.Def()
	return Envelope{Error: Body{Code: e.Code, Message: e.Message, Hint: e.Hint, Exit: d.Exit, Class: d.Class}}
}
