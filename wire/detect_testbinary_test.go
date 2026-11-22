package wire

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
)

// envAmbientProbe tells a child of this test binary which outcome
// TestAmbientIdentityChild must check: "clean" or "kept".
const envAmbientProbe = "LORE_TEST_AMBIENT_PROBE"

// ambientProbeEnv is the shell a coding agent gives `go test`: tool
// markers, the generic AI_AGENT, an OS user and identity overrides.
var ambientProbeEnv = map[string]string{
	"CLAUDECODE": "1", "CLAUDE_CODE_CHILD_SESSION": "1", "CLAUDE_CODE_SESSION_ID": "s-1",
	"AI_AGENT": "claude-code", "CODEX_THREAD_ID": "t-1", "USER": "dev", "USERNAME": "dev",
	identity.EnvHuman: "1", identity.EnvActor: "agent:x", identity.EnvSession: "s-2",
}

// TestTestBinaryIgnoresAmbientAgent (devco:D-908): a test binary started
// from a coding agent's shell resolves the same identity as one started
// from a bare shell: the agent's markers, $USER and the identity overrides
// it inherited are dropped when it starts. A child the test starts with
// explicit markers keeps them (the parent already cleaned, so cliWorld's
// claude() still detects its agent), and LORE_TEST_AMBIENT_AGENT=1 keeps
// the shell's agent. Each case runs this binary again with the shell's
// environment, so the case does not depend on the shell running the test.
func TestTestBinaryIgnoresAmbientAgent(t *testing.T) {
	t.Parallel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name  string
		extra []string
		want  string
	}{
		{"agent shell", nil, "clean"},
		{"child of a cleaned test binary", []string{envTestAmbientCleared + "=1"}, "kept"},
		{"opted in", []string{EnvTestAmbientAgent + "=1"}, "kept"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			env := []string{envAmbientProbe + "=" + c.want}
			for _, kv := range os.Environ() {
				k, _, _ := strings.Cut(kv, "=")
				if _, ours := ambientProbeEnv[k]; ours || k == envTestAmbientCleared || k == EnvTestAmbientAgent || k == envAmbientProbe {
					continue
				}
				env = append(env, kv)
			}
			for k, v := range ambientProbeEnv {
				env = append(env, k+"="+v)
			}
			cmd := exec.Command(exe, "-test.run=^TestAmbientIdentityChild$", "-test.v=true")
			cmd.Env = append(env, c.extra...)
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), "--- PASS: TestAmbientIdentityChild") {
				t.Fatalf("child (%s): %v\n%s", c.want, err, out)
			}
		})
	}
}

// TestAmbientIdentityChild is the child of TestTestBinaryIgnoresAmbientAgent;
// it skips unless that test started it.
func TestAmbientIdentityChild(t *testing.T) {
	want := os.Getenv(envAmbientProbe)
	if want == "" {
		t.Skip("run by TestTestBinaryIgnoresAmbientAgent")
	}
	r, err := DetectAgent()
	if err != nil {
		t.Fatal(err)
	}
	for k := range ambientProbeEnv {
		if got := os.Getenv(k) != ""; got != (want == "kept") {
			t.Errorf("%s set = %v, want %s", k, got, want)
		}
	}
	if agent := r.IsAgent(); agent != (want == "kept") {
		t.Errorf("agent detected = %v (%s), want %s", agent, r.Tool, want)
	}
	if want == "clean" && os.Getenv(envTestAmbientCleared) != "1" {
		t.Errorf("%s not set for the binary's children", envTestAmbientCleared)
	}
}

// TestDetectInputsHermetic: a test binary's detection walks no process
// tree and reads no terminal; a shipped binary (hermetic false) does both.
func TestDetectInputsHermetic(t *testing.T) {
	t.Parallel()
	if in := detectInputs(true); in.Procs != nil || in.TTY || in.Getenv == nil {
		t.Fatalf("hermetic inputs walk or read the terminal: procs %v tty %v", in.Procs != nil, in.TTY)
	}
	if in := detectInputs(false); in.Procs == nil || in.Getenv == nil {
		t.Fatal("real inputs lack the process-tree walk")
	}
}
