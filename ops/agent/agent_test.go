package agent

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
)

type fakeHost struct {
	agent bool
	hooks []HookIn
}

func (f *fakeHost) CallerIsAgent(context.Context) (bool, error) { return f.agent, nil }
func (f *fakeHost) CallerLabel(context.Context) (string, error) { return "@alice", nil }
func (f *fakeHost) RecordHook(_ context.Context, in HookIn)     { f.hooks = append(f.hooks, in) }

type fakeEnv struct {
	h   *fakeHost
	reg []opsinfra.ActorRegisterIn
}

func (e *fakeEnv) AgentHost(context.Context) (Host, error) { return e.h, nil }
func (e *fakeEnv) ActorRegister(_ context.Context, in opsinfra.ActorRegisterIn) (opsinfra.ActorRegisterOut, error) {
	e.reg = append(e.reg, in)
	return opsinfra.ActorRegisterOut{ID: "act_9", Kind: in.Kind, Label: in.Label, Where: "agency org/"}, nil
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
		if len(lines) != 2 || !strings.Contains(lines[0], "act_9 agent agent-frontend (owner "+wantOwner+") registered in agency org/") {
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
		in    StartIn
		agent bool
		want  errs.Code
	}{
		"no label":         {StartIn{}, false, errs.InvalidInput},
		"upper-case label": {StartIn{Label: "Agent"}, false, errs.InvalidInput},
		"label with space": {StartIn{Label: "a b"}, false, errs.InvalidInput},
		"label too long":   {StartIn{Label: strings.Repeat("a", 64)}, false, errs.InvalidInput},
		"agent starts one": {StartIn{Label: "x"}, true, errs.InvalidInput},
		"label and tool":   {StartIn{Label: "x", Tool: "codex"}, false, errs.InvalidInput},
		"session only":     {StartIn{Session: "abc"}, false, errs.InvalidInput},
	} {
		env := &fakeEnv{h: &fakeHost{agent: tc.agent}}
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
