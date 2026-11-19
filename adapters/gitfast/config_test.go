package gitfast

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestComposedConfigMatchesGit: a folder's config built from another
// folder's snapshot plus its own config file answers exactly as git does
// in that folder, includeIf gitdir: included (devco:D-418).
func TestComposedConfigMatchesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	must(t, os.MkdirAll(home, 0o755))
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	work, other, third := filepath.Join(root, "work", "a"), filepath.Join(root, "stores", "b"), filepath.Join(root, "work", "c")
	for _, d := range []string{work, other, third} {
		gitT(t, root, "init", "-q", d)
	}
	inc := filepath.Join(home, "work.inc")
	must(t, os.WriteFile(inc, []byte("[user]\n\temail = work@example.test\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = Home Name\n\temail = home@example.test\n[includeIf \"gitdir:"+filepath.Join(root, "work")+"/\"]\n\tpath = "+inc+"\n[url \"git@example.test:\"]\n\tinsteadOf = https://example.test/\n"), 0o644))
	local := "[core]\n\trepositoryformatversion = 0\n\tbare = false\n\thooksPath = /tmp/hooks-x//y/\n[remote \"origin\"]\n\turl = git@example.test:Acme/Repo.git   # comment\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n[Branch \"Main\"]\n\tRemote = origin\n[lore]\n\tflag\n\tquoted = \"  two  spaces ; not a comment\"\n\tesc = x\\ty\\\"z\n"
	for _, d := range []string{work, other, third} {
		must(t, os.WriteFile(filepath.Join(d, ".git", "config"), []byte(local), 0o644))
	}
	queries := [][]string{
		{"config", "--get-regexp", "."},
		{"config", "--get", "user.email"},
		{"config", "--show-scope", "--get", "user.email"},
		{"config", "--get", "remote.origin.url"},
		{"config", "--get-regexp", `^remote\..*\.(url|pushurl)$`},
		{"config", "--get", "branch.Main.remote"},
		{"config", "--get", "lore.quoted"},
		{"config", "--get", "lore.esc"},
		{"config", "--get", "lore.missing"},
		{"config", "user.name"},
		{"rev-parse", "--path-format=absolute", "--git-path", "hooks"},
	}
	ctx := context.Background()
	for _, first := range []string{work, other} {
		for _, dir := range []string{work, other, third} {
			s := New()
			if _, ok := s.Answer(ctx, first, []string{"config", "--get", "core.bare"}, nil, nil); !ok {
				t.Fatal("first snapshot not taken")
			}
			for _, q := range queries {
				rep, ok := s.Answer(ctx, dir, q, nil, nil)
				if !ok {
					t.Errorf("%v in %s not answered", q, dir)
					continue
				}
				out, code := realGitHome(t, dir, q, home)
				if !bytes.Equal(rep.Out, out) || rep.Code != code {
					t.Errorf("%v in %s (snapshot from %s)\n gitfast: %d %q\n git:     %d %q", q, dir, first, rep.Code, rep.Out, code, out)
				}
			}
			s.Close()
		}
	}
}

// realGitHome runs git with HOME (and nothing else changed) as the test set it.
func realGitHome(t *testing.T, dir string, args []string, home string) ([]byte, int) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+home, "LC_ALL=C")
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok {
		return out, ee.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return out, 0
}

// must fails the test on err.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
