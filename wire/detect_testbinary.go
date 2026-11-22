package wire

// detect_testbinary.go: agent detection gives the same answer in every
// shell that runs `go test` (devco:D-908).
//
// lore detects a coding agent from environment markers and from the
// process tree above it (agentdetect). A test binary inherits both from the
// shell that started `go test`: started from an agent's shell, every test
// saw an agent above it; started from the landing gate's plain shell, none.
// A test could then pass in one shell and fail in the other (H-165, H-166:
// a person's write recorded an agent session, which put the person on the
// roster as a side effect, only when an agent was above `go test`).
//
// The same holds for the identity chain's own fallbacks: $USER (or
// %USERNAME%) names the person when nothing else does, and a bare shell
// (env -i, a service) has neither, so a test that named no person passed
// in a login shell and failed there.
//
// So in a test binary (testing.Testing(), which is never true in a shipped
// binary, AGENT_IDENTITY 10.6) lore:
//   - drops, once when the binary starts, every variable detection reads
//     (agentdetect.AllowedNames), the identity overrides LORE_ACTOR,
//     LORE_HUMAN and LORE_SESSION, and USER / USERNAME (the OS-user
//     fallback): only what a test sets itself (t.Setenv,
//     a child's cmd.Env) counts. It then sets envTestAmbientCleared, which
//     every child inherits, so a child the test starts with explicit
//     markers (an app cliWorld child re-executed as lore) keeps them;
//   - runs detection with no process-tree walk and no terminal.
//
// EnvTestAmbientAgent=1 in the shell that runs `go test` keeps the real
// inputs, to try detection by hand from inside a tool.

import (
	"os"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/machine"
	"github.com/thesatellite-ai/lore/adapters/procwalk"
	"github.com/thesatellite-ai/lore/logic/agentdetect"
)

// EnvTestAmbientAgent=1 lets a test binary see the agent of the shell that
// started it (markers, process tree, terminal). Read only in test binaries.
const EnvTestAmbientAgent = "LORE_TEST_AMBIENT_AGENT"

// envTestAmbientCleared marks a test binary's environment as already
// cleaned, so its children (which inherit it) keep the markers the test
// gave them.
const envTestAmbientCleared = "LORE_TEST_AMBIENT_CLEARED"

func init() {
	if testing.Testing() {
		clearAmbientIdentity()
	}
}

// ambientIdentityEnv is every variable clearAmbientIdentity drops: the ones
// agent and CI detection read, the identity overrides and the OS-user
// fallback (identity.OSUserEnv).
func ambientIdentityEnv() []string {
	return append(append(agentdetect.AllowedNames(nil), identity.EnvActor, identity.EnvHuman, identity.EnvSession), identity.OSUserEnv...)
}

// clearAmbientIdentity drops ambientIdentityEnv from this process unless
// the shell opted in (EnvTestAmbientAgent=1) or a parent test binary
// already cleaned it (envTestAmbientCleared).
func clearAmbientIdentity() {
	if hermeticOff() {
		return
	}
	if _, done := os.LookupEnv(envTestAmbientCleared); done {
		return
	}
	for _, k := range ambientIdentityEnv() {
		_ = os.Unsetenv(k) // Unsetenv fails only on a malformed name; the names are constants
	}
	_ = os.Setenv(envTestAmbientCleared, "1")
}

// hermeticOff reports EnvTestAmbientAgent=1: the real detection inputs in
// a test binary.
func hermeticOff() bool { return os.Getenv(EnvTestAmbientAgent) == "1" }

// detectInputs are DetectAgent's inputs: the allowlisted environment
// always; the terminal and the process-tree walk only when hermetic is
// false (a shipped binary, or a test binary with EnvTestAmbientAgent=1).
func detectInputs(hermetic bool) agentdetect.Inputs {
	in := agentdetect.Inputs{Getenv: os.Getenv, Files: machine.OSFiles{}}
	if !hermetic {
		in.TTY, in.Procs = stdinTTY(), procwalk.New()
	}
	return in
}
