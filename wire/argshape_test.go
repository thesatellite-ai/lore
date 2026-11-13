package wire

import (
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/op"
)

// Same argument shapes (S-3, owner standing rule 2026-11-13): sibling
// commands take the same named flag for the same field, so `lore X add
// --title` works on `lore X edit` too and a newcomer never learns two
// spellings of one thing. Both tests walk every operation of the real
// registry, never a hand list, so a new command is checked the day it
// lands.

// shapeKey names one field, or one canonical flag, of one noun
// ("behaviour" + "name").
type shapeKey struct{ noun, field string }

// siblingShapeExceptions are the same-noun fields that may take different
// flags, each with why. Keep it short: a new entry needs a reason a
// person would accept.
var siblingShapeExceptions = map[shapeKey]string{
	// task list/board --columns picks the table's display columns; task
	// search --column restricts the full-text MATCH like every other
	// search: two meanings that share a JSON name.
	{"task", "columns"}: "display columns (list, board) vs full-text columns (search)",
	// --from on task: the source store of an id (claim, ... alias
	// --store), a file of titles (split), a file format (import), a person
	// (reassign), a checklist source: one flag, several meanings, so the
	// store alias belongs only to the store one.
	{"task", "from"}: "--from names a store, a file, a format or a person",
	// memory move --to is the destination store; --store would read as the
	// store the memory is in now, so it is not offered there.
	{"memory", "to"}: "move's --to is the destination, not the store flag",
	// task note --to is where the note row goes, like task add --to, but
	// task note's --store already spells --from (the store the task id
	// comes from, as on claim and every coord verb), so --to has no
	// --store alias there.
	{"task", "to"}: "task note's --store is --from (the id's store)",
}

// registryShapeExceptions are the JSON fields whose flag differs between
// nouns on purpose.
var registryShapeExceptions = map[string]string{
	// A knowledge row stored with a name (behaviour, workflow, workspace)
	// takes --title like every other knowledge row; a person, org, team or
	// tag takes --name.
	"name": "a row's title is --title; a person's, team's or org's name is --name",
	// The same JSON name, two meanings (see siblingShapeExceptions).
	"columns": "display columns vs full-text columns",
	// lore sync promote <id> --to <branch> reads as the destination;
	// --branch is its alias.
	"branch": "sync promote names the destination --to (alias --branch)",
	// trash restore/purge store the row id under JSON "target" (the REST
	// body since 0.1.18) but name it --id like trash show (A-18), alias
	// --target; machine reveal's --target is what to reveal, not a row.
	"target": "trash restore/purge: the row id is --id (alias --target); machine reveal --target is something else",
}

// opNoun is the noun of an operation name: every part but the verb.
func opNoun(name string) string {
	i := strings.LastIndex(name, ".")
	return name[:i]
}

// shapeFields yields every named-flag input field of the registry with its
// operation; repeated groups are skipped (their items carry the flags).
func shapeFields(reg *op.Registry, yield func(e op.Entry, f op.FieldInfo)) {
	for _, e := range reg.Entries() {
		for _, f := range e.InputFields() {
			if f.Flag == "" || f.Group {
				continue
			}
			yield(e, f)
		}
	}
}

