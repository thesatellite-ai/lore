//go:build userscen

package userscen

// render_context_test.go — `lore context`, the agent's fetch command
// (CONTEXT_BUDGET 6.8, 6.9, 6.13), driven the way an agent calls it:
//   - no mode, and two modes at once: E_INVALID_INPUT exit 20, text and
//     --json error envelope;
//   - --for-file: the folder's rules and hotfixes with their "why", the
//     --json picks {id, kind, why, bytes}; a path nothing applies to says so;
//   - --topics / --topic (a tag), an unknown topic E_NOT_FOUND exit 27 with
//     its hint; --for-prompt; --for-task with a bad id;
//   - --max-bytes: the answer is capped, never splits an entry, ends with
//     the "more:" command;
//   - --internal in a repo with nothing internal; --hook answers in the
//     tool's JSON and never fails the tool (bad input, unknown tool);
//   - REST: `lore api` answers context.fetch and render --report with the
//     same data and the same refusal as the CLI.

import (
	"encoding/json"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// renderPick is one --json pick of lore context.
type renderPick struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Why   string `json:"why"`
	Bytes int    `json:"bytes"`
}

// renderErrEnv is the --json error envelope.
type renderErrEnv struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
		Exit    int    `json:"exit"`
	} `json:"error"`
}

func TestRenderContextModeRefusals(t *testing.T) {
	c := renderSolo(t)
	c.Lore("context").Fails(t, 20, "[E_INVALID_INPUT]", "pass --for-file <path>, --for-task <id>, --topic <name>, --topics, --for-prompt <text>, --hook <tool> --hook-stdin, or --internal")
	var env renderErrEnv
	j := c.Lore("context", "--json")
	if err := json.Unmarshal([]byte(j.Stdout), &env); err != nil || env.Error.Code != "E_INVALID_INPUT" || env.Error.Exit != 20 || !strings.Contains(env.Error.Message, "--for-file <path>") {
		t.Errorf("--json: the error envelope: %v %s", err, j)
	}
	pairs := [][]string{
		{"--for-file", "a.go", "--topics"},
		{"--for-prompt", "x", "--internal"},
		{"--topic", "x", "--for-file", "a.go"},
	}
	for _, p := range pairs {
		c.Lore(append([]string{"context"}, p...)...).Fails(t, 20, "[E_INVALID_INPUT]", "pass only one of", "run one `lore context` per question")
	}
	c.Lore("context", "--for-task", "tsk_nope").Fails(t, 20, "[E_INVALID_ID]", `"tsk_nope" is not a task id`)
	c.Lore("context", "--topic", "nosuch").Fails(t, th.ExitNotFound, "[E_NOT_FOUND] no topic (tag) named nosuch", "lore context --topics")
}

func TestRenderContextForFileAndTopics(t *testing.T) {
	c := renderSolo(t)
	ids := renderBillingRows(t, c)
	r := c.MustLore("context", "--for-file", "src/billing/invoice.go")
	r.Says(t, "# lore context for src/billing/invoice.go", "## Rules that apply here", ids["dir1"], ids["dirhot"], `why="scope dir:src/billing"`)
	picks := th.DecodeJSON[struct {
		Data []renderPick `json:"data"`
	}](t, c.Lore("context", "--for-file", "src/billing/invoice.go", "--json"))
	kinds := map[string]string{}
	for _, p := range picks.Data {
		kinds[p.ID] = p.Kind
		if p.Bytes <= 0 || p.Why == "" {
			t.Errorf("each pick has a size and a why: %+v", p)
		}
	}
	if kinds[ids["dir1"]] != "rule" || kinds[ids["dir2"]] != "rule" || kinds[ids["dirhot"]] != "hotfix" {
		t.Errorf("--json picks name the folder's rules and hotfix by kind: %+v", picks.Data)
	}
	c.MustLore("context", "--for-file", "docs/nothing/here.md").Says(t, "(nothing in the store applies here; try `lore search \"<keywords>\"`)").Never(t, ids["dir1"])

	// Topics: a tag on two rows.
	c.MustLore("tag", "add", "--name", "payments")
	c.MustLore("tag", "attach", "--tag", "payments", "--on-table", "rules", "--on-id", ids["dir1"])
	c.MustLore("tag", "attach", "--tag", "payments", "--on-table", "rules", "--on-id", ids["repo"])
	tr := c.MustLore("context", "--topic", "payments")
	tr.Says(t, ids["dir1"], ids["repo"]).Never(t, ids["dir2"])
	type topic struct {
		Name   string         `json:"name"`
		Counts map[string]int `json:"counts"`
	}
	tops := th.DecodeJSON[struct {
		Data []topic `json:"data"`
	}](t, c.Lore("context", "--topics", "--json"))
	found := false
	for _, tp := range tops.Data {
		if tp.Name == "payments" {
			found = tp.Counts["rules"] == 2
		}
	}
	if !found {
		t.Errorf("--topics --json counts the payments topic's two rules: %+v", tops.Data)
	}
	c.MustLore("context", "--for-prompt", "invoice crash").Says(t, ids["dirhot"])
}

