package wire

// malformedid.go — a row id that is no lore id is refused as one, before
// any store or git lookup (devco D-634). From the home folder `lore show
// --id mem_bogus` and `memory show --id mem_bogus` answered "the folder is
// not inside a git work tree", which sent the person to the wrong fix: the
// id was wrong, not the place.
//
// Inside a git work tree the op answers in its own order and words (devco
// D-676): its kind-specific refusal (`"mem_nope" is not a memory id
// (mem_…)`), its own code (`actor delete <id>` is E_REFUSED whatever the
// id) and the same error object over REST. The early check exists only to
// beat the place lookup, which answers first only outside a work tree.

import (
	"context"
	"strconv"
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
// id at all, a folder inside a git work tree where the op refuses the id
// itself). The op's own lookup still refuses an id that is well formed but
// in no store.
func malformedIDErr(ctx context.Context, dir string, e op.Entry, d op.Decoder) error {
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
	if insideWorkTree(ctx, dir) {
		return nil // the op refuses it in its own words (D-676)
	}
	return malformedIDMessage(e.Spec().Name, v)
}

// malformedIDMessage is the refusal of malformed id v given to op name:
// the kind-specific words the op itself uses (`memory: "mem_nope" is not a
// memory id`) followed by the shape, so the same id reads the same from
// home and inside a repo (D-676); an op of no one kind (show, context)
// names a lore id.
func malformedIDMessage(name, v string) error {
	p := v[:idPrefixLen]
	shape := p + "_<32 hex>"
	hint := "copy the id from the list or search it came from (lore search --query <words> finds a row by its words)"
	path := op.CLIPath(name)
	if len(path) < 2 || path[0] == "row" {
		return errs.New(errs.InvalidID, strconv.Quote(v)+" is not a valid lore id: a lore id is its kind's prefix, an underscore and 32 hex digits ("+shape+"), as lore prints it").
			WithHint(hint)
	}
	noun := strings.Join(path[:len(path)-1], " ")
	an := "a "
	if strings.ContainsRune("aeiou", rune(noun[0])) {
		an = "an "
	}
	return errs.New(errs.InvalidID, noun+": "+strconv.Quote(v)+" is not "+an+noun+" id ("+p+"_…): "+an+noun+" id is "+p+"_ and 32 hex digits ("+shape+"), as lore prints it").
		WithHint("`lore " + noun + " list` shows the " + noun + " ids; " + hint)
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
