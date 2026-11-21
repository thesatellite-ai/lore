package op

import (
	"reflect"
	"strconv"
)

// tagPair is one key:"value" pair of a struct tag.
type tagPair struct {
	key, value string
	// bad is a value strconv.Unquote refused: reflect stops there only
	// when the key is the one asked for, so the pair is kept and answers
	// "not found" (as reflect does) instead of ending the parse.
	bad bool
}

// tagSet is a struct tag read once into its pairs. Why (devco D-825):
// every lore process defines every operation, and fieldOf asked one field's
// tag for up to fourteen keys; reflect.StructTag.Lookup scans and unquotes
// the whole tag on each call, about a third of what building the registry
// cost (the floor of every command, --version included). Lookup and Get
// answer exactly as reflect's do: pairs are read in order up to the first
// malformed part, and the first pair with a key wins.
type tagSet []tagPair

// parseTags reads tag with reflect.StructTag's grammar (key:"quoted value"
// separated by spaces), stopping where reflect.StructTag.Lookup would stop.
func parseTags(tag reflect.StructTag) tagSet {
	var out tagSet
	for tag != "" {
		i := 0
		for i < len(tag) && tag[i] == ' ' {
			i++
		}
		tag = tag[i:]
		if tag == "" {
			break
		}
		i = 0
		for i < len(tag) && tag[i] > ' ' && tag[i] != ':' && tag[i] != '"' && tag[i] != 0x7f {
			i++
		}
		if i == 0 || i+1 >= len(tag) || tag[i] != ':' || tag[i+1] != '"' {
			break
		}
		key := string(tag[:i])
		tag = tag[i+1:]
		i = 1
		for i < len(tag) && tag[i] != '"' {
			if tag[i] == '\\' {
				i++
			}
			i++
		}
		if i >= len(tag) {
			break
		}
		quoted := string(tag[:i+1])
		tag = tag[i+1:]
		value, err := strconv.Unquote(quoted)
		out = append(out, tagPair{key: key, value: value, bad: err != nil})
	}
	return out
}

// Lookup is reflect.StructTag.Lookup over the parsed pairs.
func (t tagSet) Lookup(key string) (string, bool) {
	for _, p := range t {
		if p.key == key {
			if p.bad {
				return "", false
			}
			return p.value, true
		}
	}
	return "", false
}

// Get is reflect.StructTag.Get over the parsed pairs.
func (t tagSet) Get(key string) string {
	v, _ := t.Lookup(key)
	return v
}
