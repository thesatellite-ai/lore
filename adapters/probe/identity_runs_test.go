package probe

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// doctor --all reads the identity of every repo, so one identity read is
// one git run: two runs per repo (user.name, then user.email) pushed a
// doctor of eight repos over its git budget. Break-verified: the old
// one-run-per-key read fails the count. The last value of a key wins, as
// with `git config --get`.
func TestIdentityIsOneGitRun(t *testing.T) {
	s := newSandbox(t)
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	log := filepath.Join(t.TempDir(), "git.log")
	fakeBin(t, s.bin, "git", `echo "$*" >> "`+log+`"`+"\n"+`exec "`+real+`" "$@"`)
	repo := t.TempDir()
	gitT(t, repo, "init", "--quiet", ".")
	gitT(t, s.home, "config", "--global", "user.name", "Mira")
	gitT(t, s.home, "config", "--global", "user.email", "mira@agency.example")
	gitT(t, repo, "config", "user.email", "mira@acme-hub.example")
	_ = os.Remove(log)

	p := s.prober(Options{})
	id := p.RepoIdentity(ctxT(t), repo)
	if id.Class != ClassOK || id.Name != "Mira" || id.Email != "mira@acme-hub.example" {
		t.Errorf("repo identity (the repo's email wins over the global one): %+v", id)
	}
	b, _ := os.ReadFile(log)
	if runs := strings.Count(string(b), "\n"); runs != 1 {
		t.Errorf("one identity read ran git %d times (want 1):\n%s", runs, b)
	}

	empty := t.TempDir()
	gitT(t, empty, "init", "--quiet", ".")
	gitT(t, s.home, "config", "--global", "--unset", "user.name")
	gitT(t, s.home, "config", "--global", "--unset", "user.email")
	if id := p.RepoIdentity(ctxT(t), empty); id.Class != ClassOK || id.Name != "" || id.Email != "" {
		t.Errorf("unset identity is ok and empty: %+v", id)
	}
}
