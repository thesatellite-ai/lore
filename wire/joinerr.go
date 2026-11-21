package wire

import "errors"

// joinFlat is errors.Join with the parts of every joined error among errs
// spliced in, so a refusal joined with the cleanup errors of a failed open
// stays one part of one join, however many cleanups wrap it. nil parts are
// dropped; no non-nil part is nil, one part with nothing joined is itself.
// Why (devco:D-840): open's fail joined the refusal with the agent key's
// settle and then again with the close, a join inside a join, and
// replaceErr (untouched_reason.go), which swaps the refusal for its exact
// form, saw no refusal among the outer join's parts: a write with no --to
// in an unlinked own repo lost its "--to <org>/<folder>" for the folder a
// resource names and printed the "--project <folder>" placeholder again.
func joinFlat(errs ...error) error {
	var parts []error
	for _, err := range errs {
		parts = appendFlat(parts, err)
	}
	switch len(parts) {
	case 0:
		return nil
	case 1:
		return parts[0]
	}
	return errors.Join(parts...)
}

// appendFlat is parts with err added, or with the parts of err when err is
// a join (errors.Join's own, recursively); a nil err adds nothing.
func appendFlat(parts []error, err error) []error {
	if err == nil {
		return parts
	}
	j, ok := err.(interface{ Unwrap() []error }) //nolint:errorlint // only a join itself, never one wrapped in another error
	if !ok {
		return append(parts, err)
	}
	for _, p := range j.Unwrap() {
		parts = appendFlat(parts, p)
	}
	return parts
}
