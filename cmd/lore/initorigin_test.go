package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// TestInitRecordsOriginURL (b-flow2, v0.1.17 parity): `lore init` in a
// clone records origin's URL on the project row, so `project list` shows
// it and `project show` prints its origin line; a URL carrying a
// user:token never puts the token into the committed row file. Without
// an origin the row has no URL and the commands still work.
func TestInitRecordsOriginURL(t *testing.T) {
	token := "tok" + strings.Repeat("q", 14) // built at run time (CLAUDE.md conv. 8)
	cases := []struct {
		name, origin, want string
	}{
		{"https with a token", "https://bob:" + token + "@github.com/acme/widget.git", "https://github.com/acme/widget.git"},
		{"scp form", "git@github.com:acme/widget.git", "git@github.com:acme/widget.git"},
		{"no origin", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			git, err := exec.LookPath("git")
			if err != nil {
				t.Skip("git not installed")
			}
			t.Setenv("HOME", t.TempDir())
			t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			t.Setenv(hosthooks.EnvRepo, "")
			t.Setenv(hosthooks.EnvProjectID, "")
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"remote", "add", "origin", c.origin}} {
				if args[0] == "remote" && c.origin == "" {
					continue
				}
				cmd := &exec.Cmd{Path: git, Args: append([]string{git, "-C", dir}, args...)}
				if out, cerr := cmd.CombinedOutput(); cerr != nil {
					t.Fatalf("git %v: %v %s", args, cerr, out)
				}
			}
			t.Chdir(dir)
			if code, out, errOut := runCaptured(t, "init"); code != 0 {
				t.Fatalf("lore init: exit %d\n%s\n%s", code, out, errOut)
			}
			code, list, errOut := runCaptured(t, "project", "list")
			if code != 0 {
				t.Fatalf("project list: exit %d %s", code, errOut)
			}
			if c.want != "" && !strings.Contains(list, c.want) {
				t.Fatalf("project list lacks %q:\n%s", c.want, list)
			}
			_, js, _ := runCaptured(t, "project", "list", "--json")
			pid := firstProjectID(js)
			code, show, errOut := runCaptured(t, "project", "show", pid)
			if code != 0 {
				t.Fatalf("project show: exit %d %s", code, errOut)
			}
			if hasOrigin := strings.Contains(show, "origin:"); hasOrigin != (c.want != "") || (c.want != "" && !strings.Contains(show, c.want)) {
				t.Fatalf("project show origin line (want %q):\n%s", c.want, show)
			}
			b, err := os.ReadFile(filepath.Join(dir, ".lore", "data", "projects", pid+".json"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), token) || strings.Contains(list+show, token) {
				t.Fatal("the origin token reached the project row or the output")
			}
		})
	}
}
