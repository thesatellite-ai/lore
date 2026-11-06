package wire

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
)

// f14AgentConfig: acme serves github.com/client/*, and its client repos
// take rules and decisions in the repo by default (the case F14 guards).
const f14AgentConfig = "orgs:\n  - name: acme\n    identity: {key: human:a@acme.com, name: A, handle: a}\n    own: [\"github.com/acme/*\"]\n    repos: [\"github.com/client/*\"]\n    defaults:\n      client_repos: {rule: repo, decision: repo}\n"

// TestF14AgentAtATerminal (B-FLOW4-5, refusal): on cmd/lore's org
// resolution, a rule or decision an agent writes into a client repo
// without --to is refused naming the agent, also at a terminal (before
// the fix an agent with a TTY wrote into the client repo); --to repo is
// accepted; a person at a terminal gets the repo default, without a
// terminal the refusal names the missing terminal. In the org's own repo
// the identity is never resolved for this (IsAgent not called).
func TestF14AgentAtATerminal(t *testing.T) {
	ctx := context.Background()
	for _, k := range []string{"CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_REMOTE_SESSION_ID", "CODEX_THREAD_ID", "LORE_SESSION"} {
		t.Setenv(k, "")
	}
	type who struct {
		name, actor, human string
		tty                bool
		want               string // "" = the repo default, else the refusal's words
	}
	people := []who{
		{"agent at a terminal", "agent:bot", "", true, "written by an agent"},
		{"agent without a terminal", "agent:bot", "", false, "written by an agent"},
		{"person at a terminal", "", "1", true, ""},
		{"person without a terminal", "", "1", false, "written without a terminal"},
	}
	for _, table := range []row.Table{schema.TableRules, schema.TableDecisions} {
		for _, p := range people {
			t.Run(string(table)+"/"+p.name, func(t *testing.T) {
				t.Setenv("LORE_ACTOR", p.actor)
				t.Setenv("LORE_HUMAN", p.human)
				home, dir := orgTestRepo(t, "https://github.com/client/app", f14AgentConfig)
				_, r := StandaloneOptionsOrg(dir, home, dir, "test")
				r.Interactive = func() bool { return p.tty }
				q := WriteQuery{Table: table, Text: "require prepayment"}
				to, err := r.DefaultTarget(ctx, q)
				if p.want == "" {
					if err != nil || to != "" {
						t.Fatalf("DefaultTarget = %q, %v; want the repo", to, err)
					}
				} else if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), p.want) || !strings.Contains(err.Error(), "--to") {
					t.Fatalf("DefaultTarget err = %v, want E_INVALID_INPUT naming %q and --to", err, p.want)
				}
				if gerr := r.GuardRepoWrite(ctx, q, true); gerr != nil {
					t.Fatalf("--to repo: %v", gerr)
				}
			})
		}
	}

	t.Run("own repo never asks who", func(t *testing.T) {
		t.Setenv("LORE_ACTOR", "agent:bot")
		t.Setenv("LORE_HUMAN", "")
		home, dir := orgTestRepo(t, "git@github.com:acme/web.git", f14AgentConfig)
		_, r := StandaloneOptionsOrg(dir, home, dir, "test")
		r.Interactive = func() bool { return true }
		calls := 0
		r.IsAgent = func(context.Context) bool { calls++; return true }
		if _, err := r.DefaultTarget(ctx, WriteQuery{Table: schema.TableRules, Text: "x"}); err != nil {
			t.Fatal(err)
		}
		if calls != 0 {
			t.Fatalf("IsAgent called %d times in the org's own repo", calls)
		}
	})
}
