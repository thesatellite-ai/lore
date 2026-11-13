package op

import (
	"fmt"
	"slices"
	"strings"
)

// Named args everywhere (U-4, owner decision 2026-11-13): every command's
// canonical form is named flags, `lore task add --title "x"`, `lore task
// show --id tsk_…`. A positional argument stays a quiet alias so v0.1.17
// habits and scripts keep working, but help, usage, docs and hints show
// only the flag.
//
// namePositionals gives each top-level positional field its flag: the
// field's `flag` tag, else its json name in kebab case (`new_name` →
// --new-name). A positional that only stands in for another field's flag
// (`argof:"body"`: memory add's text is --body) gets none, nor does a
// repeated group (task split's sub-task titles, argof --title there). Called once per Define, on the input
// fields only (a group item's head stays positional: it is the group).
func namePositionals(op string, in []FieldInfo) {
	spell := map[string]string{} // CLI spelling → field
	for _, f := range in {
		for _, s := range f.Spellings() {
			spell[s] = f.Name
		}
	}
	for i := range in {
		f := &in[i]
		if f.Arg == NoArg {
			continue
		}
		where := fmt.Sprintf("op.Define(%s): field %s", op, f.Name)
		if f.ArgOf != "" {
			if _, ok := spell[f.ArgOf]; !ok {
				panic(tagError{where + `: argof names --` + f.ArgOf + `, which the input has no flag for`})
			}
			continue
		}
		if f.Group { // items with their own flags: no single flag holds them
			continue
		}
		if f.Flag == "" {
			f.Flag = strings.ReplaceAll(f.Name, "_", "-")
			if !flagName.MatchString(f.Flag) {
				panic(tagError{where + ": json name " + f.Name + " makes no flag name; give it a flag tag"})
			}
			if prev, dup := spell[f.Flag]; dup {
				panic(tagError{where + ": its flag --" + f.Flag + " is also used by " + prev + "; give it a flag tag"})
			}
			spell[f.Flag] = f.Name
		}
		f.Named = true
	}
}

// NamedArgs returns the positional fields whose canonical form is a flag,
// in argument order: what a usage line shows as `--flag <value>`.
func NamedArgs(fields []FieldInfo) []FieldInfo {
	var out []FieldInfo
	for _, f := range fields {
		if f.Named {
			out = append(out, f)
		}
	}
	slices.SortFunc(out, func(a, b FieldInfo) int { return a.Arg - b.Arg })
	return out
}
