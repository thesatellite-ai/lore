package wire

// storeidentity_agent_test.go — owner bug 12: under an agent (Claude
// Code), a store clone's git identity, lore's commit identity and the
// hand-edit author are the PERSON (lore whoami's person), never "<person>
// via <tool> <agent.auto/…@lore.invalid>"; the agent goes into the
// Lore-Agent trailer. doctor accepts any of the person's emails set by
// hand and flags an agent identity.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitcommit"
)

// agentEnv makes this process look like Claude Code to agent detection
// (the strong signal), with no LORE_ACTOR/LORE_HUMAN override.
func agentEnv(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{"CLAUDE_CODE_CHILD_SESSION": "1", "CLAUDECODE": "1", "LORE_ACTOR": "", "LORE_HUMAN": "", "LORE_NO_DETECT": ""} {
		t.Setenv(k, v)
	}
}

func TestStoreIdentityUnderAgentIsThePerson(t *testing.T) {
	agentEnv(t)
	home := t.TempDir()
	writeConfig(t, home, "identity: {key: \"human:una@example.test\", name: Una Person}\n")
	s := storeSigner{opts: Options{Home: home, RepoDir: gitRepo(t)}}
	ctx := context.Background()
	who, err := resolveIdentity(ctx, s.opts)
	if err != nil {
		t.Fatal(err)
	}
	if who.Agent == "" || !strings.Contains(who.DisplayName, viaTool) {
		t.Fatalf("detection did not see the agent (the case under test): %+v", who)
	}
	if sig := s.fallback(ctx); sig.Name != "Una Person" || sig.Email != "una@example.test" {
		t.Fatalf("commit/hand-edit identity = %+v, want the person", sig)
	}
	id := s.cloneIdentity(ctx, "git@example.test:una/lore-me.git")
	if id.Name != "Una Person" || id.Email != "una@example.test" || agentSignature(id.Name, id.Email) {
		t.Fatalf("clone identity = %+v, want the person", id)
	}
	tr := s.agentTrailers()()
	if len(tr) != 1 || !strings.HasPrefix(tr[0], gitcommit.TrailerAgent+": claude_code (agent:auto/claude_code/") {
		t.Fatalf("trailers = %q, want Lore-Agent naming the agent", tr)
	}

	// lore's own commit in a clone with no git identity: author is the
	// person, the agent is in the trailer
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	env, err := s.envFor(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(env, "\n"); strings.Contains(joined, "lore.invalid") || strings.Contains(joined, viaTool) {
		t.Fatalf("commit env names the agent: %q", env)
	}
}

func TestAgentSignature(t *testing.T) {
	// a key with no address (an agent key with no person) is never written
	// into a clone
	if !agentSignature("Una Person via claude_code", "agent.auto/claude_code/abcdef@lore.invalid") ||
		!agentSignature("x", "agent.ci@LORE.invalid") || !agentSignature("Una via codex", "una@example.test") ||
		agentSignature("una", "una@example.test") {
		t.Fatal("agentSignature")
	}
}

func TestCloneIdentityAccepted(t *testing.T) {
	mine := []string{"una@example.test", "una@old.example"}
	cases := []struct {
		got, want string
		org, ok   bool
	}{
		{"una@example.test", "una@example.test", false, true},
		{"UNA@Example.test", "una@example.test", false, true},
		{"una@old.example", "una@example.test", false, true}, // the person's earlier email, set by hand
		{"una@old.example", "una@acme.example", true, false}, // an org store wants the org identity
		{"una@acme.example", "una@acme.example", true, true},
		{"someone@else.example", "una@example.test", false, false},
		{"", "una@example.test", false, false},
	}
	for _, c := range cases {
		if got := cloneIdentityAccepted(c.got, c.want, mine, c.org); got != c.ok {
			t.Errorf("%q want %q org=%v: %v, want %v", c.got, c.want, c.org, got, c.ok)
		}
	}
}

// gitT runs git in dir with an isolated config.
func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".nogit"), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}
