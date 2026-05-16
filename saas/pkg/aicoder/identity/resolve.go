package identity

// resolve.go — 8-step actor identity resolution chain (PLAN.md Round 32).
//
// Mini ALWAYS produces an actor identity. Read-only ops bypass identity
// entirely; write ops always succeed via the chain. Worst case (step 8)
// emits a loud warn and uses an ephemeral session salt.
//
// Resolution chain (highest priority first):
//
//	 1. --actor=<value>             CLI flag
//	 2. LORE_ACTOR               env var
//	 3. ~/.lore/identity.toml    persisted explicit identity
//	 4. git config user.email       project-local OR global
//	 5. $USER + hostname            OS-derived
//	 6. machine-id hash             /etc/machine-id (Linux), IOPlatformUUID (macOS), MachineGuid (Win)
//	 7. Persisted random salt       ~/.lore/identity (generated at first write)
//	 8. Ephemeral session salt      LAST RESORT, NOT persisted, loud warn
//
// Returned actor format (R32):
//
//	human:amank@example.com         named human
//	agent:claude-code               env-set AI agent
//	auto:a3f9c1k2                   synthesized stable
//	auto:ephemeral-7f3a2c           synthesized ephemeral, warn
//	anon:<random>                   --identity anonymize mode
//
// Catches: R32 (8-step chain), R37 Block 2 (actors).

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Step records which step in the chain produced the resolved identity.
// Useful for `aicoder doctor` ("source: git config user.email (step 4)").
type Step int

const (
	StepUnresolved    Step = iota
	StepFlag               // 1
	StepEnv                // 2
	StepIdentityToml       // 3
	StepGitConfig          // 4
	StepOSUser             // 5
	StepMachineID          // 6
	StepPersistedSalt      // 7
	StepEphemeralSalt      // 8 — LOUD WARN
)

func (s Step) String() string {
	switch s {
	case StepFlag:
		return "flag"
	case StepEnv:
		return "env"
	case StepIdentityToml:
		return "identity.toml"
	case StepGitConfig:
		return "git config"
	case StepOSUser:
		return "OS user"
	case StepMachineID:
		return "machine-id"
	case StepPersistedSalt:
		return "persisted-salt"
	case StepEphemeralSalt:
		return "ephemeral-salt"
	default:
		return "unresolved"
	}
}

// Stable returns true if the identity will be the same on next invocation
// from this machine. Step 8 is unstable; surfaced loud in doctor.
func (s Step) Stable() bool {
	return s != StepEphemeralSalt && s != StepUnresolved
}

// Resolved is the output of the chain.
type Resolved struct {
	StableKey   string // canonical identity used for actors.stable_key
	DisplayName string // shown in CLI output / audit log
	Kind        string // 'human' | 'agent' | 'auto' | 'anon'
	Step        Step   // which step matched
}

// Inputs are the values that may have been provided by the caller.
type Inputs struct {
	FlagActor string // --actor=<value>
}

// Resolve runs the chain. Always returns a Resolved value (never errors out
// on fundamental resolution); the caller decides whether to warn loud.
func Resolve(in Inputs) Resolved {
	// Step 1: --actor flag
	if v := strings.TrimSpace(in.FlagActor); v != "" {
		return Resolved{
			StableKey:   v,
			DisplayName: extractDisplayName(v),
			Kind:        kindFromKey(v),
			Step:        StepFlag,
		}
	}

	// Step 2: LORE_ACTOR env
	if v := strings.TrimSpace(os.Getenv("LORE_ACTOR")); v != "" {
		return Resolved{
			StableKey:   v,
			DisplayName: extractDisplayName(v),
			Kind:        kindFromKey(v),
			Step:        StepEnv,
		}
	}

	// Step 3: ~/.lore/identity.toml
	if r, ok := tryIdentityToml(); ok {
		r.Step = StepIdentityToml
		return r
	}

	// Step 4: git config user.email
	if email, ok := tryGitConfig(); ok {
		return Resolved{
			StableKey:   "human:" + email,
			DisplayName: extractDisplayName("human:" + email),
			Kind:        "human",
			Step:        StepGitConfig,
		}
	}

	// Step 5: $USER + hostname
	if r, ok := tryOSUser(); ok {
		r.Step = StepOSUser
		return r
	}

	// Step 6: machine-id
	if r, ok := tryMachineID(); ok {
		r.Step = StepMachineID
		return r
	}

	// Step 7: persisted random salt
	if r, ok := tryPersistedSalt(); ok {
		r.Step = StepPersistedSalt
		return r
	}

	// Step 8: ephemeral session salt (last resort, NOT persisted).
	rnd := randHex(6)
	return Resolved{
		StableKey:   "auto:ephemeral-" + rnd,
		DisplayName: "ephemeral-" + rnd,
		Kind:        "auto",
		Step:        StepEphemeralSalt,
	}
}

