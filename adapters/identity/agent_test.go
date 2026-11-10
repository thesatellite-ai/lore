package identity

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
)

func detectWith(env map[string]string, tty bool) func() (agentdetect.Result, error) {
	return func() (agentdetect.Result, error) {
		return agentdetect.Detect(agentdetect.Inputs{Getenv: func(k string) string { return env[k] }, TTY: tty})
	}
}

// TestResolveAgentDetection pins section 3: explicit actor wins, LORE_HUMAN
// keeps the person, a tool gives agent:auto/<tool>/<owner short>, CI with
// no tool gives the system CI actor.
func TestResolveAgentDetection(t *testing.T) {
	ctx := context.Background()
	codex := map[string]string{"CODEX_THREAD_ID": "t-1", "CODEX_SESSION_ID": "s-1"}
	base := func(env map[string]string) Inputs {
		return Inputs{Home: t.TempDir(), Getenv: func(k string) string { return env[k] }, Config: Config{Identity: Entry{Key: "human:tomas@agency.example", Name: "tomas"}},
			Detect: detectWith(env, true)}
	}
	r, err := Resolve(ctx, base(codex))
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := ActorID("human:tomas@agency.example")
	want := "agent:auto/codex/" + owner.String()[4:10]
	if r.StableKey != want || r.Kind != KindAgent || r.Agent != want || r.Label != "codex" || r.DisplayName != "tomas via codex" ||
		r.Owner == nil || r.Owner.StableKey != "human:tomas@agency.example" || r.Detection.SessionID != "s-1" {
		t.Fatalf("agent: %+v", r)
	}
	again, _ := Resolve(ctx, base(codex))
	if again.StableKey != r.StableKey {
		t.Fatal("the agent key is not deterministic")
	}
	// 6.1 collision rule: the widened key carries 10 hex of the owner id and
	// keeps everything else; an explicit actor is never widened.
	if w, ok, werr := WidenAgentKey(r); werr != nil || !ok || w.StableKey != "agent:auto/codex/"+owner.String()[4:14] || w.Owner == nil || w.DisplayName != r.DisplayName {
		t.Fatalf("widen: %+v ok=%v err=%v", w, ok, werr)
	}

	explicit := map[string]string{"LORE_ACTOR": "agent:frontend_2", "CODEX_THREAD_ID": "t"}
	if r, _ = Resolve(ctx, base(explicit)); r.StableKey != "agent:frontend_2" || r.Agent != "agent:frontend_2" || r.Detection.Tool != "codex" {
		t.Fatalf("explicit: %+v", r)
	}
	if _, ok, _ := WidenAgentKey(r); ok {
		t.Fatal("an explicit LORE_ACTOR key was widened")
	}

	human := map[string]string{"LORE_HUMAN": "1", "CODEX_THREAD_ID": "t"}
	if r, _ = Resolve(ctx, base(human)); r.Kind != KindHuman || !r.DeclaredHuman || r.Agent != "" || !slices.Contains(r.Detection.Flags, FlagHumanOverAgent) {
		t.Fatalf("human: %+v", r)
	}
	odd := map[string]string{"LORE_HUMAN": "yes", "CODEX_THREAD_ID": "t"}
	if r, _ = Resolve(ctx, base(odd)); r.Kind != KindAgent || !strings.Contains(strings.Join(r.Notes, ";"), "only the value 1") {
		t.Fatalf("LORE_HUMAN=yes: %+v", r)
	}

	ci := map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_REPOSITORY": "acme/acmehub_backend"}
	if r, _ = Resolve(ctx, base(ci)); r.StableKey != "ci:github_actions/acme/acmehub_backend" || r.Kind != KindSystem || r.Agent != "" {
		t.Fatalf("ci: %+v", r)
	}
	if r, _ = Resolve(ctx, base(map[string]string{})); r.StableKey != "human:tomas@agency.example" || r.Agent != "" {
		t.Fatalf("person: %+v", r)
	}
	in := base(codex)
	in.Detect = nil
	if r, _ = Resolve(ctx, in); r.Kind != KindHuman {
		t.Fatalf("no detector: %+v", r)
	}
}
