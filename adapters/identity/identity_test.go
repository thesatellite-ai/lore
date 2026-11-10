package identity

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// env is a fake Getenv.
func env(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }

func git(email string) func(context.Context) string {
	return func(context.Context) string { return email }
}

// TestActorIDMatchesV017: actor ids equal the ones v0.1.17 printed in
// `lore whoami --json` for the same stable key (ids.Deterministic there).
func TestActorIDMatchesV017(t *testing.T) {
	t.Parallel()
	for key, want := range map[string]string{
		"human:alice@example.com": "act_3686b9758592753ea25bf584466bc40e",
		"agent:claude-code":       "act_a9ee93f1df1078a28b91d0d3b7b97a79",
		"human:Ünïcode@例え.jp":     "act_b314f95946797913997344731c47a6f0",
	} {
		got, err := ActorID(key)
		if err != nil || got.String() != want {
			t.Errorf("ActorID(%q) = %s %v, want %s", key, got, err, want)
		}
	}
	if _, err := Deterministic("act"); err == nil {
		t.Error("no parts accepted")
	}
}

// TestResolveChain walks the chain in order: --actor, LORE_ACTOR, the
// top-level identity, ~/.lore/identity.yaml, git email, $USER; each step
// answers only when every earlier one is empty.
func TestResolveChain(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	legacyHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(legacyHome, LegacyFile), []byte("display_name: Old Name\nstable_key: human:old@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	top := Config{Identity: Entry{Key: "human:top@example.com", Name: "Top", Handle: "topper"}}
	cases := []struct {
		name               string
		in                 Inputs
		key, display, kind string
		step               Step
	}{
		{"flag beats all", Inputs{Home: legacyHome, FlagActor: " agent:ci ", Getenv: env(map[string]string{EnvActor: "agent:env"}), Config: top, GitEmail: git("g@x")}, "agent:ci", "ci", KindAgent, StepFlag},
		{"env", Inputs{Home: legacyHome, Getenv: env(map[string]string{EnvActor: "agent:env"}), Config: top, GitEmail: git("g@x")}, "agent:env", "env", KindAgent, StepEnv},
		{"top", Inputs{Home: legacyHome, Getenv: env(nil), Config: top, GitEmail: git("g@x")}, "human:top@example.com", "Top", KindHuman, StepTop},
		{"legacy", Inputs{Home: legacyHome, Getenv: env(nil), GitEmail: git("g@x")}, "human:old@example.com", "Old Name", KindHuman, StepLegacy},
		{"git", Inputs{Home: home, Getenv: env(map[string]string{"USER": "u"}), GitEmail: git(" dev@example.com\n")}, "human:dev@example.com", "dev", KindHuman, StepGit},
		{"git with a name", Inputs{Home: home, Getenv: env(nil), GitEmail: git("dev@example.com"), GitName: git(" Dev One\n")}, "human:dev@example.com", "Dev One", KindHuman, StepGit},
		{"unknown kind is human", Inputs{Home: home, FlagActor: "robot:x", Getenv: env(nil)}, "robot:x", "x", KindHuman, StepFlag},
		{"no kind", Inputs{Home: home, FlagActor: "plain", Getenv: env(nil)}, "plain", "plain", KindHuman, StepFlag},
	}
	for _, c := range cases {
		r, err := Resolve(t.Context(), c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if r.StableKey != c.key || r.DisplayName != c.display || r.Kind != c.kind || r.Step != c.step || !r.Stable() {
			t.Errorf("%s: %+v", c.name, r)
		}
	}
	r, err := Resolve(t.Context(), Inputs{Home: home, Getenv: env(map[string]string{"USERNAME": "win"}), GitEmail: git("")})
	if err != nil {
		t.Fatal(err)
	}
	if r.Step != StepOSUser || !strings.HasPrefix(r.StableKey, "human:win@") || r.DisplayName != "win" {
		t.Errorf("os user: %+v", r)
	}
	// Nothing personal: machine id (Linux) or a persisted salt; either is
	// stable, and the salt is the same on the next run.
	r1, err := Resolve(t.Context(), Inputs{Home: home, Getenv: env(nil), GitEmail: git("")})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Resolve(t.Context(), Inputs{Home: home, Getenv: env(nil), GitEmail: git("")})
	if err != nil {
		t.Fatal(err)
	}
	if !r1.Stable() || r1.StableKey != r2.StableKey || r1.Kind != KindAuto || (r1.Step != StepMachine && r1.Step != StepSalt) {
		t.Errorf("fallback %+v / %+v", r1, r2)
	}
}

// TestResolveOrg: inside an org only an agent:<label> actor overrides the
// org entry (a human --actor is ignored with a note); an org entry without
// a key fails closed (E_IDENTITY_FAILED); outside an org, a source equal to
// an org identity is skipped with a note that never names the key.
func TestResolveOrg(t *testing.T) {
	t.Parallel()
	org := &Org{Name: "agency", Identity: Entry{Key: "human:me@agency.example", Name: "Me", Handle: "me"}}
	r, err := Resolve(t.Context(), Inputs{Home: t.TempDir(), Org: org, FlagActor: "agent:bot", Getenv: env(nil)})
	if err != nil || r.StableKey != "agent:bot" || r.Org != "agency" || r.Kind != KindAgent {
		t.Errorf("agent in org: %+v %v", r, err)
	}
	r, err = Resolve(t.Context(), Inputs{Home: t.TempDir(), Org: org, Getenv: env(map[string]string{EnvActor: "human:other@x"})})
	if err != nil || r.StableKey != org.Identity.Key || r.Step != StepOrg || r.Handle != "me" || len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "ignored") {
		t.Errorf("human actor in org: %+v %v", r, err)
	}
	for _, bare := range []string{"agent:", "agent"} {
		r, err = Resolve(t.Context(), Inputs{Home: t.TempDir(), Org: org, FlagActor: bare, Getenv: env(nil)})
		if err != nil || r.StableKey != org.Identity.Key {
			t.Errorf("%q in org: %+v %v", bare, r, err)
		}
	}
	_, err = Resolve(t.Context(), Inputs{Home: t.TempDir(), Org: &Org{Name: "agency"}, Getenv: env(nil)})
	if !errs.Has(err, errs.IdentityFailed) || !errors.Is(err, ErrOrgIdentityMissing) {
		t.Errorf("keyless org: %v", err)
	}
	cfg := Config{Orgs: []Org{*org}}
	r, err = Resolve(t.Context(), Inputs{Home: t.TempDir(), Config: cfg, Getenv: env(map[string]string{"USER": "u"}), GitEmail: git("me@agency.example")})
	if err != nil || r.Step != StepOSUser || len(r.Notes) != 1 || !strings.Contains(r.Notes[0], string(StepGit)) || strings.Contains(r.Notes[0], "me@agency") {
		t.Errorf("org key skipped: %+v %v", r, err)
	}
}

// FLAG4: the signature a store commit gets when git has no identity.
func TestCommitSignature(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		in          Resolved
		name, email string
	}{
		{Resolved{StableKey: "human:alice@example.com", DisplayName: "Alice"}, "Alice", "alice@example.com"},
		{Resolved{StableKey: "human:alice@example.com"}, "alice@example.com", "alice@example.com"},
		{Resolved{StableKey: "agent:ci", DisplayName: "CI"}, "CI", "agent.ci@lore.invalid"},
		{Resolved{StableKey: "human:bob"}, "bob", "human.bob@lore.invalid"},
		{Resolved{StableKey: "carol@example.com"}, "carol@example.com", "carol@example.com"},
		{Resolved{}, "", ""},
	} {
		if n, e := c.in.CommitSignature(); n != c.name || e != c.email {
			t.Errorf("%+v: got %q %q, want %q %q", c.in, n, e, c.name, c.email)
		}
	}
}

// TestAliasesKeepTheActor: a solo setup's aliases (earlier keys, oldest
// first) reach Resolved, and the actor id comes from the first one, so an
// email change keeps one actor; with no aliases the key itself names it.
func TestAliasesKeepTheActor(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	first, err := ActorID("human:pat@old.example")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Resolve(context.Background(), Inputs{Home: home, Getenv: env(nil),
		Setup: &SetupIdentity{Setup: "solo", Entry: Entry{Key: "human:pat@new.example", Name: "Pat"}, Aliases: []string{"human:pat@old.example", "human:pat@mid.example"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := r.ActorID(); r.StableKey != "human:pat@new.example" || r.ActorKey() != "human:pat@old.example" || got != first {
		t.Errorf("resolved %q actor key %q id %s, want the new key with the first email's id %s", r.StableKey, r.ActorKey(), got, first)
	}
	plain := Resolved{StableKey: "human:pat@new.example"}
	if own, _ := ActorID("human:pat@new.example"); plain.ActorKey() != plain.StableKey {
		t.Errorf("no aliases: actor key %q, want the stable key (%s)", plain.ActorKey(), own)
	}
}
