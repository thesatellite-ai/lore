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
	"sync/atomic"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
)

// peekHint finds the table a not-found hint names.
var peekHint = regexp.MustCompile("`lore sync peek [^ ]+ ([a-z_]+)`")

// ownPrefix reads the expected prefix from a malformed-id answer
// (lookup.BadID: `… is not a memory id (mem_…)`).
var ownPrefix = regexp.MustCompile(`\(([a-z]+)_…\)`)

// showNouns are the top-level nouns with a `show` verb that takes an id.
func showNouns(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, c := range newDriftRoot(t).Commands() {
		for _, s := range c.Commands() {
			if s.Name() == "show" && strings.Contains(s.Use, "<") {
				out = append(out, c.Name())
			}
		}
	}
	return out
}

func TestNotFoundHintNamesTheRealTable(t *testing.T) {
	t.Parallel()
	tables := map[string]ids.Prefix{}
	for _, d := range schema.Builtin() {
		if d.Kind.IsZero() {
			tables[string(d.Name)] = d.Prefix
		}
	}
	// a well-formed id no row has (an all-zero id is malformed: no UUIDv7)
	const hex = "_01a1a5aca7f57659be7320c5f4b7f00d"
	var hinted atomic.Int32
	t.Run("nouns", func(t *testing.T) { // the group waits for its parallel nouns
		for _, noun := range showNouns(t) {
			t.Run(noun, func(t *testing.T) {
				t.Parallel()
				w := newCLIWorld(t).person()
				dir := w.initRepo(t)
				// B-ERRS-1: a malformed id is E_INVALID_ID with no peek hint;
				// its message names the noun's own prefix ("(mem_…)").
				_, _, errOut := w.lore(t, dir, noun, "show", "zzz"+hex)
				// (a noun whose show takes a name, resource, answers a name miss)
				if strings.Contains(errOut, "[E_INVALID_ID]") && peekHint.MatchString(errOut) {
					t.Fatalf("a malformed id got the peek hint:\n%s", errOut)
				}
				own := ownPrefix.FindStringSubmatch(errOut)
				if own == nil {
					return // this noun answers a bad id its own way (actor, project, trash, …)
				}
				_, _, errOut = w.lore(t, dir, noun, "show", own[1]+hex)
				m := peekHint.FindStringSubmatch(errOut)
				if m == nil {
					return // no live not-found answer for this noun
				}
				hinted.Add(1)
				table := m[1]
				prefix, ok := tables[table]
				if !ok {
					t.Fatalf("hint names %q, which is no table:\n%s", table, errOut)
				}
				if string(prefix) != own[1] {
					t.Fatalf("own-kind id %s_…: the peek hint names %s (%s_…):\n%s", own[1], table, prefix, errOut)
				}
				other := "rul" + hex
				if table == "rules" {
					other = "mem" + hex
				}
				if _, _, errOut = w.lore(t, dir, noun, "show", other); peekHint.MatchString(errOut) {
					t.Fatalf("an id of another kind (%s) got the peek hint:\n%s", other, errOut)
				}
			})
		}
	})
	if n := hinted.Load(); n < 15 {
		t.Fatalf("only %d nouns gave the peek hint; the live not-found answer is not wired", n)
	}
}
