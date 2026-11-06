// Package lookup holds the one rule for an id that names an EXISTING row
// (show, edit, archive, delete, done, --supersedes, --after, --ref, tag
// and comment targets, --created-by, …): a value that cannot be such an
// id (malformed, another table's prefix, all zeros) answers E_INVALID_ID
// (exit 20); only a well-formed id that no row has is E_NOT_FOUND (exit
// 27). Owner decision BATCH2 #11 (B-ERRS-1, CLI_REFERENCE 5): a
// malformed id is never reported as not found, so a script can tell a
// typo from a row that is gone. (8b930f17 had mapped it to E_NOT_FOUND
// for v0.1.17 parity; reverted.)
package lookup

import "github.com/thesatellite-ai/lore/model/errs"

// BadID is the error for a lookup value that is not a usable id: msg says
// what was expected (e.g. `rule: "x" is not a rule id (rul_…)`). Code:
// E_INVALID_ID. cause (the parse error, nil allowed) is not wrapped: msg
// already says what was expected, and a parser cause can carry another
// code (ids answers E_NOT_FOUND for a wrong prefix) that errs.Has would
// then also report.
func BadID(msg string, cause error) *errs.Error {
	_ = cause
	return errs.New(errs.InvalidID, msg)
}
