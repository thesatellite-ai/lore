// Package lookup holds the one rule for an id that names an EXISTING row
// (show, edit, archive, delete, done, --supersedes, --after, --ref, tag
// and comment targets, --created-by, …): a value that cannot be such an
// id (malformed, another table's prefix, all zeros) answers E_NOT_FOUND,
// exactly like a well-formed id no row has. That is v0.1.17's behaviour
// (it looked every value up and found nothing), and it keeps the exit
// code a script checks the same whatever kind of wrong id it passed.
// E_INVALID_ID stays for an id supplied for a NEW row only.
package lookup

import "github.com/thesatellite-ai/lore/model/errs"

// BadID is the error for a lookup value that is not a usable id: msg says
// what was expected (e.g. `rule: "x" is not a rule id (rul_…)`). Code:
// E_NOT_FOUND. cause (the parse error, nil allowed) is not wrapped: its
// own E_INVALID_ID code would show in the message and contradict the
// answer.
func BadID(msg string, cause error) *errs.Error {
	_ = cause
	return errs.New(errs.NotFound, msg)
}