// TestSiblingCommandsShareFlags: within one noun (add/edit/list/show/
// delete, ...) a JSON field has one canonical flag, and a canonical flag
// accepts the same spellings on every sibling: add's `--title` (alias
// `--name`) is edit's `--title` (alias `--name`) even where the two store
// it under different JSON names.
func TestSiblingCommandsShareFlags(t *testing.T) {
	t.Parallel()
	a := newTestApp(t, t.TempDir())
	type use struct {
		op        string
		flag      string
		spellings []string
	}
	byField := map[shapeKey][]use{} // noun + JSON field
	byFlag := map[shapeKey][]use{}  // noun + canonical flag
	shapeFields(a.Registry(), func(e op.Entry, f op.FieldInfo) {
		name := e.Spec().Name
		u := use{name, f.Flag, f.Spellings()}
		byField[shapeKey{opNoun(name), f.Name}] = append(byField[shapeKey{opNoun(name), f.Name}], u)
		byFlag[shapeKey{opNoun(name), f.Flag}] = append(byFlag[shapeKey{opNoun(name), f.Flag}], u)
	})
	for k, us := range byField {
		if _, ok := siblingShapeExceptions[k]; ok {
			continue
		}
		for _, u := range us[1:] {
			if u.flag != us[0].flag {
				t.Errorf("%s: field %q is --%s on %s but --%s on %s; siblings take one flag per field (alias the old spelling)",
					k.noun, k.field, us[0].flag, us[0].op, u.flag, u.op)
			}
		}
	}
	for k, us := range byFlag {
		if _, ok := siblingShapeExceptions[k]; ok {
			continue
		}
		for _, u := range us[1:] {
			for _, pair := range [][2]use{{us[0], u}, {u, us[0]}} {
				for _, s := range pair[0].spellings {
					if !slices.Contains(pair[1].spellings, s) {
						t.Errorf("%s: %s takes --%s as --%s but %s does not", k.noun, pair[0].op, s, k.field, pair[1].op)
					}
				}
			}
		}
	}
	for k := range siblingShapeExceptions {
		if len(byField[k]) == 0 && len(byFlag[k]) == 0 {
			t.Errorf("siblingShapeExceptions names %s %s, which no operation has: drop it", k.noun, k.field)
		}
	}
}

// TestFieldTakesOneFlagEverywhere: across nouns, a JSON field of one type
// has one canonical flag (--body is --body on every noun, --tasklist on
// every command that takes a tasklist), except the reasoned exceptions.
func TestFieldTakesOneFlagEverywhere(t *testing.T) {
	t.Parallel()
	a := newTestApp(t, t.TempDir())
	type typed struct {
		field string
		typ   op.FieldType
	}
	first := map[typed]string{} // field → "--flag on op"
	flagOf := map[typed]string{}
	used := map[string]bool{}
	shapeFields(a.Registry(), func(e op.Entry, f op.FieldInfo) {
		k := typed{f.Name, f.Type}
		if _, ok := registryShapeExceptions[f.Name]; ok {
			used[f.Name] = true
			return
		}
		at := "--" + f.Flag + " on " + e.Spec().Name
		prev, seen := flagOf[k]
		if !seen {
			flagOf[k], first[k] = f.Flag, at
			return
		}
		if prev != f.Flag {
			t.Errorf("field %q (%s) is %s but %s; one field, one flag", f.Name, f.Type, first[k], at)
		}
	})
	for name := range registryShapeExceptions {
		if !used[name] {
			t.Errorf("registryShapeExceptions names %q, which no flag field has: drop it", name)
		}
	}
}

// pageFlags are the shared paging flags every list takes (op.PageFlags):
// JSON field → canonical flag.
var pageFlags = map[string]string{"limit": "limit", "page": "page"}

// TestEveryListPages (T-4): every *.list operation takes --limit and
// --page as int flags, the same way. Found in a migration trial: `lore
// search --limit` worked while `lore task list --limit` and `lore memory
// list --limit` were refused as an unknown flag. A list input embeds
// op.PageFlags (Invoke cuts the rows), or pages itself with the same two
// fields (trash list); either way a new list command is checked here the
// day it lands.
func TestEveryListPages(t *testing.T) {
	t.Parallel()
	a := newTestApp(t, t.TempDir())
	lists := 0
	for _, e := range a.Registry().Entries() {
		name := e.Spec().Name
		if !strings.HasSuffix(name, ".list") {
			continue
		}
		lists++
		for field, flag := range pageFlags {
			i := slices.IndexFunc(e.InputFields(), func(f op.FieldInfo) bool { return f.Name == field })
			if i < 0 {
				t.Errorf("%s: no --%s; every list takes --limit and --page (embed op.PageFlags in its input)", name, flag)
				continue
			}
			if f := e.InputFields()[i]; f.Flag != flag || f.Type != op.TypeInt || f.Arg != op.NoArg {
				t.Errorf("%s: field %q is --%s (%s, arg %d); want the int flag --%s of op.PageFlags", name, field, f.Flag, f.Type, f.Arg, flag)
			}
		}
	}
	if lists < 50 {
		t.Fatalf("found %d *.list operations; the registry walk is broken", lists)
	}
}
