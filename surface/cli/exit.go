package cli

import (
	"errors"
	"strconv"

	"github.com/thesatellite-ai/lore/model/errs"
)

// ExitUsage is the exit status of a cobra usage error (unknown flag,
// unknown shorthand flag, missing flag value, unknown command).
//
// FLAG2: a missing or invalid argument is an input error, so a usage error
// exits 20 (class bad_input, E_INVALID_INPUT) like every other bad input;
// exit 1 is kept for unexpected internal errors only. This supersedes
// CLI_REFERENCE 1.5's "usage errors exit 1".
const ExitUsage = errs.ExitBadInput

// UsageCode is the error code a usage error is printed with (FLAG2).
const UsageCode = errs.InvalidInput

// ExitUnknown is ExitCode's answer for an error no generated command
// returned (an old command mounted beside the new ones): the generic exit,
// because nothing says what class it is.
const ExitUnknown = errs.ExitGeneric

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
// returned (usage errors included: usageError returns them as ExitUsage),
// ExitUnknown for anything else (an old command mounted beside the new
// ones).
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
	return ExitUnknown
}
