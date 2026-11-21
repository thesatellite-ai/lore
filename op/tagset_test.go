package op

import (
	"reflect"
	"testing"
)

// TestTagSetMatchesReflect (devco D-825): a tag read once answers every
// key exactly as reflect.StructTag does, malformed tags included (reflect
// stops at the first malformed part, and refuses a bad quoted value only
// for the key asked).
func TestTagSetMatchesReflect(t *testing.T) {
	tags := []reflect.StructTag{
		``, `json:"id"`, `json:"id,omitempty" flag:"id" desc:"the id"`,
		`json:"a" json:"b"`, `  json:"spaced"   flag:"x"  `, `json:"x" flag`, `json:"x" flag:`, `json:"x" flag:"unterminated`,
		`json:"x" bad:"\q" flag:"after-bad"`, `json:"esc \"q\" \\ \t"`, `json:"x":"y"`, `:"x" json:"y"`, `"x" json:"y"`,
		`json:"x"flag:"y"`, `json:"ü" desc:"日本"`, "json:\"x\"\tflag:\"y\"", `json:"-" arg:"0" rest:"true"`,
	}
	keys := []string{"json", "flag", "desc", "bad", "arg", "rest", "x", ""}
	for _, tag := range tags {
		ts := parseTags(tag)
		for _, k := range keys {
			wv, wok := tag.Lookup(k)
			gv, gok := ts.Lookup(k)
			if wv != gv || wok != gok {
				t.Errorf("tag %q key %q: tagSet (%q, %v), reflect (%q, %v)", tag, k, gv, gok, wv, wok)
			}
			if tag.Get(k) != ts.Get(k) {
				t.Errorf("tag %q key %q: Get differs", tag, k)
			}
		}
	}
}
