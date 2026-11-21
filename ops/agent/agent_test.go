package agent

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
)

type fakeHost struct {
	hooks []HookIn
}

func (f *fakeHost) CallerLabel(context.Context) (string, error) { return "@alice", nil }
func (f *fakeHost) RecordHook(_ context.Context, in HookIn)     { f.hooks = append(f.hooks, in) }

type fakeEnv struct {
	h   *fakeHost
	reg []opsinfra.ActorRegisterIn
}

func (e *fakeEnv) AgentHost(context.Context) (Host, error) { return e.h, nil }
func (e *fakeEnv) ActorRegister(_ context.Context, in opsinfra.ActorRegisterIn) (opsinfra.ActorRegisterOut, error) {
	e.reg = append(e.reg, in)
	return opsinfra.ActorRegisterOut{ID: "act_9", Kind: in.Kind, Label: in.Label, Where: "agency (agency/org)"}, nil
}
func (e *fakeEnv) ActorDeactivate(context.Context, string) (opsinfra.ActorDeactivateOut, error) {
	return opsinfra.ActorDeactivateOut{}, nil
}

// noRoster is an Env without the Roster upgrade.
type noRoster struct{ h *fakeHost }

func (e noRoster) AgentHost(context.Context) (Host, error) { return e.h, nil }

func code(err error) errs.Code {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// TestStartLabel: the roster gets kind agent with the label and owner; the
// last line is the export a shell evals.
func TestStartLabel(t *testing.T) {
	for _, owner := range []string{"", "@bob"} {
		env := &fakeEnv{h: &fakeHost{}}
		out, err := Start(context.Background(), env, StartIn{Label: "agent-frontend", Owner: owner})
		if err != nil {
			t.Fatal(err)
		}
		if len(env.reg) != 1 || env.reg[0] != (opsinfra.ActorRegisterIn{Kind: opsinfra.RegisterKindAgent, Label: "agent-frontend", Owner: owner}) {
			t.Fatalf("register: %+v", env.reg)
		}
		wantOwner := owner
		if owner == "" {
			wantOwner = "@alice"
		}
		lines := strings.Split(strings.TrimSuffix(out.Text(), "\n"), "\n")
		if len(lines) != 2 || !strings.Contains(lines[0], "act_9 agent agent-frontend (owner "+wantOwner+") registered in the roster of agency (agency/org)") {
			t.Fatalf("text: %q", out.Text())
		}
		if !regexp.MustCompile(`^export LORE_ACTOR=agent:agent-frontend LORE_SESSION=s-[0-9a-f]{8}$`).MatchString(lines[1]) {
			t.Fatalf("export line: %q", lines[1])
		}
	}
}

// TestStartRefusals: every refusal is E_INVALID_INPUT (E_UNSUPPORTED
// without a roster) and registers nobody.
func TestStartRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		in   StartIn
		want errs.Code
	}{
		"no label":         {StartIn{}, errs.InvalidInput},
		"upper-case label": {StartIn{Label: "Agent"}, errs.InvalidInput},
		"label with space": {StartIn{Label: "a b"}, errs.InvalidInput},
		"label too long":   {StartIn{Label: strings.Repeat("a", 64)}, errs.InvalidInput},
		"label and tool":   {StartIn{Label: "x", Tool: "codex"}, errs.InvalidInput},
		"session only":     {StartIn{Session: "abc"}, errs.InvalidInput},
	} {
		env := &fakeEnv{h: &fakeHost{}}
		if _, err := Start(context.Background(), env, tc.in); code(err) != tc.want {
			t.Fatalf("%s: got %v", name, err)
		}
		if len(env.reg) != 0 || len(env.h.hooks) != 0 {
			t.Fatalf("%s: side effects %+v %+v", name, env.reg, env.h.hooks)
		}
	}
	if _, err := Start(context.Background(), noRoster{h: &fakeHost{}}, StartIn{Label: "x"}); code(err) != errs.Unsupported {
		t.Fatalf("no roster: %v", err)
	}
}

// TestStartHook: --tool hands its fields to the host, prints nothing and
// never fails; it never registers.
func TestStartHook(t *testing.T) {
	env := &fakeEnv{h: &fakeHost{}}
	out, err := Start(context.Background(), env, StartIn{Tool: "claude_code", Session: "s1", Model: "m", HookStdin: true})
	if err != nil || out.Text() != "" || !out.Hook {
		t.Fatalf("out %+v err %v", out, err)
	}
	if len(env.h.hooks) != 1 || env.h.hooks[0] != (HookIn{Tool: "claude_code", Session: "s1", Model: "m", Stdin: true}) || len(env.reg) != 0 {
		t.Fatalf("hooks %+v reg %+v", env.h.hooks, env.reg)
	}
}

// toolHost is a Host that runs under a detected coding tool (ToolHost).
type toolHost struct {
	fakeHost
	tool string
}

func (h *toolHost) CallerTool(context.Context) string { return h.tool }

// toolEnv hands out a toolHost.
type toolEnv struct {
	fakeEnv
	th *toolHost
}

func (e *toolEnv) AgentHost(context.Context) (Host, error) { return e.th, nil }

// TestStartInsideAToolSaysThePrefix pins devco:D-850: a coding tool runs
// each command in a new shell, so inside one the printed export reaches no
// later command and that command is credited to "<owner> via <tool>"; the
// output says so and gives the per-command prefix, and the export stays
// the last line (eval scripts take it with tail -1). The JSON output is
// unchanged.
func TestStartInsideAToolSaysThePrefix(t *testing.T) {
	env := &toolEnv{th: &toolHost{tool: "codex"}}
	out, err := Start(context.Background(), env, StartIn{Label: "frontend_2"})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.Text(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines inside a tool: %q", out.Text())
	}
	prefix := "LORE_ACTOR=agent:frontend_2 LORE_SESSION=" + out.Session
	if !strings.Contains(lines[1], "codex runs each command in a new shell") || !strings.Contains(lines[1], "start every lore command of this agent with "+prefix) {
		t.Fatalf("tool line: %q", lines[1])
	}
	if lines[2] != "export "+prefix {
		t.Fatalf("the export is not the last line: %q", lines[2])
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "codex") {
		t.Fatalf("the tool leaked into the JSON output: %s", b)
	}
	// outside a tool: the two lines as before
	env.th.tool = ""
	if out, _ = Start(context.Background(), env, StartIn{Label: "frontend_2"}); strings.Count(out.Text(), "\n") != 2 {
		t.Fatalf("outside a tool: %q", out.Text())
	}
}
