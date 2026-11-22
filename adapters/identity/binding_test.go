package identity

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBoundToolSessionWritesAsTheStartedAgent pins devco:D-889: after
// `lore agent start` bound a coding-tool session (Bind), every later
// command of that session (a new shell, no LORE_ACTOR) resolves as the
// agent with its LORE_SESSION, for every identity shape and each tool that
// names its session. LORE_ACTOR and LORE_HUMAN=1 still win, another
// session of the same tool is still the detected agent, and a tool that
// names no session is never bound.
func TestBoundToolSessionWritesAsTheStartedAgent(t *testing.T) {
	ctx := context.Background()
	const actor, session = "agent:frontend", "s-0a1b2c3d"
	tools := map[string]struct {
		markers map[string]string
		id      string
	}{
		"codex":       {map[string]string{"CODEX_THREAD_ID": "thread-7"}, "thread-7"},
		"claude_code": {map[string]string{"CLAUDE_CODE_CHILD_SESSION": "1", "CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "cc-7"}, "cc-7"},
		"opencode":    {map[string]string{"OPENCODE": "1", "OPENCODE_SESSION_ID": "oc-7"}, "oc-7"},
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
	}
	for tool, tc := range tools {
		for shape, set := range shapes {
			home := t.TempDir()
			if err := Bind(home, tc.id, Binding{Tool: tool, Actor: actor, Session: session}); err != nil {
				t.Fatal(err)
			}
			resolve := func(extra map[string]string) Resolved {
				t.Helper()
				env := maps.Clone(tc.markers)
				maps.Copy(env, extra)
				in := Inputs{Home: home, Getenv: func(k string) string { return env[k] }, Detect: detectWith(env, false),
					GitEmail: func(context.Context) string { return "" }, GitName: func(context.Context) string { return "" }}
				set(&in)
				r, err := Resolve(ctx, in)
				if err != nil {
					t.Fatalf("%s/%s %v: %v", tool, shape, extra, err)
				}
				return r
			}
			r := resolve(nil)
			if r.StableKey != actor || r.Step != StepBound || r.Agent != actor || r.Kind != KindAgent || r.Session != session || r.Owner != nil {
				t.Fatalf("%s/%s: a bound session's next command is not the started agent: %+v", tool, shape, r)
			}
			if r.Detection == nil || r.Detection.Tool != tool || !strings.Contains(r.Source(), "lore agent start in this "+tool+" session") || !strings.Contains(r.Source(), "`lore agent stop`") {
				t.Fatalf("%s/%s: source %q, detection %+v", tool, shape, r.Source(), r.Detection)
			}
			if in := (shape == "org entry"); in && r.Org != "acme" {
				t.Fatalf("%s/%s: the org is lost: %+v", tool, shape, r)
			}
			if SessionOf(r) != session {
				t.Fatalf("%s/%s: SessionOf = %q, want the bound %s", tool, shape, SessionOf(r), session)
			}
			if got := resolve(map[string]string{EnvActor: "agent:other"}); got.Step != StepEnv || got.StableKey != "agent:other" {
				t.Fatalf("%s/%s: LORE_ACTOR lost to the binding: %+v", tool, shape, got)
			}
			if got := resolve(map[string]string{EnvHuman: "1"}); got.Kind == KindAgent || !got.DeclaredHuman {
				t.Fatalf("%s/%s: LORE_HUMAN=1 lost to the binding: %+v", tool, shape, got)
			}
			other := map[string]string{}
			for k := range tc.markers {
				if strings.Contains(k, "SESSION_ID") || k == "CODEX_THREAD_ID" {
					other[k] = tc.id + "-other"
				}
			}
			if got := resolve(other); got.Step != StepDetected {
				t.Fatalf("%s/%s: another %s session took the binding: %+v", tool, shape, tool, got)
			}
			if b, ok, err := Unbind(home, tool, tc.id); err != nil || !ok || b.Actor != actor || b.Session != session {
				t.Fatalf("%s/%s: unbind = %+v %v %v", tool, shape, b, ok, err)
			}
			if got := resolve(nil); got.Step != StepDetected || got.Session != "" {
				t.Fatalf("%s/%s: after unbind the session is still the agent: %+v", tool, shape, got)
			}
			if _, ok, err := Unbind(home, tool, tc.id); err != nil || ok {
				t.Fatalf("%s/%s: a second unbind = %v %v, want nothing to remove", tool, shape, ok, err)
			}
		}
	}
	// a tool that names no session (OPENCODE=1 alone) has no key to bind
	home := t.TempDir()
	if err := Bind(home, "", Binding{Tool: "opencode", Actor: actor}); err == nil {
		t.Fatal("a session-less tool was bound")
	}
}

// TestBindingFileIsPrivate: the binding file holds no tool session id
// (only its hash), is owner-only, refuses a non-agent actor, and a binding
// past bindingMaxAge is removed when the next one is written.
func TestBindingFileIsPrivate(t *testing.T) {
	home := t.TempDir()
	if err := Bind(home, "thread-secret-42", Binding{Tool: "codex", Actor: "human:mira@acme-co.example"}); err == nil {
		t.Fatal("a person's key was bound to a tool session")
	}
	old := filepath.Join(home, filepath.FromSlash(BindingsDir), "codex-000000000000000000000000.json")
	if err := os.MkdirAll(filepath.Dir(old), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte(`{"tool":"codex","actor":"agent:x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-bindingMaxAge - time.Hour)
	if err := os.Chtimes(old, stale, stale); err != nil {
		t.Fatal(err)
	}
	if err := Bind(home, "thread-secret-42", Binding{Tool: "codex", Actor: "agent:frontend", Session: "s-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("a binding past its age was kept: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(old))
	if err != nil || len(entries) != 1 {
		t.Fatalf("bindings folder: %v %v", entries, err)
	}
	path := filepath.Join(filepath.Dir(old), entries[0].Name())
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "thread-secret-42") || strings.Contains(entries[0].Name(), "thread-secret-42") {
		t.Fatalf("the tool session id is written: %s %s", entries[0].Name(), b)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != fileMode {
		t.Fatalf("binding mode: %v %v", info.Mode(), err)
	}
	if got, ok := BoundTo(home, "codex", "thread-secret-42"); !ok || got.Actor != "agent:frontend" || got.Session != "s-1" {
		t.Fatalf("BoundTo = %+v %v", got, ok)
	}
	if _, ok := BoundTo(home, "claude_code", "thread-secret-42"); ok {
		t.Fatal("one tool's binding answers for another tool")
	}
}

// TestSessionOfPrefersLORESESSION: an explicit LORE_SESSION wins over the
// session a binding gave.
func TestSessionOfPrefersLORESESSION(t *testing.T) {
	r := Resolved{Step: StepBound, Session: "s-bound"}
	t.Setenv(EnvSession, "")
	if got := SessionOf(r); got != "s-bound" {
		t.Fatalf("SessionOf = %q", got)
	}
	t.Setenv(EnvSession, "s-env")
	if got := SessionOf(r); got != "s-env" {
		t.Fatalf("SessionOf = %q, want LORE_SESSION", got)
	}
}
