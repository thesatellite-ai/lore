package gitenv

import (
	"slices"
	"testing"
)

// Every repository-locating name is dropped, everything else is kept in
// order: auth/transport, identity, the config layers a person or a test sets
// on purpose, and look-alike names (a prefix or suffix of a dropped one).
func TestClean(t *testing.T) {
	dropped := []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_NAMESPACE", "GIT_PREFIX",
		"GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
		"GIT_QUARANTINE_PATH", "GIT_SHALLOW_FILE", "GIT_GRAFT_FILE", "GIT_CONFIG",
	}
	kept := []string{
		"PATH=/bin", "HOME=/h",
		"GIT_SSH_COMMAND=ssh -i k", "GIT_ASKPASS=/bin/askpass", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=a", "GIT_COMMITTER_EMAIL=c@x",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=url.x.insteadOf", "GIT_CONFIG_VALUE_0=y",
		"GIT_CONFIG_PARAMETERS='a.b'='c'", "GIT_CEILING_DIRECTORIES=/tmp",
		"GIT_DIRX=1", "XGIT_DIR=1", "GIT_DI=1", "git_dir=lowercase-is-another-name",
		"NOEQUALS", "=weird", "GIT_DIR_=1",
	}
	var in []string
	for i, k := range kept {
		in = append(in, k)
		if i < len(dropped) {
			in = append(in, dropped[i]+"=/somewhere/.git")
		}
	}
	in = append(in, "GIT_DIR=") // empty value is still the variable
	got := Clean(in)
	if !slices.Equal(got, kept) {
		t.Fatalf("Clean =\n%q\nwant\n%q", got, kept)
	}
	for _, name := range dropped {
		if !RepoLocating(name) {
			t.Errorf("RepoLocating(%q) = false", name)
		}
	}
	if !slices.Equal(in[:2], []string{"PATH=/bin", "GIT_DIR=/somewhere/.git"}) {
		t.Fatalf("Clean modified its input: %q", in[:2])
	}
}

// A nil or empty env gives an empty, non-nil slice: assigned to
// exec.Cmd.Env it must mean "no variables", never "inherit everything".
func TestCleanEmptyIsNonNil(t *testing.T) {
	for _, in := range [][]string{nil, {}, {"GIT_DIR=x"}} {
		if got := Clean(in); got == nil || len(got) != 0 {
			t.Errorf("Clean(%q) = %#v, want empty non-nil", in, got)
		}
	}
}

// Names and RepoLocating describe the same closed set: every name is
// reported, and the drop list in TestClean is exactly Names.
func TestNamesMatchRepoLocating(t *testing.T) {
	names := Names()
	for _, n := range names {
		if !RepoLocating(n) {
			t.Errorf("Names has %q but RepoLocating(%q) = false", n, n)
		}
	}
	env := make([]string, 0, len(names))
	for _, n := range names {
		env = append(env, n+"=x")
	}
	if got := Clean(env); len(got) != 0 {
		t.Errorf("Clean kept %q of Names", got)
	}
	names[0] = "CHANGED"
	if Names()[0] != "GIT_DIR" {
		t.Error("Names shares its backing array between calls")
	}
}
