package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// TestInitPathArg: the path of `lore init <path>` is found among init's
// flags (a --name / --target value is never the path, `--x=v` takes no
// next argument, `--` ends the flags) and taken off; any other command and
// a bare init keep their arguments (CLI_REFERENCE 2.14).
func TestInitPathArg(t *testing.T) {
	cases := []struct {
		args []string
		path string
		rest []string
		ok   bool
	}{
		{[]string{"init"}, "", []string{"init"}, false},
		{[]string{"init", "repo"}, "repo", []string{"init"}, true},
		{[]string{"init", "--name", "x", "repo"}, "repo", []string{"init", "--name", "x"}, true},
		{[]string{"init", "--name=x", "repo", "--non-interactive"}, "repo", []string{"init", "--name=x", "--non-interactive"}, true},
		{[]string{"init", "--target", "codex"}, "", []string{"init", "--target", "codex"}, false},
		{[]string{"init", "--non-interactive", "../a b"}, "../a b", []string{"init", "--non-interactive"}, true},
		{[]string{"init", "--", "-odd"}, "-odd", []string{"init"}, true},
		{[]string{"memory", "add", "init"}, "", []string{"memory", "add", "init"}, false},
		{nil, "", nil, false},
	}
	for _, c := range cases {
		orig := slices.Clone(c.args)
		path, rest, ok := initPathArg(c.args)
		if path != c.path || ok != c.ok || !slices.Equal(rest, c.rest) {
			t.Errorf("%q: got (%q, %q, %v), want (%q, %q, %v)", c.args, path, rest, ok, c.path, c.rest, c.ok)
		}
		if !slices.Equal(c.args, orig) {
			t.Errorf("%q: the input was changed to %q", orig, c.args)
		}
	}
}

// TestEnterInitDir: an existing folder becomes the working directory; a
// missing path or a file is E_BAD_PATH and the directory is unchanged.
func TestEnterInitDir(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(t.TempDir())
	before, _ := os.Getwd()
	for _, p := range []string{filepath.Join(dir, "missing"), "/dev/null"} {
		if err := enterInitDir(p); !errs.Has(err, errs.BadPath) {
			t.Errorf("%s: %v, want E_BAD_PATH", p, err)
		}
		if now, _ := os.Getwd(); now != before {
			t.Errorf("%s: moved to %s", p, now)
		}
	}
	if err := enterInitDir(dir); err != nil {
		t.Fatal(err)
	}
	// compared resolved: os.Chdir does not set PWD, so Getwd names the
	// folder with its links resolved (macOS: /private/var for /var)
	now, _ := os.Getwd()
	nowReal, _ := filepath.EvalSymlinks(now)
	dirReal, _ := filepath.EvalSymlinks(dir)
	if nowReal != dirReal {
		t.Fatalf("wd %s, want %s", now, dir)
	}
}
