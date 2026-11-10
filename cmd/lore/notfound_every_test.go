package main

import (
	"regexp"
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
	t.Parallel()
	w := newCLIWorld(t).person()
	dir := w.initRepo(t)
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
			t.Parallel()
			_, _, errOut := w.lore(t, dir, noun, "show", prefixOf[table]+hex)
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

// TestNotFoundHintOnlyForTheRow (t-e2e6): a not-found about the TARGET of
// a found row is never answered with the row's peek hint: `--to me` with
// no personal store names `lore store init me`, an unknown store name gets
// no peek hint; a missing row id still gets it.
func TestNotFoundHintOnlyForTheRow(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person()
	dir := w.initRepo(t)
	code, out, errOut := w.lore(t, dir, "memory", "add", "--body", "a note to move")
	if code != 0 {
		t.Fatalf("memory add: %d %s", code, errOut)
	}
	id := memIDRe.FindString(out + errOut)
	if id == "" {
		t.Fatalf("no id in %q", out)
	}
	cases := []struct {
		name    string
		args    []string
		want    string // substring of stderr
		notWant string
	}{
		{"to me, no personal store", []string{"move", id, "--to", "me"}, "lore store init me", "lore sync peek"},
		{"to an unknown store", []string{"move", id, "--to", "nosuchstore"}, "no store named nosuchstore", "lore sync peek"},
		{"missing row", []string{"move", "mem_01a1a5aca7f57659be7320c5f4b7f00d", "--to", "repo"}, "lore sync peek", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			code, _, errOut := w.lore(t, dir, c.args...)
			if code == 0 || !strings.Contains(errOut, c.want) || (c.notWant != "" && strings.Contains(errOut, c.notWant)) {
				t.Fatalf("exit %d, stderr:\n%s\nwant %q, not %q", code, errOut, c.want, c.notWant)
			}
		})
	}
}

// memIDRe finds a memory id in command output.
var memIDRe = regexp.MustCompile(`mem_[0-9a-f]{32}`)
