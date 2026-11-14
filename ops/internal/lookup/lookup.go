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

import (
	"errors"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// BadID is the error for a lookup value that is not a usable id: msg says
// what was expected (e.g. `rule: "x" is not a rule id (rul_…)`). Code:
// E_INVALID_ID, with errs.InvalidIDHint as the next step. cause (the
// parse error, nil allowed) is not wrapped: msg
// already says what was expected, and a parser cause can carry another
// code (ids answers E_NOT_FOUND for a wrong prefix) that errs.Has would
// then also report. The hint (errs.InvalidIDHint) names where a person finds a
// real id, so every lookup refusal carries its next step.
func BadID(msg string, cause error) *errs.Error {
	// devco:L-9: an id of the right prefix and length whose hex tail is
	// not one lore makes (msn_000…) is named as malformed, never as a
	// wrong kind: "not a msn_ id" contradicted the hint's own shape.
	var me *ids.MalformedError
	if errors.As(cause, &me) && wantsPrefix(msg, me.Prefix) {
		return errs.New(errs.InvalidID, malformedMsg(msg)+": "+me.Plain).
			WithHint("check you copied the whole id; `lore row show --id <id>` or the list of that kind shows the real ids")
	}
	return errs.New(errs.InvalidID, msg).WithHint(errs.InvalidIDHint)
}

// wantsPrefix reports whether msg's expected kind (the text after its
// "not a") admits prefix p: it names p_ or names no prefix at all ("a row
// id"). An id of another kind keeps the wrong-kind message.
func wantsPrefix(msg string, p ids.Prefix) bool {
	i := strings.LastIndex(msg, "not a")
	if i < 0 {
		return true
	}
	want := msg[i:]
	return strings.Contains(want, string(p)+"_") || !strings.Contains(want, "_")
}

// malformedMsg turns a caller's "x is not a <kind> id" into "x is a
// malformed <kind> id" (also "not an …" and a leading "not a …").
func malformedMsg(msg string) string {
	for _, r := range []struct{ from, to string }{
		{" is not an ", " is a malformed "}, {" is not a ", " is a malformed "}, {": not an ", ": a malformed "}, {": not a ", ": a malformed "},
	} {
		if strings.Contains(msg, r.from) {
			return strings.Replace(msg, r.from, r.to, 1)
		}
	}
	return msg + " (a malformed id)"
}

// ListHint is the next step after a bad id of noun: the list that shows
// the real ones (`lore memory list`). A wrong-kind or mistyped id is the
// usual cause, and the person needs to find the right id, not re-read the
// message.
func ListHint(noun string) string {
	return "`lore " + noun + " list` shows the " + noun + " ids"
}
