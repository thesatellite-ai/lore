package identity

// agentdisplay_test.go — issue #113 (devco:D-900): identity.agent_display
// decides how a detected agent's display name reads before anything is
// written; the stable key (and so the actor id) is the same in every mode.

import (
	"context"
	"testing"
)

func TestAgentDisplayModes(t *testing.T) {
	ctx := context.Background()
	codex := map[string]string{"CODEX_THREAD_ID": "t-1"}
	in := func(mode AgentDisplay) Inputs {
		return Inputs{Home: t.TempDir(), Getenv: func(k string) string { return codex[k] }, Config: Config{Identity: Entry{Key: "human:tomas@agency.example", Name: "tomas"}},
			Detect: detectWith(codex, true), AgentDisplay: mode}
	}
	want := map[AgentDisplay]string{"": "tomas via codex", AgentDisplayTool: "tomas via codex", AgentDisplayAgent: "tomas (agent)", AgentDisplayPerson: "tomas"}
	var key string
	for mode, name := range want {
		r, err := Resolve(ctx, in(mode))
		if err != nil {
			t.Fatal(err)
		}
		if r.DisplayName != name {
			t.Errorf("mode %q: display %q, want %q", mode, r.DisplayName, name)
		}
		if key == "" {
			key = r.StableKey
		}
		if r.StableKey != key || r.Label != "codex" {
			t.Errorf("mode %q changed the key or label: %+v", mode, r)
		}
		if r.HidesTool() != mode.HidesTool() {
			t.Errorf("mode %q: HidesTool %v", mode, r.HidesTool())
		}
		// the collision rule's widened key keeps the mode
		if w, ok, err := WidenAgentKey(r); err != nil || !ok || w.DisplayName != name {
			t.Errorf("mode %q: widened %+v ok=%v err=%v", mode, w, ok, err)
		}
	}

	// WithAgentDisplay only hides more, and never touches anyone else
	r, _ := Resolve(ctx, in(AgentDisplayAgent))
	if got := WithAgentDisplay(r, AgentDisplayTool); got.DisplayName != "tomas (agent)" {
		t.Errorf("a looser mode loosened: %q", got.DisplayName)
	}
	if got := WithAgentDisplay(r, AgentDisplayPerson); got.DisplayName != "tomas" || got.StableKey != r.StableKey {
		t.Errorf("a stricter mode did not apply: %+v", got)
	}
	person := in("")
	person.Detect = nil
	p, _ := Resolve(ctx, person)
	if got := WithAgentDisplay(p, AgentDisplayAgent); got.DisplayName != p.DisplayName {
		t.Errorf("the person was renamed: %q", got.DisplayName)
	}
	explicit := map[string]string{"LORE_ACTOR": "agent:frontend_2", "CODEX_THREAD_ID": "t"}
	ex := in(AgentDisplayPerson)
	ex.Getenv = func(k string) string { return explicit[k] }
	if e, _ := Resolve(ctx, ex); e.HidesTool() || e.StableKey != "agent:frontend_2" {
		t.Errorf("an agent named on purpose follows the mode: %+v", e)
	}
	if m, ok := ParseAgentDisplay(" Person "); !ok || m != AgentDisplayPerson {
		t.Errorf("parse: %q %v", m, ok)
	}
	if _, ok := ParseAgentDisplay("vendor"); ok {
		t.Error("a value outside the set parsed")
	}
}
