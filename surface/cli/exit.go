package cli

import (
	"errors"
	"strconv"

	"github.com/thesatellite-ai/lore/model/errs"
)

// ExitUsage is the exit status of a cobra usage error (unknown flag,
// unknown command): CLI_REFERENCE 1.5 puts these in exit 1 with E_INTERNAL,
// the generic class.
const ExitUsage = errs.ExitGeneric

// exitError carries the process exit status of a command that already
// printed its error. It is returned from RunE so the binary's main can call
// ExitCode without printing the error a second time (every generated
// command sets SilenceErrors).
type exitError struct {
	code int
	// cause is the original error, kept for errors.Is/As in tests and for
	// callers that log it.
	cause error
}

func (e *exitError) Error() string {
	if e.cause != nil {
		return e.cause.Error()
	}
	return "exit status " + strconv.Itoa(e.code)
}

func (e *exitError) Unwrap() error { return e.cause }

// ExitCode maps the error returned by cobra's Execute to the process exit
// status: 0 for nil, the class exit for an error a generated command
// returned, ExitUsage for anything else (cobra's own usage errors, an old
// command mounted beside the new ones).
//
// Invariant: never 0 for a non-nil error, so a failure can never look like
// success to a shell or an agent.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exitError
	if errors.As(err, &ee) && ee.code != 0 {
		return ee.code
	}
	return ExitUsage
}
