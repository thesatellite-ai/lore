// Package gitenv strips the inherited environment variables that would make
// a child git act on a different repository than the one lore names.
//
// Why it exists: git exports GIT_DIR, GIT_WORK_TREE, GIT_INDEX_FILE and
// friends to every hook it runs, and lore runs inside hooks (and its tests
// run inside the pre-push hook through `task check`). A child started with
// the whole parent environment then ignores its -C or working directory and
// writes to the hook's repository instead: `git -C <tempdir> init --bare .`
// turned the developer's own clone bare, `git -C <tempdir> remote add`
// rewrote its origin, and so on (public issue #6). Every place lore starts
// git builds the child environment from Clean(os.Environ()), so a repository
// is addressed by its directory only; a caller that really wants one of these
// variables (a pre-commit hook passing GIT_INDEX_FILE) appends it after.
//
// What it keeps, on purpose: auth and transport (GIT_SSH_COMMAND,
// GIT_ASKPASS, GIT_TERMINAL_PROMPT), identity (GIT_AUTHOR_*,
// GIT_COMMITTER_*), and the config layers a person or a test sets on
// purpose (GIT_CONFIG_GLOBAL, GIT_CONFIG_SYSTEM, GIT_CONFIG_NOSYSTEM,
// GIT_CONFIG_COUNT / _KEY_n / _VALUE_n, GIT_CONFIG_PARAMETERS): none of them
// moves git to another repository. GIT_CEILING_DIRECTORIES only stops
// discovery from climbing, so it stays too.
package gitenv

import "strings"

// RepoLocating reports whether name is a variable that points git at a
// repository, work tree, index, object store or config file other than the
// one its directory names. The set is closed and written as a switch so it
// cannot be changed at run time:
//
//   - GIT_DIR, GIT_WORK_TREE, GIT_COMMON_DIR, GIT_NAMESPACE, GIT_PREFIX:
//     where the repository and work tree are (hooks set the first two);
//   - GIT_INDEX_FILE: which index `add`/`commit` write;
//   - GIT_OBJECT_DIRECTORY, GIT_ALTERNATE_OBJECT_DIRECTORIES,
//     GIT_QUARANTINE_PATH: where objects are read and written (pre-receive
//     sets the last);
//   - GIT_SHALLOW_FILE, GIT_GRAFT_FILE: per-repository files read from a
//     path outside the repository named;
//   - GIT_CONFIG: the legacy variable that makes `git config` read and write
//     that one file instead of the repository's config.
func RepoLocating(name string) bool {
	switch name {
	case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_NAMESPACE", "GIT_PREFIX",
		"GIT_INDEX_FILE",
		"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_QUARANTINE_PATH",
		"GIT_SHALLOW_FILE", "GIT_GRAFT_FILE",
		"GIT_CONFIG":
		return true
	}
	return false
}

// Names returns every name RepoLocating reports, in a fresh slice the
// caller may keep. It exists for code that must remove the variables from a
// whole process rather than from one child's environment: a test package
// whose tests start git directly unsets them once, so a suite run from
// inside a git hook never touches the hook's repository.
func Names() []string {
	return []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_NAMESPACE", "GIT_PREFIX",
		"GIT_INDEX_FILE",
		"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_QUARANTINE_PATH",
		"GIT_SHALLOW_FILE", "GIT_GRAFT_FILE",
		"GIT_CONFIG",
	}
}

// Clean returns env (KEY=VALUE entries, as os.Environ gives them) without
// the RepoLocating entries, in the same order. It never modifies env, and a
// nil or empty env gives an empty, non-nil slice, so a caller that assigns
// the result to exec.Cmd.Env never falls back to the inherited environment
// by accident (a nil Env means "inherit everything").
func Clean(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !RepoLocating(name) {
			out = append(out, kv)
		}
	}
	return out
}
