package opstest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/logic/gitenv"
)

// CommitOn writes files (repo-relative path → content) into the harness
// repo and commits them dated day (YYYY-MM-DD, 10:00 UTC), returning the
// commit's short sha. Why: the staleness signal (devco:D-941) compares a
// fact's verified day with when its evidence files changed in git, so a
// test needs commits on chosen days.
func (h *H) CommitOn(day string, files map[string]string) string {
	h.T.Helper()
	for p, body := range files {
		full := filepath.Join(h.Repo, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			h.T.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			h.T.Fatal(err)
		}
	}
	h.git(day, "add", "-A", "--", ".")
	h.git(day, "commit", "-q", "-m", "change on "+day)
	return h.git("", "rev-parse", "--short", "HEAD")
}

// git runs git in the harness repo with a made-up identity; day dates a
// commit.
func (h *H) git(day string, args ...string) string {
	h.T.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = h.Repo
	env := append(gitenv.Clean(os.Environ()), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_AUTHOR_NAME=Ann Example", "GIT_AUTHOR_EMAIL=ann@example.test",
		"GIT_COMMITTER_NAME=Ann Example", "GIT_COMMITTER_EMAIL=ann@example.test")
	if day != "" {
		env = append(env, "GIT_AUTHOR_DATE="+day+"T10:00:00Z", "GIT_COMMITTER_DATE="+day+"T10:00:00Z")
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.T.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
