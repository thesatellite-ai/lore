package main

// later.md 112.2: the live not-found answer for every core noun on the
// release binary. `lore <noun> show <id>` of a missing row names
// `lore sync peek <remote>/<branch> <table>` (the row may have landed on
// the default branch), and that table must be a real table — the one the
// id's prefix belongs to — never a guessed plural; an id of another kind
// gets no such hint, since it can never land as this noun.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
)

// peekHint finds the table a not-found hint names.
var peekHint = regexp.MustCompile("`lore sync peek [^ ]+ ([a-z_]+)`")

// showNouns are the top-level nouns with a `show` verb that takes an id.
func showNouns(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, c := range driftRoot(t).Commands() {
		for _, s := range c.Commands() {
			if s.Name() == "show" && strings.Contains(s.Use, "<") {
				out = append(out, c.Name())
			}
		}
	}
	return out
}

func TestNotFoundHintNamesTheRealTable(t *testing.T) {
	asPerson(t)
	envScopeRepo(t)
	tables := map[string]ids.Prefix{}
	for _, d := range schema.Builtin() {
		if d.Kind.IsZero() {
			tables[string(d.Name)] = d.Prefix
		}
	}
	const hex = "_00000000000000000000000000000000"
	hinted := 0
	for _, noun := range showNouns(t) {
		t.Run(noun, func(t *testing.T) {
			_, _, errOut := runCaptured(t, noun, "show", "zzz"+hex)
			m := peekHint.FindStringSubmatch(errOut)
			if m == nil {
				return // this noun answers a missing id its own way (actor, project, trash, …)
			}
			hinted++
			table := m[1]
			prefix, ok := tables[table]
			if !ok {
				t.Fatalf("hint names %q, which is no table:\n%s", table, errOut)
			}
			_, _, errOut = runCaptured(t, noun, "show", string(prefix)+hex)
			if m := peekHint.FindStringSubmatch(errOut); m == nil || m[1] != table {
				t.Fatalf("own-kind id %s_…: want the peek hint naming %s:\n%s", prefix, table, errOut)
			}
			other := "rul" + hex
			if table == "rules" {
				other = "mem" + hex
			}
			if _, _, errOut = runCaptured(t, noun, "show", other); peekHint.MatchString(errOut) {
				t.Fatalf("an id of another kind (%s) got the peek hint:\n%s", other, errOut)
			}
		})
	}
	if hinted < 15 {
		t.Fatalf("only %d nouns gave the peek hint; the live not-found answer is not wired", hinted)
	}
}
