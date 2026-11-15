package wire

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/model/errs"
)

// noCIEnv is a CI environment with nothing set (no push range).
func noCIEnv(string) string { return "" }

// gitHead is HEAD's sha in dir.
func gitHead(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	cmd.Env = gitenv.Clean(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// TestCIScansEveryCommitOfAPushOnTheDefaultBranch (devco D-330): a push of
// several commits made past the hooks (--no-verify) straight to the
// default branch, with the key in an earlier commit and a harmless change
// last, fails `lore check leaks --ci` whichever way the range is named:
// --since <the last commit CI checked>, the GitHub Actions event payload's
// before, GitLab's CI_COMMIT_BEFORE_SHA, or nothing (the fallback reaches
// back further than one commit; here the short history means every
// tracked file). A range that starts after the key passes (the key is not
// new there), and a --since the clone lacks is E_INVALID_INPUT naming the
// fetch. Break-verified: on origin/dev 391c213a (base: HEAD's first
// parent) every refusal case passed.
func TestCIScansEveryCommitOfAPushOnTheDefaultBranch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := "AKIA" + strings.Repeat("W", 16) // built at run time, never a literal key
	dir := t.TempDir()
	gitInitIn(t, dir)
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	commit := func() string {
		t.Helper()
		for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@example.test", "commit", "-q", "--no-verify", "-m", "c"}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			cmd.Env = gitenv.Clean(os.Environ())
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v %s", args, err, out)
			}
		}
		return gitHead(t, dir)
	}
	write("README.md", "# gadgets\n")
	seen := commit() // the last commit CI checked
	write("deploy.env.example", "AWS_ACCESS_KEY_ID="+key+"\n")
	withKey := commit()
	write("README.md", "# gadgets, v2\n")
	commit() // the push's last commit touches only the README

	event := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(event, []byte(`{"before":"`+seen+`","after":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	refused := map[string]struct {
		since  string
		getenv func(string) string
	}{
		"--since":                   {seen, noCIEnv},
		"github event before":       {"", env(map[string]string{envGitHubActions: "true", envGitHubEventPath: event})},
		"gitlab before sha":         {"", env(map[string]string{envGitLabCI: "true", envGitLabBefore: seen})},
		"no range named (fallback)": {"", noCIEnv},
		"unknown env sha skipped": {"", env(map[string]string{envGitLabCI: "true",
			envGitLabBefore: strings.Repeat("ab", 20)})},
		"all-zero before (new branch)": {"", env(map[string]string{envGitLabCI: "true", envGitLabBefore: strings.Repeat("0", 40)})},
	}
	for name, c := range refused {
		err := checkCISecrets(ctx, dir, c.since, c.getenv)
		if !errs.Has(err, errs.LeakBlocked) {
			t.Errorf("%s: err = %v, want E_LEAK_BLOCKED for a key in an earlier commit of the push", name, err)
			continue
		}
		if !strings.Contains(err.Error(), "deploy.env.example:1") || strings.Contains(err.Error(), key) {
			t.Errorf("%s: refusal must name the file and never the value:\n%v", name, err)
		}
	}
	if err := checkCISecrets(ctx, dir, withKey, noCIEnv); err != nil {
		t.Errorf("--since after the key: %v, want a pass (the key is not new there)", err)
	}
	err := checkCISecrets(ctx, dir, strings.Repeat("cd", 20), noCIEnv)
	if !errs.Has(err, errs.InvalidInput) || !strings.Contains(hintOf(err), "fetch-depth: 0") {
		t.Errorf("--since the clone lacks: err = %v (hint %q), want E_INVALID_INPUT naming the fetch", err, hintOf(err))
	}
}

// TestCIEnvSince pins which CI variables name the push range, in order.
func TestCIEnvSince(t *testing.T) {
	t.Parallel()
	a, b, c := strings.Repeat("a1", 20), strings.Repeat("b2", 20), strings.Repeat("c3", 20)
	event := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(event, []byte(`{"before":"`+a+`","pull_request":{"base":{"sha":"`+b+`"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	kv := map[string]string{envGitHubActions: "true", envGitHubEventPath: event, envGitLabCI: "true", envGitLabMRBase: c, envGitLabBefore: "not-a-sha"}
	got := strings.Join(ciEnvSince(func(k string) string { return kv[k] }), ",")
	if want := b + "," + a + "," + c; got != want {
		t.Fatalf("ciEnvSince = %s, want %s", got, want)
	}
	if got := ciEnvSince(func(string) string { return "" }); len(got) != 0 {
		t.Fatalf("no CI: %v", got)
	}
}
