package identity

import (
	"context"
	"maps"
	"testing"
)

// TestExplicitActorWinsInsideAnAgentSession pins devco:D-850: inside a
// coding agent's session (tool markers set) an explicit
// LORE_ACTOR=agent:<label> answers for every identity shape: no org, an
// org entry, a solo setup, a top-level identity, with LORE_HUMAN=1 or CI
// markers beside it, and under each tool whose markers detection reads.
// Detection is still recorded (whoami names the tool), it only never
// decides who writes.
func TestExplicitActorWinsInsideAnAgentSession(t *testing.T) {
	ctx := context.Background()
	const actor = "agent:frontend_2"
	tools := map[string]map[string]string{
		"codex":       {"CODEX_THREAD_ID": "t-1"},
		"claude_code": {"CLAUDE_CODE_CHILD_SESSION": "1", "CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "s-9"},
		"opencode":    {"OPENCODE": "1"},
	}
	shapes := map[string]func(in *Inputs){
		"no identity":  func(*Inputs) {},
		"top identity": func(in *Inputs) { in.Config = Config{Identity: Entry{Key: "human:mira@acme-co.example", Name: "Mira"}} },
		"org entry": func(in *Inputs) {
			in.Org = &Org{Name: "acme", Identity: Entry{Key: "human:mira@acme-co.example", Name: "Mira"}}
		},
		"solo setup": func(in *Inputs) {
			in.Setup = &SetupIdentity{Setup: "solo", Entry: Entry{Key: "human:mira@home.example", Name: "Mira"}}
		},
		"org key skip": func(in *Inputs) { in.Config = Config{Orgs: []Org{{Name: "acme", Identity: Entry{Key: actor}}}} },
		"with human=1": func(in *Inputs) { in.Config = Config{Identity: Entry{Key: "human:mira@acme-co.example"}} },
		"with CI":      func(in *Inputs) { in.Config = Config{Identity: Entry{Key: "human:mira@acme-co.example"}} },
	}
	extra := map[string]map[string]string{
		"with human=1": {EnvHuman: "1"},
		"with CI":      {"GITHUB_ACTIONS": "true", "GITHUB_REPOSITORY": "acme-co/app"},
	}
	for tool, markers := range tools {
		for shape, set := range shapes {
			env := maps.Clone(markers)
			maps.Copy(env, extra[shape])
			env[EnvActor] = actor
			in := Inputs{Home: t.TempDir(), Getenv: func(k string) string { return env[k] }, Detect: detectWith(env, false),
				GitEmail: func(context.Context) string { return "" }, GitName: func(context.Context) string { return "" }}
			set(&in)
			r, err := Resolve(ctx, in)
			if shape == "org key skip" {
				// an org's own key in LORE_ACTOR outside that org is skipped
				// (F10 rule 6): then detection may answer, never the org key
				if err != nil || r.StableKey == actor {
					t.Fatalf("%s/%s: %+v err=%v", tool, shape, r, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("%s/%s: %v", tool, shape, err)
			}
			if r.StableKey != actor || r.Step != StepEnv || r.Agent != actor || r.Kind != KindAgent || r.Owner != nil || r.DeclaredHuman {
				t.Fatalf("%s/%s: the detected agent or the person won over LORE_ACTOR: %+v", tool, shape, r)
			}
			if r.Detection == nil || r.Detection.Tool != tool {
				t.Fatalf("%s/%s: detection not recorded beside LORE_ACTOR: %+v", tool, shape, r.Detection)
			}
			if in.Org != nil && r.Org != "acme" {
				t.Fatalf("%s/%s: the org is lost: %+v", tool, shape, r)
			}
			// a detected agent's key rules never rewrite an explicit actor
			if _, ok, _ := WidenAgentKey(r); ok {
				t.Fatalf("%s/%s: LORE_ACTOR widened", tool, shape)
			}
			if _, ok, _ := ReownAgent(r, Resolved{StableKey: "human:mira@acme-co.example"}); ok {
				t.Fatalf("%s/%s: LORE_ACTOR re-owned", tool, shape)
			}
		}
	}
}
