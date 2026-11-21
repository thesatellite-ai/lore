//go:build userscen

package userscen

// campaign_render_context_test.go — `lore context` (the agent's fetch
// command, CONTEXT_BUDGET 6.5-6.12) in the cases a script meets first:
//   - nothing applies: every list mode answers --json "data": [] (a list
//     a caller can take the length of, never null), on the CLI and REST;
//     the modes that search (--for-file, --for-prompt) also say what they
//     searched and what to try (devco:D-821/D-824), next to data, nothing
//     else (devco:D-832);
//   - odd values: a path with spaces and unicode, a 10k-char prompt, a
//     tag as a topic, a missing topic (E_NOT_FOUND + hint), a task id
//     of the wrong shape;
//   - a fetch writes nothing in the work tree.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestCampaignRender_ContextEmptyIsAList(t *testing.T) {
	c := renderSolo(t)
	srv := renderStartAPI(t, c)
	modes := []struct {
		name     string
		cli      []string
		rest     string
		searched string // the "searched" field of an empty answer ("" none)
	}{
		{"for-file", []string{"--for-file", "src/a.go"}, `{"for_file":"src/a.go"}`, `"path":"src/a.go"`},
		{"for-prompt", []string{"--for-prompt", "how do we deploy"}, `{"for_prompt":"how do we deploy"}`, `"words":["deploy"]`},
		{"topics", []string{"--topics"}, `{"topics":true}`, ""},
	}
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			r := c.Lore(append([]string{"context"}, append(m.cli, "--json")...)...).OK(t)
			contextEmpty(t, "CLI --json", r.Stdout, m.searched)
			rep := srv.post(t, "/v1/context/fetch", []byte(m.rest))
			if rep.status != 200 {
				t.Errorf("REST with nothing to return: 200, got %d %s", rep.status, rep.body)
			}
			contextEmpty(t, "REST", string(rep.body), m.searched)
			if r := strings.TrimSpace(r.Stdout); !sameJSON(r, string(rep.body)) {
				t.Errorf("CLI --json and REST differ:\n%s\n%s", r, rep.body)
			}
			// The text form says so instead of printing nothing.
			if txt := c.MustLore(append([]string{"context"}, m.cli...)...); strings.TrimSpace(txt.Stdout) == "" {
				t.Errorf("the text form is never empty: %s", txt)
			}
		})
	}
}

// contextEmpty checks body, a `lore context --json` answer with nothing
// to return: "data" is [] (never null), and when searched is set the
// searched object holds it and try lists 1-3 lore commands; no other field.
func contextEmpty(t *testing.T, where, body, searched string) {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("%s: not JSON: %v: %s", where, err, body)
	}
	var data []json.RawMessage
	if err := json.Unmarshal(m["data"], &data); err != nil || data == nil || len(data) != 0 {
		t.Errorf("%s with nothing to return: \"data\": [], got %s", where, body)
	}
	want := 1
	if searched != "" {
		want = 3
		var s bytes.Buffer
		if err := json.Compact(&s, m["searched"]); err != nil || !strings.Contains(s.String(), searched) {
			t.Errorf("%s: searched holds %s, got %s", where, searched, body)
		}
		var try []string
		if err := json.Unmarshal(m["try"], &try); err != nil || len(try) == 0 || len(try) > 3 || !strings.HasPrefix(try[0], "lore ") {
			t.Errorf("%s: try lists 1-3 lore commands, got %s", where, body)
		}
	}
	if len(m) != want {
		t.Errorf("%s: %d fields, want %d (data, and searched + try for a search): %s", where, len(m), want, body)
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
