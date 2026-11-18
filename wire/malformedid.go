package wire

// malformedid.go — a row id that is no lore id is refused as one, before
// any store or git lookup (devco D-634). From the home folder `lore show
// --id mem_bogus` and `memory show --id mem_bogus` answered "the folder is
// not inside a git work tree", which sent the person to the wrong fix: the
// id was wrong, not the place.

import (
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
)

// idFieldTakesOther are words of an id field's desc that say it takes
// something besides a full id (a name, a machine id prefix, a key, an
// @handle, a number): such a value is never refused for its shape here.
var idFieldTakesOther = []string{"name", "prefix", "key", "handle", "number", "sha", "reference"}

// malformedIDErr is the E_INVALID_ID of e's `--id` when it starts like a
// lore id (a registered prefix and "_") but is not one; nil otherwise (no
// id field, a field that takes names too, a valid id, a value that is no
// id at all). The op's own lookup still refuses an id that is well formed
// but in no store.
func malformedIDErr(e op.Entry, d op.Decoder) error {
	var field op.FieldInfo
	found := false
	for _, f := range e.InputFields() {
		if f.Name == idField {
			field, found = f, true
			break
		}
	}
	if !found || takesOther(field.Desc) {
		return nil
	}
	in, err := e.Decode(d)
	if err != nil {
		return nil // the usual path reports the decode error
	}
	var v string
	for _, tv := range in.Texts() {
		if tv.Field == idField {
			v = strings.TrimSpace(tv.Text)
		}
	}
	if len(v) <= idPrefixLen || v[idPrefixLen] != '_' || !ids.Prefix(v[:idPrefixLen]).Registered() {
		return nil
	}
	if hex32(v[idPrefixLen+1:]) {
		// the shape of an id: whether lore made it (a UUIDv7) is the op's
		// own check, as before (devco:L-9 wording), so a placeholder id
		// keeps its op's refusal
		return nil
	}
	p := v[:idPrefixLen]
	return errs.New(errs.InvalidID, v+" is not a valid lore id: a lore id is its kind's prefix, an underscore and 32 hex digits ("+p+"_<32 hex>), as lore prints it").
		WithHint("copy the id from the list or search it came from (lore search --query <words> finds a row by its words)")
}

// hex32 reports whether s is 32 hex digits, the tail of a lore id (the
// case is the op's own check).
func hex32(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

// idPrefixLen is the length of a lore id's kind prefix (mem, rul, tsk).
const idPrefixLen = 3

// takesOther reports whether an id field's desc says it takes more than a
// full id (idFieldTakesOther).
func takesOther(desc string) bool {
	d := strings.ToLower(desc)
	for _, w := range idFieldTakesOther {
		if strings.Contains(d, w) {
			return true
		}
	}
	return false
}
