//go:build userscen

package userscen

// campaign_render_context_test.go — `lore context` (the agent's fetch
// command, CONTEXT_BUDGET 6.5-6.12) in the cases a script meets first:
//   - nothing applies: every list mode answers --json {"data": []} (a list
//     a caller can take the length of, never null), on the CLI and REST;
//   - odd values: a path with spaces and unicode, a 10k-char prompt, a
//     tag as a topic, a missing topic (E_NOT_FOUND + hint), a task id
//     of the wrong shape;
//   - a fetch writes nothing in the work tree.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCampaignRender_ContextEmptyIsAList(t *testing.T) {
	c := renderSolo(t)
	srv := renderStartAPI(t, c)
	modes := []struct {
		name string
		cli  []string
		rest string
	}{
		{"for-file", []string{"--for-file", "src/a.go"}, `{"for_file":"src/a.go"}`},
		{"for-prompt", []string{"--for-prompt", "how do we deploy"}, `{"for_prompt":"how do we deploy"}`},
		{"topics", []string{"--topics"}, `{"topics":true}`},
	}
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			r := c.Lore(append([]string{"context"}, append(m.cli, "--json")...)...).OK(t)
			if got := strings.TrimSpace(r.Stdout); !sameJSON(got, `{"data":[]}`) { // the CLI indents --json
				t.Errorf("CLI --json with nothing to return: {\"data\":[]}, got %s", got)
			}
			rep := srv.post(t, "/v1/context/fetch", []byte(m.rest))
			if rep.status != 200 || strings.TrimSpace(string(rep.body)) != `{"data":[]}` {
				t.Errorf("REST with nothing to return: 200 {\"data\":[]}, got %d %s", rep.status, rep.body)
			}
			// The text form says so instead of printing nothing.
			if txt := c.MustLore(append([]string{"context"}, m.cli...)...); strings.TrimSpace(txt.Stdout) == "" {
				t.Errorf("the text form is never empty: %s", txt)
			}
		})
	}
}

func TestCampaignRender_ContextOddValues(t *testing.T) {
	c := renderSolo(t)
	c.MustLore("tag", "add", "--name=launch_week")
	id := renderID(t, c.MustLore("rule", "add", "--body", "launch días: freeze the schema", "--severity", "must", "--tag", "launch_week"), "rul")
	c.WriteFile("docs/my notes/ünïcode file.md", "x\n")
	before := campTree(t, c.Dir)

	c.MustLore("context", "--topic", "launch_week").Says(t, id)
	topics := campJSON[struct {
		Data []struct {
			Name   string         `json:"name"`
			Counts map[string]int `json:"counts"`
		} `json:"data"`
	}](t, c.Lore("context", "--topics", "--json"))
	found := false
	for _, tp := range topics.Data {
		found = found || (tp.Name == "launch_week" && tp.Counts["rules"] == 1)
	}
	if !found {
		t.Errorf("--topics --json lists the tag with its rule count: %+v", topics.Data)
	}
	c.MustLore("context", "--for-file", "docs/my notes/ünïcode file.md").Says(t, "# lore context for docs/my notes/ünïcode file.md")
	long := strings.Repeat("freeze the schema ", 600) // ~10k chars
	r := c.MustLore("context", "--for-prompt", long, "--json")
	var picks struct {
		Data []renderPick `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &picks); err != nil {
		t.Fatalf("a 10k-char prompt still answers JSON: %v", err)
	}
	campSameTree(t, "lore context", before, campTree(t, c.Dir))

	// Refusals: the exact code, a hint where the next step is known.
	campRefusal(t, c, campExitNotFound, "E_NOT_FOUND", "lore context --topics", "context", "--topic", "no-such-topic")
	campRefusal(t, c, campExitBadInput, "E_INVALID_INPUT", "", "context")
	campRefusal(t, c, campExitBadInput, "E_INVALID_INPUT", "one `lore context` per question", "context", "--topics", "--for-file", "a.go")
	campRefusal(t, c, campExitBadInput, "E_INVALID_ID", "", "context", "--for-task", "tsk_nope")
}
