package app

// ONBOARDING_DESIGN 4.10 / conflict 10.2: `lore init` and the registry's
// `lore repo init` (repo.init, the word the setup page calls) are one path.
// Each runs in its own fresh checkout of the same shape; the stdout (ids
// masked), the stderr, the rows written and the .gitignore must match.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/testenv"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// initIDs are the minted ids an init prints (they differ per run).
var initIDs = regexp.MustCompile(`\b(prj|rep)_[0-9A-Za-z]+`)

// initResult is what one init left behind, ids masked.
type initResult struct {
	code           int
	stdout, stderr string
	tables, ignore string
}

// freshCheckout is an empty git repo named acme-hub, made the working dir.
func freshCheckout(t *testing.T, git, parent string) string {
	t.Helper()
	dir := filepath.Join(parent, "acme-hub")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	cmd := &exec.Cmd{Path: git, Args: []string{git, "-C", dir, "init", "-q", "-b", "main"}}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	t.Chdir(dir)
	return dir
}

// runInit runs args in a fresh checkout under its own temp folder.
func runInit(t *testing.T, git string, args ...string) initResult {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := freshCheckout(t, git, parent)
	code, out, errOut := runCaptured(t, args...)
	var tables []string
	for _, table := range []string{"projects", "repos"} {
		tables = append(tables, table+"="+strings.Repeat("x", len(rowFiles(t, dir, table))))
	}
	sort.Strings(tables)
	ignore, _ := os.ReadFile(filepath.Join(dir, ".gitignore")) // absent reads as ""
	mask := func(s string) string {
		return strings.ReplaceAll(initIDs.ReplaceAllString(s, "${1}_ID"), parent, "<parent>")
	}
	return initResult{code: code, stdout: mask(out), stderr: mask(errOut), tables: strings.Join(tables, " "), ignore: string(ignore)}
}

func TestInitCLIMatchesRepoInitOp(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	asPerson(t)
	testenv.SetHome(t, t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv(hosthooks.EnvRepo, "")
	t.Setenv(hosthooks.EnvProjectID, "")

	cli := runInit(t, git, "init", "--name=Acme-Hub")
	viaOp := runInit(t, git, "repo", "init", "--name=Acme-Hub")
	if cli.code != 0 || !strings.Contains(cli.stdout, "lore initialized") {
		t.Fatalf("lore init: exit %d\n%s\n%s", cli.code, cli.stdout, cli.stderr)
	}
	if cli != viaOp {
		t.Fatalf("lore init and lore repo init differ:\ninit:      %+v\nrepo init: %+v", cli, viaOp)
	}
}
