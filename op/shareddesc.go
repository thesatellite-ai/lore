package op

// shareddesc.go — flag help shared by many ops. A desc tag that starts
// with "shared:" names one of these texts instead of spelling it out, so a
// rule every command follows (where a write with no --to lands) is written
// once and cannot drift between forty copies (devco:D-729: every add said
// "default repo" while a linked repo already wrote its store folder).

import "strings"

// sharedDescPrefix marks a desc tag that names a shared text.
const sharedDescPrefix = "shared:"

// DescWriteTo is the --to help of every create and edit: where a row with
// no --to is written. Why: in a repo linked to a store folder the default
// is that folder (the place its reads come from), and the help said repo.
const DescWriteTo = "where the row is written: a name from .lore/stores.yaml, me, <store>/<folder>, or repo (this repo's .lore/data); default: the store folder this repo is linked to (where its reads come from), else the repo"

// sharedDescs maps a desc tag's name (after "shared:") to its text.
var sharedDescs = map[string]string{
	"write-to": DescWriteTo,
}

// sharedDesc expands a desc tag: "shared:<name>" is the shared text of that
// name (ok false for an unknown name), any other tag is itself.
func sharedDesc(tag string) (string, bool) {
	name, shared := strings.CutPrefix(tag, sharedDescPrefix)
	if !shared {
		return tag, true
	}
	text, ok := sharedDescs[name]
	return text, ok
}