// tryIdentityToml reads ~/.lore/identity.toml.
//
// Format:
//
//	display_name = "amank"
//	stable_key = "human:amank@example.com"  (optional; computed from display_name otherwise)
func tryIdentityToml() (Resolved, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Resolved{}, false
	}
	path := filepath.Join(home, ".lore", "identity.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return Resolved{}, false
	}

	var t struct {
		DisplayName string `toml:"display_name"`
		StableKey   string `toml:"stable_key"`
	}
	if err := toml.Unmarshal(data, &t); err != nil {
		return Resolved{}, false
	}

	display := strings.TrimSpace(t.DisplayName)
	stable := strings.TrimSpace(t.StableKey)

	if stable == "" && display != "" {
		stable = "human:" + display
	}
	if stable == "" {
		return Resolved{}, false
	}
	if display == "" {
		display = extractDisplayName(stable)
	}
	return Resolved{
		StableKey:   stable,
		DisplayName: display,
		Kind:        kindFromKey(stable),
	}, true
}

// tryGitConfig runs `git config user.email`.
func tryGitConfig() (string, bool) {
	cmd := exec.Command("git", "config", "user.email")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	email := strings.TrimSpace(string(out))
	if email == "" {
		return "", false
	}
	return email, true
}

// tryOSUser composes "human:$USER@<hostname>".
func tryOSUser() (Resolved, bool) {
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME") // Windows
	}
	if user == "" {
		return Resolved{}, false
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	}
	key := fmt.Sprintf("human:%s@%s", user, host)
	return Resolved{
		StableKey:   key,
		DisplayName: user,
		Kind:        "human",
	}, true
}

// tryMachineID hashes /etc/machine-id (Linux) into "auto:<12 hex>".
// macOS / Windows variants deferred — falls through to next step on those.
func tryMachineID() (Resolved, bool) {
	candidates := []string{
		"/etc/machine-id",          // systemd
		"/var/lib/dbus/machine-id", // dbus
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		s := strings.TrimSpace(string(data))
		if s == "" {
			continue
		}
		sum := sha256.Sum256([]byte(s))
		key := "auto:" + hex.EncodeToString(sum[:])[:12]
		return Resolved{
			StableKey:   key,
			DisplayName: key[5:], // skip "auto:"
			Kind:        "auto",
		}, true
	}
	return Resolved{}, false
}

// tryPersistedSalt reads ~/.lore/identity (or creates it on first call).
// Single-line file with hex random data.
func tryPersistedSalt() (Resolved, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Resolved{}, false
	}
	dir := filepath.Join(home, ".lore")
	path := filepath.Join(dir, "identity")

	if data, err := os.ReadFile(path); err == nil {
		s := strings.TrimSpace(string(data))
		if len(s) >= 12 {
			key := "auto:" + s[:12]
			return Resolved{
				StableKey:   key,
				DisplayName: key[5:],
				Kind:        "auto",
			}, true
		}
	}

	// Create — best-effort. If create fails, fall through to step 8.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Resolved{}, false
	}
	rnd := randHex(16)
	if err := os.WriteFile(path, []byte(rnd+"\n"), 0o600); err != nil {
		return Resolved{}, false
	}
	key := "auto:" + rnd[:12]
	return Resolved{
		StableKey:   key,
		DisplayName: key[5:],
		Kind:        "auto",
	}, true
}

// extractDisplayName turns a stable_key like "human:amank@example.com" into
// a friendly "amank". Conservative: if anything looks unfamiliar, returns
// the full key (caller can override via display_name field on the actors row).
func extractDisplayName(stableKey string) string {
	parts := strings.SplitN(stableKey, ":", 2)
	if len(parts) != 2 {
		return stableKey
	}
	value := parts[1]
	// "amank@example.com" → "amank"
	if at := strings.IndexByte(value, '@'); at > 0 {
		return value[:at]
	}
	return value
}

// kindFromKey returns the Actor.kind enum from a stable_key prefix.
func kindFromKey(stableKey string) string {
	switch {
	case strings.HasPrefix(stableKey, "human:"):
		return "human"
	case strings.HasPrefix(stableKey, "agent:"):
		return "agent"
	case strings.HasPrefix(stableKey, "hook:"):
		return "hook"
	case strings.HasPrefix(stableKey, "plugin:"):
		return "plugin"
	case strings.HasPrefix(stableKey, "cron:"):
		return "cron"
	case strings.HasPrefix(stableKey, "system:"):
		return "system"
	case strings.HasPrefix(stableKey, "anon:"):
		return "human" // anon mode is still a human, just unidentified
	default:
		// Unknown prefix → treat as auto/system. Conservative.
		return "human"
	}
}

// randHex returns 2*n hex chars of crypto/rand bytes.
func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
