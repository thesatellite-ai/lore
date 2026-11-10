package gitcommit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/thesatellite-ai/lore/logic/gitenv"
)

// gitEnv pins identity and dates so the commit is reproducible.
var gitEnv = []string{
	"GIT_AUTHOR_NAME=Mira Patel", "GIT_AUTHOR_EMAIL=mira@example.com",
	"GIT_COMMITTER_NAME=Mira Patel", "GIT_COMMITTER_EMAIL=mira@example.com",
	"GIT_AUTHOR_DATE=2026-11-04T10:00:00+05:30", "GIT_COMMITTER_DATE=2026-11-04T10:00:00+05:30",
}

func gitRepo(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath(gitProgram)
	if err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "."},
		{"-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "feat: résumé ✓"},
	} {
		cmd := &exec.Cmd{Path: bin, Args: append([]string{bin}, args...), Dir: dir, Env: append(gitenv.Clean(os.Environ()), gitEnv...)}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}

// TestResolver resolves HEAD, a short sha and the full sha to one sha, and
// describes it in v0.1.17's shape.
func TestResolver(t *testing.T) {
	t.Parallel()
	dir := gitRepo(t)
	var r Resolver
	sha, err := r.RevParse(t.Context(), dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(sha) {
		t.Fatalf("sha %q", sha)
	}
	for _, ref := range []string{sha[:7], sha} {
		got, rerr := r.RevParse(t.Context(), dir, ref)
		if rerr != nil || got != sha {
			t.Errorf("RevParse(%s) = %s %v", ref, got, rerr)
		}
	}
	subject, author, at, err := r.Describe(t.Context(), dir, sha)
	if err != nil {
		t.Fatal(err)
	}
	if subject != "feat: résumé ✓" || author != "Mira Patel <mira@example.com>" || at != "2026-11-04T10:00:00+05:30" {
		t.Errorf("describe %q %q %q", subject, author, at)
	}
	if _, err := r.RevParse(t.Context(), dir, "nosuchref"); err == nil {
		t.Error("unknown ref resolved")
	}
	if _, err := r.RevParse(t.Context(), t.TempDir(), "HEAD"); err == nil {
		t.Error("HEAD outside a repo resolved")
	}
}

// TestResolverRefusesOptions: a reference that git would read as an option
// is refused before git runs, so `--output=<file>` writes nothing.
func TestResolverRefusesOptions(t *testing.T) {
	t.Parallel()
	dir := gitRepo(t)
	target := filepath.Join(t.TempDir(), "written")
	var r Resolver
	for _, ref := range []string{"", "-", "--output=" + target, "-o" + target} {
		if got, err := r.RevParse(t.Context(), dir, ref); err == nil {
			t.Errorf("RevParse(%q) = %q, want refusal", ref, got)
		}
		if _, _, _, err := r.Describe(t.Context(), dir, ref); err == nil {
			t.Errorf("Describe(%q) accepted", ref)
		}
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("an option-shaped ref wrote %s: %v", target, err)
	}
}

// TestRunCancelled: a cancelled context stops before git runs.
func TestRunCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := run(ctx, "", "version"); err == nil {
		t.Fatal("cancelled run succeeded")
	}
}
