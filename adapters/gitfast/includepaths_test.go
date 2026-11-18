package gitfast

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestIncludePathsMatchesGit: IncludePaths reads the include.path and
// includeIf.<cond>.path values git's `config --file --get-regexp` lists,
// in the same order, with no git process (devco:D-624); syntax it does
// not read says ok=false so the caller asks git.
func TestIncludePathsMatchesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "gitconfig")
	body := "[user]\n\tname = Pat Example\n[include]\n\tpath = ~/.gitconfig-extra ; comment\n" +
		"[includeIf \"gitdir:~/work/\"]\n\tpath = work.inc\n[IncludeIf \"hasconfig:remote.*.url:git@example.test:*/**\"]\n\tPath = \"/abs/client.inc\"\n" +
		"[core]\n\tpathx = not-an-include\n"
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := IncludePaths(f)
	if !ok {
		t.Fatal("IncludePaths refused a plain config file")
	}
	out, err := exec.Command("git", "config", "--file", f, "--get-regexp", `^include(if\..*)?\.path$`).Output()
	if err != nil {
		t.Fatalf("git config: %v", err)
	}
	var want []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		_, v, _ := strings.Cut(line, " ")
		want = append(want, v)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("IncludePaths = %q, git lists %q", got, want)
	}
	if ps, ok := IncludePaths(filepath.Join(dir, "missing")); !ok || len(ps) != 0 {
		t.Fatalf("missing file: %q ok=%v, want none and ok", ps, ok)
	}
	odd := filepath.Join(dir, "odd")
	if err := os.WriteFile(odd, []byte("[include]\n\tpath = a\\\nb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := IncludePaths(odd); ok {
		t.Fatal("a continuation line must send the caller to git")
	}
}
