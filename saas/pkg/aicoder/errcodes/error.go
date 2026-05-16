package errcodes

import (
	"errors"
	"fmt"
)

// CLIError is the canonical error envelope for lore commands.
// It carries:
//
//   - Code:    a stable identifier from the registry (e.g., E_DB_LOCKED).
//   - Message: a human-readable description of what went wrong (mutable
//     wording across versions; consumers should NOT regex-match).
//   - Hint:    actionable next step ("run X" / "set Y env"); optional.
//   - DocURL:  pointer to docs/errors/<code>.md (built from Code).
//   - Wrapped: underlying cause (preserved via errors.Unwrap).
//
// Invariant: the Code field is the stable contract. Code values may be added
// freely; removal/rename is a breaking change requiring a major bump.
type CLIError struct {
	Code    Code
	Message string
	Hint    string
	DocURL  string
	Wrapped error
}

// New builds a CLIError with the given code and message. Use the chainable
// WithHint / WithCause / WithDocURL methods to add detail.
func New(code Code, msg string) *CLIError {
	return &CLIError{
		Code:    code,
		Message: msg,
		DocURL:  "https://aicoder.dev/errors/" + string(code),
	}
}

// Newf is like New but formats the message via fmt.Sprintf.
func Newf(code Code, format string, args ...any) *CLIError {
	return New(code, fmt.Sprintf(format, args...))
}

// WithHint attaches an actionable next-step hint.
func (e *CLIError) WithHint(hint string) *CLIError {
	e.Hint = hint
	return e
}

// WithCause attaches a wrapped underlying error (for errors.Unwrap chains).
func (e *CLIError) WithCause(err error) *CLIError {
	e.Wrapped = err
	return e
}

// WithDocURL overrides the default doc URL.
func (e *CLIError) WithDocURL(url string) *CLIError {
	e.DocURL = url
	return e
}

// Error implements the error interface.
//
// Format: "[<code>] <message>" with cause appended if present.
// Hint is intentionally NOT included in Error() — it's a UX field surfaced by
// the CLI's renderer (plain or JSON), not by callers using errors.Error().
func (e *CLIError) Error() string {
	if e.Wrapped != nil {
		return fmt.Sprintf("[%s] %s: %s", e.Code, e.Message, e.Wrapped.Error())
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// Unwrap returns the wrapped cause for use with errors.Is / errors.As.
func (e *CLIError) Unwrap() error {
	return e.Wrapped
}

// Is allows errors.Is(err, &CLIError{Code: SomeCode}) for code-level matching.
// Two CLIErrors are Is-equal if their Codes match (regardless of message).
func (e *CLIError) Is(target error) bool {
	t, ok := target.(*CLIError)
	if !ok {
		return false
	}
	return e.Code == t.Code
}

// IsCode returns true if err (or any wrapped error in its chain) is a CLIError
// with the given code. Convenience for the common pattern.
func IsCode(err error, code Code) bool {
	var cli *CLIError
	if errors.As(err, &cli) {
		return cli.Code == code
	}
	return false
}

// JSONEnvelope is the wire format for --json error output.
type JSONEnvelope struct {
	Error JSONError `json:"error"`
}

// JSONError is the inner structure of JSONEnvelope.
type JSONError struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	DocURL  string `json:"doc_url,omitempty"`
}

// AsJSON returns the JSON-marshalable form of this error.
func (e *CLIError) AsJSON() JSONEnvelope {
	return JSONEnvelope{
		Error: JSONError{
			Code:    e.Code,
			Message: e.Message,
			Hint:    e.Hint,
			DocURL:  e.DocURL,
		},
	}
}

// Plain returns the human-readable multiline rendering for terminal output.
//
//	[E_DB_LOCKED] Database busy: another aicoder process is writing.
//	Hint: try again in a moment, or kill it: kill 12345
//	More: https://aicoder.dev/errors/E_DB_LOCKED
func (e *CLIError) Plain() string {
	out := fmt.Sprintf("[%s] %s", e.Code, e.Message)
	if e.Wrapped != nil {
		out += "\n  Cause: " + e.Wrapped.Error()
	}
	if e.Hint != "" {
		out += "\n  Hint: " + e.Hint
	}
	if e.DocURL != "" {
		out += "\n  More: " + e.DocURL
	}
	return out
}
