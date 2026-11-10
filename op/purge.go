package op

import (
	"reflect"
	"strings"
)

// PurgeFlags are a delete's --purge / --confirm (DELETION 2.5, 3.12, DQ2,
// DQ8, FA-DEL-1), one shared input for every noun's delete operation
// (P3-DEL-1 ONE-PATH follow-up): a delete input embeds it, so the CLI flags,
// the `lore api` JSON fields ("purge", "confirm") and the docs come from the
// one field set, and the engine's Run reads them from any input
// (Input.Purge). No operation handles them itself:
//
//	purge, no confirm  ─► E_NEEDS_CONFIRM before anything runs (DQ2: never asks, --yes never purges)
//	purge + confirm    ─► the engine tombstones the group; "purged for good" line
//	confirm alone      ─► answers the delete's question like --yes, plus a hint at lore trash purge
//
// Why an embedded struct and not two fields per input: the meaning is the
// engine's, not the noun's, so it is defined once (like engine.LiveFlags);
// Input.Purge recognises the embedded type itself, so an operation with an
// unrelated `confirm` field (trash purge) is never read as a delete.
type PurgeFlags struct {
	Purge   bool `json:"purge,omitempty" flag:"purge" desc:"skip the trash: remove for good, only a tombstone stays (needs --confirm)"`
	Confirm bool `json:"confirm,omitempty" flag:"confirm" desc:"confirms a --purge (or a delete under policy trash.purge_on_delete); without them, v0.1.17's spelling of --yes"`
}

// purgeFlagsType is the embedded type Input.Purge looks for.
var purgeFlagsType = reflect.TypeFor[PurgeFlags]()

// Purge returns the input's embedded PurgeFlags; ok is false when the
// input struct does not embed them (an operation that is not a delete).
func (i Input) Purge() (flags PurgeFlags, ok bool) {
	if !i.v.IsValid() || i.v.Kind() != reflect.Struct {
		return PurgeFlags{}, false
	}
	t := i.v.Type()
	for n := range t.NumField() {
		sf := t.Field(n)
		if !sf.Anonymous || sf.Type != purgeFlagsType {
			continue
		}
		fv := i.v.Field(n)
		return PurgeFlags{
			Purge:   fv.FieldByName("Purge").Bool(),
			Confirm: fv.FieldByName("Confirm").Bool(),
		}, true
	}
	return PurgeFlags{}, false
}

// Positionals returns the text of the input's positional arguments in
// argument order (the row a delete names), skipping empty ones; a refusal
// names what the command would have touched with them.
func (i Input) Positionals() []string {
	var out []string
	if !i.v.IsValid() {
		return nil
	}
	byArg := map[int]string{}
	maxArg := -1
	for _, f := range i.fields {
		if f.Arg == NoArg || f.Group {
			continue
		}
		fv := i.v.FieldByIndex(f.index)
		switch f.Type {
		case TypeString:
			byArg[f.Arg] = fv.String()
		case TypeStrings:
			var s []string
			for j := range fv.Len() {
				s = append(s, fv.Index(j).String())
			}
			byArg[f.Arg] = strings.Join(s, " ")
		default:
			continue
		}
		maxArg = max(maxArg, f.Arg)
	}
	for a := 0; a <= maxArg; a++ {
		if s := byArg[a]; s != "" {
			out = append(out, s)
		}
	}
	return out
}
