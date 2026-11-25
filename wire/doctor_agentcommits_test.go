package wire

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// agentCommitGit runs git in dir with an isolated config and the extra
// environment (an author for commit-tree), returning trimmed stdout.
func agentCommitGit(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".nogit"), "GIT_CONFIG_NOSYSTEM=1"), env...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// agentCommitOn appends one empty-tree commit signed name <email> with msg
// to ref.
func agentCommitOn(t *testing.T, dir, ref, name, email, msg string) {
	t.Helper()
	tree := agentCommitGit(t, dir, nil, "mktree")
	args := []string{"commit-tree", tree, "-m", msg}
	if parent, err := exec.Command("git", "-C", dir, "rev-parse", "-q", "--verify", ref).Output(); err == nil {
		args = append(args, "-p", strings.TrimSpace(string(parent)))
	}
	env := []string{"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email, "GIT_COMMITTER_NAME=" + name, "GIT_COMMITTER_EMAIL=" + email}
	oid := agentCommitGit(t, dir, env, args...)
	agentCommitGit(t, dir, nil, "update-ref", ref, oid)
}

// TestDoctorAgentCommitsCountsAndRewrites is devco:D-983 (H-248): commits
// an older lore signed as an agent on refs/lore/* are counted per ref with
// an example, a person's commit is not, and the printed rewrite, run as
// printed, leaves no commit naming an agent.
func TestDoctorAgentCommitsCountsAndRewrites(t *testing.T) {
	dir := t.TempDir()
	agentCommitGit(t, dir, nil, "init", "-q")
	const signals, live = "refs/lore/signals/main", "refs/lore/live"
	agentCommitOn(t, dir, signals, "Alice Example", "alice@example.com", "claim one")
	agentCommitOn(t, dir, signals, "Alice Example via codex", "agent.auto/codex/abc123@lore.invalid", "claim two")
	agentCommitOn(t, dir, signals, "Alice Example", "alice@example.com", "claim three\n\nLore-Agent: codex (agent:auto/codex/abc123)")
	agentCommitOn(t, dir, live, "Alice Example", "alice@example.com", "live row")

	counts := agentCommitCounts(t.Context(), dir, agentCommitRefsRepo)
	if len(counts) != 1 || counts[0].ref != signals || counts[0].count != 2 || !strings.Contains(counts[0].example, "codex") {
		t.Fatalf("counts = %+v, want %s with 2 agent commits and an example naming the agent (and nothing on %s)", counts, signals, live)
	}
	if fix := agentCommitsFix(dir, []string{signals}, "Alice Via Codex", "agent.auto/codex/x@lore.invalid"); fix != "" {
		t.Fatalf("a rewrite to an agent identity was printed: %s", fix)
	}
	fix := agentCommitsFix(dir, []string{signals}, "Alice Example", "alice@example.com")
	if !strings.Contains(fix, "filter-repo --force --refs "+signals) || !strings.Contains(fix, "push --force origin "+signals) {
		t.Fatalf("fix = %s, want filter-repo over the ref and a forced push", fix)
	}
	if _, err := exec.LookPath("git-filter-repo"); err != nil {
		t.Log("git filter-repo is not installed: the printed rewrite is not run")
		return
	}
	rewrite, _, _ := strings.Cut(fix, " && ") // the push needs a remote
	cmd := exec.Command("sh", "-c", rewrite)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".nogit"), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the printed rewrite failed: %v\n%s\n%s", err, rewrite, out)
	}
	if left := agentCommitCounts(t.Context(), dir, agentCommitRefsRepo); len(left) != 0 {
		t.Fatalf("after the printed rewrite: %+v", left)
	}
	got := agentCommitGit(t, dir, nil, "log", "--format=%an <%ae> | %cn <%ce> | %s", signals)
	want := "Alice Example <alice@example.com> | Alice Example <alice@example.com> | claim three\n" +
		"Alice Example <alice@example.com> | Alice Example <alice@example.com> | claim two\n" +
		"Alice Example <alice@example.com> | Alice Example <alice@example.com> | claim one"
	if got != want {
		t.Fatalf("rewritten history:\n%s\nwant:\n%s", got, want)
	}
}
