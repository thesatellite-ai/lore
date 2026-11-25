package identity

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
)

// TestPolicyAgentEveryActorKind pins devco:D-991 (bug hunt H-259): every
// actor kind the schema knows goes through the one NonHuman test, so a bot
// (plugin, cron, system) or a hook is what the agents.* policies and the
// agent-written trust mark see as an agent, and only a person (human, or
// the anonymous auto key) is not. Before the fix PolicyAgent did not exist
// and Resolved.Agent stayed "" for LORE_ACTOR=plugin:<label>, so
// agents.no_write "*" let the bot write and its rules kept trust 0.50.
func TestPolicyAgentEveryActorKind(t *testing.T) {
	t.Parallel()
	for _, kind := range schema.ActorKinds() {
		key := kind + ":helper"
		if kind == KindHuman {
			key = "human:alice@acme.example"
		}
		r := fromKey(key, "", StepEnv)
		if r.Kind != kind {
			t.Fatalf("%s: the key %q reads as kind %q", kind, key, r.Kind)
		}
		wantAgent := kind != KindHuman
		if got := NonHuman(kind); got != wantAgent {
			t.Fatalf("NonHuman(%q) = %v, want %v", kind, got, wantAgent)
		}
		got := r.PolicyAgent()
		if wantAgent && got != key {
			t.Fatalf("%s: PolicyAgent() = %q, want the stable key %q (a non-person is bound by agents.*)", kind, got, key)
		}
		if !wantAgent && got != "" {
			t.Fatalf("%s: a person has no policy agent, got %q", kind, got)
		}
	}
	if NonHuman(KindAuto) || NonHuman("") || NonHuman("robot") {
		t.Fatal("an anonymous person, no kind and an unknown kind are people")
	}
	// a detected agent keeps its agent key (the tool's, not the person's)
	det := Resolved{StableKey: "human:alice@acme.example", Kind: KindHuman, Agent: "agent:auto/codex/1a2b3c"}
	if got := det.PolicyAgent(); got != det.Agent {
		t.Fatalf("a detected agent: PolicyAgent() = %q, want %q", got, det.Agent)
	}
}

// TestBotActorInOrgIsAPolicyAgent: LORE_ACTOR=<bot kind>:<label> inside an
// org (the only way a bot writes, H-116) resolves to a caller every
// agents.* policy sees, for each bot kind; LORE_ACTOR=agent:<label> keeps
// its agent key, and the org's person has none.
func TestBotActorInOrgIsAPolicyAgent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	org := &Org{Name: "acme", Identity: Entry{Key: "human:alice@acme.example", Name: "Alice"}}
	for _, actor := range []string{"plugin:helper", "cron:nightly", "system:sync", "agent:scribe", ""} {
		env := map[string]string{EnvActor: actor, EnvHuman: "1"}
		in := Inputs{Home: t.TempDir(), Org: org, Getenv: func(k string) string { return env[k] },
			GitEmail: func(context.Context) string { return "" }, GitName: func(context.Context) string { return "" }}
		r, err := Resolve(ctx, in)
		if err != nil {
			t.Fatalf("%q: %v", actor, err)
		}
		if got := r.PolicyAgent(); got != actor {
			t.Fatalf("LORE_ACTOR=%q in an org: PolicyAgent() = %q (kind %q), want %q", actor, got, r.Kind, actor)
		}
	}
}
