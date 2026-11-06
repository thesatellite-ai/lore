//go:build teame2e

package main

// te-c per-noun metadata filters (later 461, OV14 tests; METADATA 3.7):
// every noun whose list takes --meta filters by it, on a teammate's clone
// too. dev1 adds two rows of each noun, one with team=a and one with
// team=b, and shares them (sync + push); dev2 pulls and lists each noun
// with --meta team=a: the first row is listed, the second is not, and
// --meta team=b is the mirror. Handoff is left out: its add needs a
// registered @handle (--for), which this world does not set up.

import (
	"encoding/json"
	"strings"
	"testing"

	th "saas/cmd/cli/internal/teamharness"
)

// teCFilterNoun is one noun's add arguments; "X" is replaced by a per-row
// marker so the two rows differ.
type teCFilterNoun struct {
	noun string
	add  []string
}

func teCFilterNouns(tasklist string) []teCFilterNoun {
	titled := func(n string) teCFilterNoun {
		return teCFilterNoun{n, []string{"--title", "title X", "--body", "body"}}
	}
	return []teCFilterNoun{
		titled("architecturenote"), titled("behaviour"), titled("cookbookrecipe"), titled("decision"),
		titled("hotfix"), titled("incident"), titled("pattern"), titled("plan"), titled("playbook"),
		titled("prompt"), titled("snapshot"), titled("suggestion"), titled("tasklist"), titled("workflow"),
		titled("workspace"),
		{"external-source", []string{"--kind", "web", "--name", "source X"}},
		{"memory", []string{"--body", "memory X"}},
		{"mission", []string{"mission X"}},
		{"reminder", []string{"reminder X", "--due", "2030-01-01"}},
		{"rule", []string{"--body", "rule X"}},
		{"task", []string{"task X", "--tasklist", tasklist}},
		{"tastepref", []string{"taste X"}},
		{"techdoc", []string{"--name", "techdoc X"}},
	}
}

// teCAddID runs add --json and returns the new row's id.
func teCAddID(t *testing.T, c *th.Clone, args ...string) string {
	t.Helper()
	r := c.MustLore(append(args, "--json")...)
	var v struct {
		ID   string `json:"id"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil {
		t.Fatalf("%v --json: %v %s", args, err, r)
	}
	if v.Data.ID != "" {
		return v.Data.ID
	}
	if v.ID == "" {
		t.Fatalf("%v --json printed no id: %s", args, r)
	}
	return v.ID
}

func TestTeC_MetaFilterEveryNounAcrossClones(t *testing.T) {
	rt := newRepoTeam(t, th.Flag("meta", "memory", "list"))
	rt.c1.MustLore("meta", "add", "team", "--type", "text")
	nouns := teCFilterNouns(rt.tasklist)
	type pair struct{ a, b string }
	ids := map[string]pair{}
	for _, n := range nouns {
		withMarker := func(m string) []string {
			out := make([]string, 0, len(n.add)+3)
			for _, a := range n.add {
				out = append(out, strings.ReplaceAll(a, "X", m))
			}
			return out
		}
		a := teCAddID(t, rt.c1, append(append([]string{n.noun, "add"}, withMarker("one")...), "--meta", "team=a")...)
		b := teCAddID(t, rt.c1, append(append([]string{n.noun, "add"}, withMarker("two")...), "--meta", "team=b")...)
		ids[n.noun] = pair{a, b}
	}
	rt.c1.MustLore("sync")
	rt.c1.CommitAll("rows with metadata")
	rt.c1.AdminPush("origin", "main")
	rt.c2.Git("pull", "-q", "--ff-only", "origin", "main")
	rt.c2.MustLore("sync")

	for _, n := range nouns {
		p := ids[n.noun]
		for _, c := range []struct{ value, in, out string }{{"a", p.a, p.b}, {"b", p.b, p.a}} {
			got := rt.c2.MustLore(n.noun, "list", "--meta", "team="+c.value, "--json").Stdout
			if !strings.Contains(got, c.in) {
				t.Errorf("%s list --meta team=%s on dev2 misses %s: %s", n.noun, c.value, c.in, got)
			}
			if strings.Contains(got, c.out) {
				t.Errorf("%s list --meta team=%s on dev2 lists %s (team is the other value)", n.noun, c.value, c.out)
			}
		}
	}
}
