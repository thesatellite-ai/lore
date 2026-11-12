package engine

// handedit.go — a row a person edited by hand in its file (plain files
// round, brief 2.1): the pick-up in wire reads the file and hands it here
// with the row as the last commit holds it. It goes through Update's one
// write path (schema, references, secret scan, policy) as a patch of the
// fields that differ, and is re-stamped like every edit (new HLC,
// updated_by = the caller, updated_at), so the hand edit wins a later
// merge against older changes exactly as `lore task edit` would.
//
//	base (last commit) + hand (the file) ─▶ patch = changed fields, a field
//	   gone from the file = cleared ─▶ updateFrom(base, patch)
//
// What lore writes itself in a row file (the envelope: _hlc, created_by,
// updated_by/at, _o) is never taken from the hand edit: the write stamps
// it anew. Metadata (meta) changes by hand are refused: the registry
// checks of `lore meta set` cannot run on a raw value.

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// PickUp writes the hand edit hand of the row base (as the last commit of
// store src holds it) through the write path. A file whose fields equal
// base changes nothing and writes nothing (Written.Row is base).
func (e *Engine) PickUp(ctx context.Context, src store.Source, base, hand row.Row) (Written, error) {
	if hand.Table != base.Table || hand.ID != base.ID {
		return Written{}, errs.New(errs.InvalidInput, "the file holds row "+hand.ID.String()+" of table "+string(hand.Table)+", but its name says "+base.ID.String()+" of table "+string(base.Table)).
			WithHint("put the id and table back as the file name says (or restore it with git checkout -- <file>)")
	}
	if hand.Deleted && !base.Deleted {
		del := "the delete command of its kind"
		n := tableNoun(base.Table)
		if def, derr := e.tableDef(base.Table); derr == nil && def.Noun != "" {
			n = def.Noun
		}
		if n != "" {
			del = "`lore " + n + " delete " + base.ID.String() + "`" // the row's own noun, never a fixed one (B2-3)
		}
		return Written{}, errs.New(errs.InvalidInput, "the file marks "+base.ID.String()+" deleted by hand").
			WithHint("delete the file instead (lore moves the row to the trash), or run " + del)
	}
	if !sameMeta(base.Meta, hand.Meta) {
		return Written{}, errs.New(errs.InvalidInput, "the metadata (meta) of "+base.ID.String()+" was changed by hand; lore cannot check a raw metadata value").
			WithHint("put meta back as it was and change it with lore meta set " + base.ID.String() + " <key> <value>")
	}
	def, err := e.tableDef(base.Table)
	if err != nil {
		return Written{}, err
	}
	was, now := retype(def, base.Fields), retype(def, hand.Fields)
	patch := map[string]value.Value{}
	for k, v := range now {
		if old, held := was[k]; !held || !value.Equal(old, v) {
			patch[k] = v
		}
	}
	for k := range was {
		if _, held := now[k]; !held {
			patch[k] = value.Null{}
		}
	}
	if len(patch) == 0 {
		return Written{Row: base}, nil
	}
	return e.updateFrom(ctx, src, base, patch)
}

// PickUpChanged names the fields PickUp would write for hand over base,
// sorted ("" entries never): what the pick-up prints.
func PickUpChanged(base, hand row.Row) []string {
	var out []string
	for k, v := range hand.Fields {
		if old, held := base.Fields[k]; !held || !value.Equal(old, v) {
			out = append(out, k)
		}
	}
	for k := range base.Fields {
		if _, held := hand.Fields[k]; !held {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// sameMeta reports whether two rows' metadata maps hold the same values.
func sameMeta[K comparable](a, b map[K]value.JSON) bool {
	if len(a) != len(b) {
		return false
	}
	for _, k := range slices.Collect(maps.Keys(a)) {
		w, ok := b[k]
		if !ok || !value.Equal(a[k], w) {
			return false
		}
	}
	return true
}

// RetypeFields is the fields of a row read from a file, each field the
// definition declares decoded as its declared kind (a file reads a json
// column's array as a list): what a check of a raw row file validates
// (the store clone's pre-commit hook).
func RetypeFields(def schema.TableDef, fields map[string]value.Value) map[string]value.Value {
	return retype(def, fields)
}

// tableNoun is the command noun of a table whose noun is its plain
// singular (memories → memory, tasks → task); "" for a table whose noun
// is spelled otherwise (an underscore, a declared schema.Noun), so a hint
// never names a command that does not exist.
func tableNoun(t row.Table) string {
	name := string(t)
	switch {
	case strings.Contains(name, "_"):
		return ""
	case strings.HasSuffix(name, "ies"):
		return strings.TrimSuffix(name, "ies") + "y"
	case strings.HasSuffix(name, "s"):
		return strings.TrimSuffix(name, "s")
	}
	return ""
}
