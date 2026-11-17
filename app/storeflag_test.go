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

	// a show reads the store it names too (devco D-499 widened: every
	// read takes --store): the row is found in that store, and an id the
	// store does not hold is not found there
	var listed struct {
		Data []struct {
			ID   string `json:"id"`
			Body string `json:"body"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(w.mustLore(t, dir, "memory", "list", "--json")), &listed); err != nil {
		t.Fatal(err)
	}
	var innerID, repoID string
	for _, d := range listed.Data {
		switch d.Body {
		case "placeholder alpha":
			innerID = d.ID
		case "placeholder gamma":
			repoID = d.ID
		}
	}
	if out := w.mustLore(t, dir, "memory", "show", innerID, "--store", "inner"); !strings.Contains(out, "placeholder alpha") {
		t.Errorf("memory show %s --store inner does not show it:\n%s", innerID, out)
	}
	if code, out = w.loreOut(t, dir, "memory", "show", repoID, "--store", "inner"); code == 0 || strings.Contains(out, "unknown flag") {
		t.Errorf("memory show <a repo memory> --store inner: exit %d, want not found in inner\n%s", code, out)
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
