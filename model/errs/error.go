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
	// Retryable marks this failure as one that the same (or the Next)
	// command can get past without a fix by a person. It is ORed with the
	// class default (coordination and needs_confirmation are retryable), so
	// a caller can only add retryability, never hide it.
	Retryable bool
	// NeedsPerson marks a failure a person should answer or act on; for an
	// agent it means "ask your human", never "you are refused". ORed with
	// the class default (needs_confirmation).
	NeedsPerson bool
	// Next is the command that moves things forward, ready to run; nil
	// when there is none.
	Next *Next
	// Question is the exact question to put to the person; set only for
	// E_NEEDS_CONFIRM.
	Question string
	// DocURL points at the page explaining the code; may be empty.
	DocURL string
}

// Next is the `next` object of the --json error contract (CLI_REFERENCE
// 1.4): a command plus its arguments, never a shell string, so an agent can
// run it without quoting mistakes.
type Next struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
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

// WithNext returns a copy of e with the next command set.
func (e *Error) WithNext(command string, args ...string) *Error {
	c := *e
	c.Next = &Next{Command: command, Args: append([]string(nil), args...)}
	return &c
}

// WithQuestion returns a copy of e with the question for the person set
// (E_NEEDS_CONFIRM only, by convention; not enforced so a test can probe it).
func (e *Error) WithQuestion(q string) *Error {
	c := *e
	c.Question = q
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
//
// Field set and names follow CLI_REFERENCE 1.4 (decision OQ5): fields are
// only ever added, never renamed or removed.
type Body struct {
	Code        Code   `json:"code"`
	Message     string `json:"message"`
	Hint        string `json:"hint,omitempty"`
	Exit        int    `json:"exit"`
	Class       Class  `json:"class"`
	Retryable   bool   `json:"retryable"`
	NeedsPerson bool   `json:"needs_person"`
	Next        *Next  `json:"next,omitempty"`
	Question    string `json:"question,omitempty"`
	DocURL      string `json:"doc_url,omitempty"`
}

// classRetryable is the retryable default per class: coordination failures
// (contention, offline) and confirmations clear on a rerun without a fix.
func classRetryable(c Class) bool {
	return c == ClassCoordination || c == ClassNeedsConfirmation
}

// classNeedsPerson is the needs_person default per class: a confirmation
// always needs a person's answer.
func classNeedsPerson(c Class) bool { return c == ClassNeedsConfirmation }

// EnvelopeOf builds the JSON shape for any error: an *Error in the chain
// gives its own fields, anything else becomes E_INTERNAL with the error text.
func EnvelopeOf(err error) Envelope {
	var e *Error
	if !errors.As(err, &e) {
		e = Wrap(Internal, "internal error", err)
	}
	d := e.Def()
	return Envelope{Error: Body{
		Code: e.Code, Message: e.Message, Hint: e.Hint, Exit: d.Exit, Class: d.Class,
		Retryable:   e.Retryable || classRetryable(d.Class),
		NeedsPerson: e.NeedsPerson || classNeedsPerson(d.Class),
		Next:        e.Next, Question: e.Question, DocURL: e.DocURL,
	}}
}
