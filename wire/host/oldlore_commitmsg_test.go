package host

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// oldLoreScript plays a lore older than 0.1.18 first on PATH: no `migrate`
// command, a `check leaks` that refuses every commit of a 0.1.18 repo (the
// live ref needs a newer lore), and no `task hook commit-msg --check-leaks`.
const oldLoreScript = `#!/bin/sh
case "$1 $2 $3" in
"migrate --help "*) exit 1 ;;
"check leaks --help") exit 0 ;;
"check leaks "*) echo "ERROR: [E_OLD_LORE] this lore is too old to write live rows here" >&2; exit 1 ;;
"task hook commit-msg") [ "$4" = "--check-leaks" ] && exit 1; exit 0 ;;
esac
exit 0
`

// TestOldLoreCommitMsgLetsCodeCommitsThrough (devco:S-10): with an older
// lore first on PATH, the commit-msg block refused a README commit with
// the old lore's "too old to write live rows" error, as if the commit were
// a lore write. Now a commit that carries no row file goes through; one
// that carries a row file, or any commit in a client repo, is refused
// naming the binary the hooks ran and `doctor --fix`.
func TestOldLoreCommitMsgLetsCodeCommitsThrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	t.Parallel()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "lore"), []byte(oldLoreScript), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "-b", "main")
	row := filepath.Join(dir, ".lore", "data", "memories", "mem_01a1dd7f88e57623b8440abc44aa682f.json")
	if err := os.MkdirAll(filepath.Dir(row), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p, body string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(row, "{\n  \"_v\": 2,\n  \"body\": \"retry the flaky job once\"\n}\n")
	write(filepath.Join(dir, "README.md"), "# demo\n")
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "--no-verify", "-m", "start")
	b := hookBlocks("")[hookCommitMsg]
	if err := ensureHookBlock(filepath.Join(dir, ".git", "hooks"), hookCommitMsg, b.marker, b.lines); err != nil {
		t.Fatal(err)
	}
	commit := func(msg string) (string, error) {
		inv := gitref.Invocation{Dir: dir, Args: []string{"commit", "-q", "-am", msg}, Env: []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_NOSYSTEM=1", "PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")}}
		out, err := (gitref.Exec{}).Run(context.Background(), inv)
		return string(out), err
	}
	write(filepath.Join(dir, "README.md"), "# demo\nmore\n")
	if out, err := commit("readme touch"); err != nil {
		t.Fatalf("a README commit with an older lore on PATH was refused: %v\n%s", err, out)
	}
	write(row, "{\n  \"_v\": 2,\n  \"body\": \"retry the flaky job twice\"\n}\n")
	out, err := commit("edit note")
	if err == nil {
		t.Fatal("a commit carrying a 0.1.18 row file went through an older lore unchecked")
	}
	for _, want := range []string{filepath.Join(bin, "lore"), "is older", "doctor --fix"} {
		if !strings.Contains(out+err.Error(), want) {
			t.Fatalf("the refusal does not name %q:\n%s %v", want, out, err)
		}
	}
	gitT(t, dir, "checkout", "-q", "--", ".lore")
	gitT(t, dir, "config", "lore.clientrepo", "true")
	write(filepath.Join(dir, "README.md"), "# demo\nmore\nand more\n")
	if _, err := commit("client readme"); err == nil {
		t.Fatal("in a client repo an older lore that cannot check let a commit through")
	}
}
