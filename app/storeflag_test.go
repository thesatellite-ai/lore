package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/fileuri"
)

// TestStoreFlagSpellings (FB-FLAG-STORE): the store flag is --to on the
// add commands (v0.1.17's spelling) and --store on list and search; every
// one of them takes both spellings for the same value, a repeat across
// the two spellings is refused as one flag given twice, and an id command
// given a store flag gets a hint saying why it takes none.
func TestStoreFlagSpellings(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person()
	dir := w.initRepo(t)
	w.mustLore(t, dir, "store", "init", "inner", fileuri.URL(t.TempDir(), ""), "--yes")

	// add: --store is --to
	w.mustLore(t, dir, "memory", "add", "--body", "placeholder alpha", "--to", "inner")
	w.mustLore(t, dir, "memory", "add", "--body", "placeholder beta", "--store", "inner")
	w.mustLore(t, dir, "memory", "add", "--body", "placeholder gamma")

	// list and search: --to is --store
	bodies := func(args ...string) []string {
		t.Helper()
		var env struct {
			Data []struct {
				Body string `json:"body"`
			} `json:"data"`
		}
		out := w.mustLore(t, dir, append(args, "--json")...)
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		var got []string
		for _, d := range env.Data {
			got = append(got, d.Body)
		}
		return got
	}
	for _, spelling := range []string{"--to", "--store"} {
		for _, args := range [][]string{
			{"memory", "list", spelling, "inner"},
			{"memory", "search", "placeholder", spelling, "inner"},
		} {
			got := bodies(args...)
			if len(got) != 2 || strings.Contains(strings.Join(got, ","), "gamma") {
				t.Errorf("lore %s: got %q, want the two inner memories", strings.Join(args, " "), got)
			}
		}
	}

	// the two spellings are one flag
	code, out := w.loreOut(t, dir, "memory", "add", "--body", "placeholder delta", "--to", "inner", "--store", "repo")
	if code == 0 || !strings.Contains(out, "takes one value") {
		t.Errorf("--to with --store: exit %d, want a one-value refusal\n%s", code, out)
	}

	// help names the other spelling once, on the flag's own line
	help := w.mustLore(t, dir, "memory", "list", "--help")
	if !strings.Contains(help, "--store string") || !strings.Contains(help, "(also --to)") || strings.Contains(help, "--to string") {
		t.Errorf("memory list --help must list --store with (also --to), the alias hidden:\n%s", help)
	}

	// an id command takes no store flag: the hint says why
	code, out = w.loreOut(t, dir, "memory", "show", "mem_00000000000000000000000000000000", "--store", "inner")
	if code != 20 || !strings.Contains(out, "unknown flag: --store") ||
		!strings.Contains(out, "hint: lore memory show takes no store flag: it finds the row by its id in every attached store") {
		t.Errorf("memory show --store: exit %d\n%s", code, out)
	}

	// a command whose --to means something else keeps --store unknown, and
	// the hint names its store flag (handoff's --to is the deprecated --for)
	code, out = w.loreOut(t, dir, "handoff", "list", "--to", "inner")
	if code != 20 || !strings.Contains(out, "hint: lore handoff list takes --store, not --to") {
		t.Errorf("handoff list --to: exit %d\n%s", code, out)
	}
	code, out = w.loreOut(t, dir, "memory", "move", "mem_00000000000000000000000000000000", "--store", "inner")
	if code != 20 || !strings.Contains(out, "hint: lore memory move takes --to, not --store") {
		t.Errorf("memory move --store: exit %d\n%s", code, out)
	}
}
