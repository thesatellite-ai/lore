package identity

import (
	"errors"
	"strings"
	"testing"

	"saas/pkg/aicoder/lorecfg"
)

type fakeRoster map[string]bool

func (f fakeRoster) AgentRegistered(k string) (bool, bool) { return f[k], true }

// TestResolveForOrgMatrix: org identity present / missing / malformed ×
// LORE_ACTOR unset / human / registered agent / unknown agent / bare
// "agent:" × roster checked / not checked × --actor flag.
func TestResolveForOrgMatrix(t *testing.T) {
	sh := &lorecfg.Org{Name: "northwind", Identity: lorecfg.OrgIdentity{
		Key: "human:alice@northwind.example", Name: "Alice Doe", Handle: "alice"}}
	noID := &lorecfg.Org{Name: "betalabs"}
	badID := &lorecfg.Org{Name: "x", Identity: lorecfg.OrgIdentity{Key: "human:alice"}}
	roster := fakeRoster{"agent:frontend-2": true}

	type tc struct {
		name    string
		org     *lorecfg.Org
		env     map[string]string
		flag    string
		roster  Roster
		wantKey string
		kind    string
		step    Step
		err     error
		note    string
	}
	cases := []tc{
		{name: "org identity", org: sh, wantKey: "human:alice@northwind.example", kind: KindHuman, step: StepOrgConfig},
		{name: "missing org identity refuses", org: noID, err: ErrOrgIdentityMissing},
		{name: "malformed org identity refuses", org: badID, err: errors.New("not an email")},
		{name: "human LORE_ACTOR ignored with note", org: sh, env: map[string]string{EnvActor: "human:someone@else.example"},
			wantKey: "human:alice@northwind.example", kind: KindHuman, step: StepOrgConfig, note: "ignored"},
		{name: "human LORE_ACTOR does not rescue a missing identity", org: noID, env: map[string]string{EnvActor: "human:x@y.z"}, err: ErrOrgIdentityMissing},
		{name: "agent, roster not read yet → accepted with note", org: sh, env: map[string]string{EnvActor: "agent:frontend-2", EnvSession: "s-7f2e"},
			wantKey: "agent:frontend-2", kind: KindAgent, step: StepEnv, note: "without a roster check"},
		{name: "agent on roster", org: sh, roster: roster, env: map[string]string{EnvActor: "agent:frontend-2"},
			wantKey: "agent:frontend-2", kind: KindAgent, step: StepEnv},
		{name: "agent not on roster refused", org: sh, roster: roster, env: map[string]string{EnvActor: "agent:rogue"}, err: ErrAgentNotOnRoster},
		{name: "agent works even without a human identity", org: noID, env: map[string]string{EnvActor: "agent:bot"}, wantKey: "agent:bot", kind: KindAgent, step: StepEnv, note: "without a roster check"},
		{name: "bare agent: prefix is not an agent", org: sh, env: map[string]string{EnvActor: "agent:"}, wantKey: "human:alice@northwind.example", kind: KindHuman, step: StepOrgConfig, note: "ignored"},
		{name: "auto key ignored", org: sh, env: map[string]string{EnvActor: "auto:abc"}, wantKey: "human:alice@northwind.example", kind: KindHuman, step: StepOrgConfig, note: "ignored"},
		{name: "--actor flag agent beats env", org: sh, flag: "agent:cli", env: map[string]string{EnvActor: "agent:env"}, wantKey: "agent:cli", kind: KindAgent, step: StepFlag, note: "--actor=agent:cli"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := c.env
			r, err := ResolveForOrg(OrgInputs{Org: c.org, FlagActor: c.flag, Roster: c.roster,
				Getenv: func(k string) string { return env[k] }})
			if c.err != nil {
				if err == nil || (!errors.Is(err, c.err) && !strings.Contains(err.Error(), c.err.Error())) {
					t.Fatalf("err = %v, want %v", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if r.StableKey != c.wantKey || r.Kind != c.kind || r.Step != c.step || r.Org != c.org.Name {
				t.Fatalf("got %+v", r)
			}
			if c.note != "" && !strings.Contains(strings.Join(r.Notes, "\n"), c.note) {
				t.Errorf("notes %q lack %q", r.Notes, c.note)
			}
			if c.note == "" && len(r.Notes) > 0 {
				t.Errorf("unexpected notes %q", r.Notes)
			}
		})
	}

	r, _ := ResolveForOrg(OrgInputs{Org: sh})
	if r.DisplayName != "Alice Doe" || r.Handle != "alice" || r.Step.String() != stepOrgConfigName {
		t.Errorf("display fields: %+v (step %q)", r, r.Step)
	}
	r, _ = ResolveForOrg(OrgInputs{Org: sh, Getenv: func(k string) string {
		return map[string]string{EnvActor: "agent:x", EnvSession: "s-1"}[k]
	}})
	if r.Session != "s-1" {
		t.Errorf("session = %q", r.Session)
	}
}

func TestRequireAgent(t *testing.T) {
	if err := RequireAgent(Resolved{Kind: KindAgent, StableKey: "agent:x"}); err != nil {
		t.Fatal(err)
	}
	if err := RequireAgent(Resolved{Kind: KindHuman, StableKey: "human:a@b.c"}); !errors.Is(err, ErrAgentRequired) {
		t.Fatalf("human passed RequireAgent: %v", err)
	}
}
