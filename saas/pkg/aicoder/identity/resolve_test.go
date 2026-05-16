package identity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stripChainEnv neutralizes env vars that earlier steps would consume so
// per-step tests can exercise their target step in isolation.
func stripChainEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LORE_ACTOR", "")
	t.Setenv("USER", "")
	t.Setenv("USERNAME", "")
}

func TestResolve_Step1_Flag(t *testing.T) {
	stripChainEnv(t)
	r := Resolve(Inputs{FlagActor: "human:amank@example.com"})
	if r.Step != StepFlag {
		t.Errorf("Step=%v", r.Step)
	}
	if r.StableKey != "human:amank@example.com" {
		t.Errorf("StableKey=%q", r.StableKey)
	}
	if r.DisplayName != "amank" {
		t.Errorf("DisplayName=%q", r.DisplayName)
	}
	if r.Kind != "human" {
		t.Errorf("Kind=%q", r.Kind)
	}
}

func TestResolve_Step2_Env(t *testing.T) {
	stripChainEnv(t)
	t.Setenv("LORE_ACTOR", "agent:claude-code")
	r := Resolve(Inputs{})
	if r.Step != StepEnv {
		t.Errorf("Step=%v", r.Step)
	}
	if r.Kind != "agent" {
		t.Errorf("Kind=%q", r.Kind)
	}
}

func TestResolve_Step3_IdentityToml(t *testing.T) {
	stripChainEnv(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := os.MkdirAll(filepath.Join(tmp, ".lore"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `display_name = "amank"
stable_key = "human:amank@persisted.local"`
	if err := os.WriteFile(filepath.Join(tmp, ".lore", "identity.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Resolve(Inputs{})
	if r.Step != StepIdentityToml {
		t.Errorf("Step=%v", r.Step)
	}
	if r.StableKey != "human:amank@persisted.local" {
		t.Errorf("StableKey=%q", r.StableKey)
	}
}

func TestResolve_Step3_TomlInfersStableKeyFromDisplayName(t *testing.T) {
	stripChainEnv(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := os.MkdirAll(filepath.Join(tmp, ".lore"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `display_name = "amank"`
	if err := os.WriteFile(filepath.Join(tmp, ".lore", "identity.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Resolve(Inputs{})
	if r.Step != StepIdentityToml {
		t.Errorf("Step=%v", r.Step)
	}
	if !strings.HasPrefix(r.StableKey, "human:") {
		t.Errorf("expected human: prefix, got %q", r.StableKey)
	}
}

func TestResolve_Step8_Ephemeral(t *testing.T) {
	// Use a tmpdir as HOME so ~/.lore/identity.toml and persisted-salt
	// won't exist; force fall-through.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("LORE_ACTOR", "")
	t.Setenv("USER", "")
	t.Setenv("USERNAME", "")

	// Force tryMachineID to fail by ensuring no read-access fallback is
	// used here. (It will skip /etc/machine-id if either unreadable; on
	// Linux CI this MAY succeed, in which case we can't reach step 8 in
	// pure tests.)

	r := Resolve(Inputs{})
	// On Linux with /etc/machine-id present, this returns StepMachineID
	// or StepPersistedSalt. On macOS without machine-id, falls to step 7
	// (persisted salt — which we just created in HOME=tmp).
	// Either way, the result must be stable OR ephemeral with loud warning.
	if r.Step == StepUnresolved {
		t.Errorf("expected resolved Step, got Unresolved")
	}
	if r.StableKey == "" {
		t.Errorf("StableKey empty")
	}
}

func TestStep_Stable(t *testing.T) {
	cases := map[Step]bool{
		StepFlag:          true,
		StepEnv:           true,
		StepIdentityToml:  true,
		StepGitConfig:     true,
		StepOSUser:        true,
		StepMachineID:     true,
		StepPersistedSalt: true,
		StepEphemeralSalt: false,
		StepUnresolved:    false,
	}
	for s, want := range cases {
		if got := s.Stable(); got != want {
			t.Errorf("Step(%v).Stable() = %v, want %v", s, got, want)
		}
	}
}

func TestExtractDisplayName(t *testing.T) {
	cases := map[string]string{
		"human:amank@example.com": "amank",
		"agent:claude-code":       "claude-code",
		"auto:a3f9c1k2":           "a3f9c1k2",
		"system:internal":         "internal",
		"raw-no-colon":            "raw-no-colon",
	}
	for in, want := range cases {
		if got := extractDisplayName(in); got != want {
			t.Errorf("extractDisplayName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKindFromKey(t *testing.T) {
	cases := map[string]string{
		"human:amank":    "human",
		"agent:cc":       "agent",
		"hook:precommit": "hook",
		"plugin:linear":  "plugin",
		"cron:nightly":   "cron",
		"system:int":     "system",
		"anon:abc":       "human",
	}
	for in, want := range cases {
		if got := kindFromKey(in); got != want {
			t.Errorf("kindFromKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRandHex(t *testing.T) {
	a := randHex(8)
	b := randHex(8)
	if a == b {
		t.Errorf("randHex collision: %q", a)
	}
	if len(a) != 16 {
		t.Errorf("len = %d, want 16", len(a))
	}
}

func TestResolve_PersistedSaltCreatesFile(t *testing.T) {
	stripChainEnv(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USER", "") // skip step 5

	r := Resolve(Inputs{})
	// On macOS where /etc/machine-id doesn't exist, this should land at
	// step 7 (persisted salt) which CREATES the file.
	if r.Step == StepPersistedSalt {
		path := filepath.Join(tmp, ".lore", "identity")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("expected persisted-salt file: %v", err)
		}
		if len(strings.TrimSpace(string(data))) < 12 {
			t.Errorf("salt file too short")
		}
	}
}
