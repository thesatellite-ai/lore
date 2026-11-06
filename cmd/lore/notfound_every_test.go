package main

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
)

// ownNotFound are the show nouns that answer a missing id their own way
// (a hint of their own, or nothing that could land): every other noun of
// a built-in table must give the F1 peek hint (later 112.2: "every core
// noun"; `lore resource show` gave none because its arguments were never
// recorded for the not-found answer).
var ownNotFound = map[string]string{
	"project": "hint: lore project list",
	"trash":   "a trashed row is not a branch row",
	"commit":  "a git sha, not a row id",
	"policy":  "a policy key, not a row id",
	"skill":   "",
	"machine": "machine-local rows never land; hint: lore machine list",
	"scope":   "show takes a path, not a row id",
}

// TestNotFoundHintForEveryCoreNoun: `lore <noun> show <own-prefix id>` of
// a missing row gives the peek hint for every show noun backed by a
// built-in table, except the listed nouns with an answer of their own.
func TestNotFoundHintForEveryCoreNoun(t *testing.T) {
	asPerson(t)
	envScopeRepo(t)
	prefixOf := map[string]string{}
	for _, d := range schema.Builtin() {
		if d.Kind.IsZero() && d.Prefix != "" {
			prefixOf[string(d.Name)] = string(d.Prefix)
		}
	}
	const hex = "_01a1a5aca7f57659be7320c5f4b7f00d"
	checked := 0
	for _, noun := range showNouns(t) {
		if _, own := ownNotFound[noun]; own {
			continue
		}
		flat := strings.ReplaceAll(noun, "-", "_")
		var table string
		for _, cand := range []string{flat + "s", flat + "es", strings.TrimSuffix(flat, "y") + "ies"} {
			if _, ok := prefixOf[cand]; ok {
				table = cand
				break
			}
		}
		if table == "" {
			continue // no built-in table behind this noun (a dynamic kind)
		}
		checked++
		t.Run(noun, func(t *testing.T) {
			_, _, errOut := runCaptured(t, noun, "show", prefixOf[table]+hex)
			m := peekHint.FindStringSubmatch(errOut)
			if m == nil || m[1] != table {
				t.Fatalf("%s show %s%s: want the peek hint naming %s:\n%s", noun, prefixOf[table], hex, table, errOut)
			}
		})
	}
	if checked < 15 {
		t.Fatalf("only %d nouns checked: the noun walk is broken", checked)
	}
}