func TestRenderContextMaxBytesNeverSplits(t *testing.T) {
	c := renderSolo(t)
	c.WriteFile("src/billing/invoice.go", "package billing\n")
	for i := range 6 {
		c.MustLore("rule", "add", "--body", renderLongBody(i, 300), "--severity", "must", "--scope", "dir:src/billing")
	}
	full := c.MustLore("context", "--for-file", "src/billing/invoice.go")
	for _, cap := range []string{"600", "1200"} {
		r := c.MustLore("context", "--for-file", "src/billing/invoice.go", "--max-bytes", cap)
		out := r.Stdout
		if len(out) >= len(full.Stdout) {
			t.Errorf("--max-bytes %s shortens the answer (%d vs %d)", cap, len(out), len(full.Stdout))
		}
		if strings.Count(out, "<rule ") != strings.Count(out, "</rule>") {
			t.Errorf("--max-bytes %s never splits an entry:\n%s", cap, out)
		}
		if !strings.Contains(out, "more: ") {
			t.Errorf("a capped answer ends with the command for the rest: %s", out)
		}
	}
}

func TestRenderContextInternalAndHook(t *testing.T) {
	c := renderSolo(t)
	id := renderID(t, c.MustLore("hotfix", "add", "--title", "Invoice crash", "--body", "restart the invoice worker", "--severity", "critical"), "hfx")
	c.MustLore("context", "--internal").Says(t, "(nothing internal or personal loads in this repo)")

	type hookOut struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	renderHookNoDeadline(c)
	hook := func(tool, stdin string) th.Result {
		cmd := c.Command("context", "--hook", tool, "--hook-stdin")
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		code := 0
		if err != nil {
			code = -1
			if ee, ok := err.(interface{ ExitCode() int }); ok {
				code = ee.ExitCode()
			}
		}
		return th.Result{Args: cmd.Args[1:], Stdout: string(out), Code: code}
	}
	r := hook("claude_code", `{"prompt":"the invoice crash again"}`)
	var ho hookOut
	if r.Code != 0 || json.Unmarshal([]byte(r.Stdout), &ho) != nil || !strings.Contains(ho.HookSpecificOutput.AdditionalContext, id) {
		t.Fatalf("a prompt hook answers in Claude Code's JSON with the matching hotfix: %s", r)
	}
	// A hook never fails the tool: bad input and an unknown tool answer
	// nothing, exit 0.
	for _, tc := range [][2]string{{"claude_code", "not json"}, {"vim", "{}"}, {"codex", ""}} {
		if r := hook(tc[0], tc[1]); r.Code != 0 || strings.TrimSpace(r.Stdout) != "" {
			t.Errorf("hook %s with %q: exit 0 and no answer: %s", tc[0], tc[1], r)
		}
	}
}

func TestRenderContextAndReportOverREST(t *testing.T) {
	c := renderSolo(t)
	ids := renderBillingRows(t, c)
	srv := renderStartAPI(t, c)

	cli := th.DecodeJSON[struct {
		Data []renderPick `json:"data"`
	}](t, c.Lore("context", "--for-file", "src/billing/invoice.go", "--json"))
	rep := srv.post(t, "/v1/context/fetch", []byte(`{"for_file":"src/billing/invoice.go"}`))
	if rep.status != 200 {
		t.Fatalf("REST context.fetch: %d %s", rep.status, rep.body)
	}
	var rest struct {
		Data []renderPick `json:"data"`
	}
	if err := json.Unmarshal(rep.body, &rest); err != nil {
		t.Fatalf("REST answer is {data: picks}: %v %s", err, rep.body)
	}
	if len(rest.Data) != len(cli.Data) || len(rest.Data) == 0 {
		t.Fatalf("REST and CLI pick the same rows: rest %+v cli %+v", rest.Data, cli.Data)
	}
	for i := range cli.Data {
		if rest.Data[i] != cli.Data[i] {
			t.Errorf("pick %d: REST %+v, CLI %+v", i, rest.Data[i], cli.Data[i])
		}
	}
	if !strings.Contains(string(rep.body), ids["dirhot"]) {
		t.Errorf("REST names the folder's hotfix: %s", rep.body)
	}
	// The same refusal: no mode, two modes.
	for _, body := range []string{`{}`, `{"for_file":"a.go","topics":true}`} {
		r := srv.post(t, "/v1/context/fetch", []byte(body))
		if r.status != 400 || r.code != "E_INVALID_INPUT" {
			t.Errorf("REST %s: 400 E_INVALID_INPUT, got %d %s", body, r.status, r.body)
		}
	}
	// render --report over REST: the same numbers, nothing written.
	before := renderSnapshot(c, renderLoreMD, "AGENTS.md")
	rr := srv.post(t, "/v1/render/run", []byte(`{"report":true}`))
	if rr.status != 200 || !strings.Contains(string(rr.body), `"budget"`) {
		t.Fatalf("REST render --report answers the report: %d %s", rr.status, rr.body)
	}
	renderSameFiles(t, "REST render --report", before, renderSnapshot(c, renderLoreMD, "AGENTS.md"))
	r := srv.post(t, "/v1/render/run", []byte(`{"scopes":true}`))
	if r.status != 400 || r.code != "E_INVALID_INPUT" || renderErrCode(c.Lore("render", "--scopes")) != r.code {
		t.Errorf("REST --scopes without --report: the CLI's refusal, got %d %s", r.status, r.body)
	}
}
