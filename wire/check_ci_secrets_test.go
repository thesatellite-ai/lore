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

// TestCIScansTheCheckoutForSecrets (devco R-12): `lore check leaks --ci` is
// the documented backstop for a commit or push that skipped the hooks
// (--no-verify), so it scans the content and names of the files the last
// push's commits add or change (base: --since, the commit CI checked last) with the secret rules: a key in a committed file and a file named
// after a key are both refused, each named with its rule and hash, never
// the value; the file finding carries its `lore secrets allow --file` line.
// A clean checkout passes, and a value the same file already held at the
// base is not new (the hooks' history rule), so the next commit passes. Break-verified: with checkCISecrets returning nil the
// refusal case passes the check.
func TestCIScansTheCheckoutForSecrets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := "AKIA" + strings.Repeat("Q", 16) // built at run time, never a literal key
	nameKey := "AKIA" + strings.Repeat("Z", 16)
	dir := t.TempDir()
	gitInitIn(t, dir)
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	commit := func() {
		t.Helper()
		for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@example.test", "commit", "-q", "--no-verify", "-m", "c"}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			cmd.Env = gitenv.Clean(os.Environ())
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v %s", args, err, out)
			}
		}
	}
	write("README.md", "# widgets\n")
	commit()
	if err := checkCISecrets(ctx, dir, "", noCIEnv); err != nil {
		t.Fatalf("a clean checkout was refused: %v", err)
	}
	write("deploy.env.example", "AWS_ACCESS_KEY_ID="+key+"\n")
	write("cfg-"+nameKey+".txt", "nothing here\n")
	commit()
	err := checkCISecrets(ctx, dir, "", noCIEnv)
	if !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("err = %v, want E_LEAK_BLOCKED for a key pushed past the hooks", err)
	}
	msg := err.Error() + "\nhint: " + hintOf(err)
	for _, want := range []string{"deploy.env.example:1", "not a secret? lore secrets allow --file deploy.env.example --rule ", "secret in the file name", "cfg-<sha256:", "--no-verify"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the CI refusal misses %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, key) || strings.Contains(msg, nameKey) {
		t.Fatalf("the CI refusal printed a value:\n%s", msg)
	}
	seen := gitHead(t, dir) // the last commit CI checked
	write("deploy.env.example", "# example\nAWS_ACCESS_KEY_ID="+key+"\n")
	write("README.md", "# widgets, v2\n")
	commit()
	if err := checkCISecrets(ctx, dir, seen, noCIEnv); err != nil {
		t.Fatalf("a value the file already held at the base was refused again: %v", err)
	}
}

// TestCIRefusalSaysStoreAllowsAreNotSeen (devco D-803, owner answer 1): a
// linked repo saves `lore secrets allow` in its store folder, which a CI
// clone does not have, so CI keeps reporting a value the hooks let through.
// The refusal's hint says so and names the committed .gitleaksignore line;
// that line must really let CI pass, or the hint would print a step that
// does not work. Break-verified: without ciStoreAllowNote the hint misses
// the note.
func TestCIRefusalSaysStoreAllowsAreNotSeen(t *testing.T) {
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
	commit := func() {
		t.Helper()
		for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@example.test", "commit", "-q", "--no-verify", "-m", "c"}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			cmd.Env = gitenv.Clean(os.Environ())
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v %s", args, err, out)
			}
		}
	}
	write("fixture.env", "AWS_ACCESS_KEY_ID="+key+"\n")
	commit()
	err := checkCISecrets(ctx, dir, "", noCIEnv)
	if !errs.Has(err, errs.LeakBlocked) {
		t.Fatalf("err = %v, want E_LEAK_BLOCKED", err)
	}
	hint := hintOf(err)
	for _, want := range []string{"linked to a store folder", "CI cannot read", "pre-push hooks", ".gitleaksignore as <path>:<rule>:<line>"} {
		if !strings.Contains(hint, want) {
			t.Errorf("the CI hint misses %q:\n%s", want, hint)
		}
	}
	_, after, ok := strings.Cut(err.Error(), "--rule ")
	if !ok {
		t.Fatalf("the refusal names no rule:\n%v", err)
	}
	rule, _, _ := strings.Cut(after, " ")
	write(".gitleaksignore", "fixture.env:"+rule+":1\n")
	commit()
	if err := checkCISecrets(ctx, dir, "", noCIEnv); err != nil {
		t.Fatalf("the .gitleaksignore line the hint names did not let CI pass: %v", err)
	}
}
