package op

// group.go — the repeated group input (P3-TASK-1, TASK 6.3 per-child flags).
//
// A repeated group is a list of small records typed on one command line:
//
//	lore task split <id> --priority low "a" --assign @x --priority high "b"
//	                     └ shared ───┘  └ item 1 ─────────────────┘ └ item 2
//
// Go shape: the input's last positional is a `[]S` with `rest:"true"`; S
// has exactly one positional field (arg 0, the item's head: a string) and
// otherwise only flag fields. Every flag of S must also be a flag of the
// outer input with the same type: that outer field is the shared value
// for every item, the item field overrides it for one item. The op merges
// the two (it knows what "unset" means for its field); the decoder only
// routes values.
//
// Routing (ArgsDecoder, needs FlagPos from the CLI): each positional from
// the group's index on starts a new item; a value of an item flag given
// after the n-th item and before the next one belongs to item n; one given
// before the first item goes to the outer (shared) field. Flags that are
// not item flags always go to the outer input. JSON (REST, --json input)
// needs no routing: the field is an array of objects.

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"

	"github.com/thesatellite-ai/lore/model/errs"
)

// groupItemFields reads and checks the item struct of a repeated group.
func groupItemFields(op, where string, t reflect.Type) []FieldInfo {
	fields := fieldsOf(op, t, dirInput)
	heads := 0
	for _, f := range fields {
		switch {
		case f.Arg == 0 && !f.Rest && (f.Type == TypeString || f.Type == TypeText):
			heads++
		case f.Arg != NoArg:
			panic(tagError{where + ": a group item has one positional (arg 0, a string): " + f.Name})
		case f.Flag == "":
			panic(tagError{where + ": a group item field needs a flag tag: " + f.Name})
		case f.Type == TypeList:
			panic(tagError{where + ": a group item cannot nest a group: " + f.Name})
		}
	}
	if heads != 1 {
		panic(tagError{where + ": a group item needs exactly one positional head (arg 0)"})
	}
	return fields
}

// checkGroups verifies every item flag of a group has an outer flag of the
// same name and type (the shared value), so a flag means one thing before
// and after the first item, and --help, docs and OpenAPI that list the
// outer flags list every flag the command takes.
func checkGroups(op string, in []FieldInfo) {
	outer := map[string]FieldType{}
	for _, f := range in {
		if f.Flag != "" {
			outer[f.Flag] = f.Type
		}
	}
	for _, g := range in {
		if !g.Group {
			continue
		}
		for _, f := range g.Fields {
			if f.Flag == "" {
				continue
			}
			if t, ok := outer[f.Flag]; !ok || t != f.Type {
				panic(tagError{fmt.Sprintf("op.Define(%s): group %s flag --%s needs an outer --%s of the same type (the shared value)", op, g.Name, f.Flag, f.Flag)})
			}
		}
	}
}

// GroupHead returns the item's positional head field of a group.
func (f FieldInfo) GroupHead() (FieldInfo, bool) {
	for _, c := range f.Fields {
		if c.Arg == 0 {
			return c, true
		}
	}
	return FieldInfo{}, false
}

// GroupFlags returns the flag names an item of group f takes.
func (f FieldInfo) GroupFlags() []string {
	var out []string
	for _, c := range f.Fields {
		if c.Flag != "" {
			out = append(out, c.Flag)
		}
	}
	return out
}

// routeGroup splits the flag values of d between the outer input and the
// items of the group g (see the file comment). items[i] holds the flags of
// item i. Without FlagPos every value stays outer (a caller that cannot
// tell positions, such as a test decoder, keeps the old meaning).
func (d ArgsDecoder) routeGroup(g FieldInfo) (outer map[string][]string, items []map[string][]string, err error) {
	n := max(len(d.Args)-g.Arg, 0)
	items = make([]map[string][]string, n)
	if d.FlagPos == nil {
		return d.Flags, items, nil
	}
	outer = make(map[string][]string, len(d.Flags))
	for name, vals := range d.Flags {
		outer[name] = vals
	}
	for _, name := range g.GroupFlags() {
		vals, ok := d.Flags[name]
		if !ok {
			continue
		}
		pos := d.FlagPos[name]
		if len(pos) != len(vals) {
			return nil, nil, errs.New(errs.Internal, "--"+name+": "+strconv.Itoa(len(vals))+" values but "+strconv.Itoa(len(pos))+" positions")
		}
		var shared []string
		for k, v := range vals {
			i := pos[k] - g.Arg - 1 // positionals before the value, minus the ones before the group, minus one: the item it follows
			if i < 0 || i >= n {
				shared = append(shared, v)
				continue
			}
			if items[i] == nil {
				items[i] = map[string][]string{}
			}
			items[i][name] = append(items[i][name], v)
		}
		if len(shared) == 0 {
			delete(outer, name) // given only after items: the shared field stays unset
		} else {
			outer[name] = shared
		}
	}
	return outer, items, nil
}

// routeGroupByFlag is routeGroup for a group whose items are the values
// of its flag g.ArgOf (`lore task split --id tsk_… --title a --priority
// high --title b`, U-4): a group flag typed after the k-th item flag (by
// FlagSeq) is item k's, one typed before the first is shared. The item
// flag itself leaves the outer input: its values are the items.
func (d ArgsDecoder) routeGroupByFlag(g FieldInfo) (outer map[string][]string, items []map[string][]string, err error) {
	headSeq := d.FlagSeq[g.ArgOf]
	if len(headSeq) != len(d.Flags[g.ArgOf]) {
		return nil, nil, errs.New(errs.Internal, "--"+g.ArgOf+": values and their order differ")
	}
	items = make([]map[string][]string, len(headSeq))
	outer = make(map[string][]string, len(d.Flags))
	for name, vals := range d.Flags {
		if name != g.ArgOf {
			outer[name] = vals
		}
	}
	for _, name := range g.GroupFlags() {
		vals, ok := d.Flags[name]
		if !ok {
			continue
		}
		seq := d.FlagSeq[name]
		if len(seq) != len(vals) {
			return nil, nil, errs.New(errs.Internal, "--"+name+": values and their order differ")
		}
		var shared []string
		for k, v := range vals {
			i := -1 // the last item flag typed before this value
			for j, hs := range headSeq {
				if hs < seq[k] {
					i = j
				}
			}
			if i < 0 {
				shared = append(shared, v)
				continue
			}
			if items[i] == nil {
				items[i] = map[string][]string{}
			}
			items[i][name] = append(items[i][name], v)
		}
		if len(shared) == 0 {
			delete(outer, name)
		} else {
			outer[name] = shared
		}
	}
	return outer, items, nil
}

// decodeGroup fills the []S of group g: one item per positional from the
// group's index, each item decoded by its own ArgsDecoder (head + flags).
func decodeGroup(fv reflect.Value, g FieldInfo, args []string, items []map[string][]string) error {
	if len(args) == 0 {
		return nil
	}
	out := reflect.MakeSlice(fv.Type(), len(args), len(args))
	for i, a := range args {
		if err := (ArgsDecoder{Args: []string{a}, Flags: items[i]}).Decode(out.Index(i), g.Fields); err != nil {
			// The item decode fails only with an E_INVALID_INPUT of its own;
			// say which item, keep its message (no doubled code prefix).
			msg := err.Error()
			if e, ok := errors.AsType[*errs.Error](err); ok {
				msg = e.Message
			}
			return errs.New(errs.InvalidInput, g.Name+" "+strconv.Itoa(i+1)+" "+strconv.Quote(a)+": "+msg)
		}
	}
	fv.Set(out)
	return nil
}
