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
	if Offline(env) {
		// the belt under every runner's own check (NetRefused): git itself
		// refuses every transport, so no git lore starts reaches a remote
		out = append(out, noTransport)
	}
	return out
}

// Network switches of a lore process (owner bug 14: `lore fix --all
// --dry-run` fetched every store once per repo, minutes with no output).
// Both are read from the environment a process starts with, so a parent
// sets them for its children and every git lore starts sees the same value:
//
//	LORE_OFFLINE=1   no fetch, push, clone, ls-remote or pull at all: a
//	                 dry run of lore fix reads what is on disk only. The
//	                 runners refuse those calls (NetRefused) and Clean adds
//	                 GIT_ALLOW_PROTOCOL naming no transport, so a git started
//	                 anywhere else refuses too.
//	LORE_NO_FETCH=1  no fetch, ls-remote or pull from any remote; pushes
//	                 and clones still run.
//	LORE_NO_STORE_FETCH=1  no fetch, ls-remote or pull of a STORE clone
//	                 only (the store backend and the org link reader honour
//	                 it, StoreNetRefused): the parent of lore fix --all
//	                 fetched every store clone once just before, so a child
//	                 reads those refs instead of fetching the same store
//	                 again. The repo's own remote stays reachable (owner
//	                 item 21: the migrate team lock on the repo's signals
//	                 ref must fetch it), and so does a store's signals ref.
const (
	EnvOffline      = "LORE_OFFLINE"
	EnvNoFetch      = "LORE_NO_FETCH"
	EnvNoStoreFetch = "LORE_NO_STORE_FETCH"
	// On is the one value that turns a switch on (like every lore switch).
	On = "1"
)

// noTransport is GIT_ALLOW_PROTOCOL naming a protocol no git transport
// has: git refuses ssh, https, file and local paths alike, before
// connecting.
const noTransport = "GIT_ALLOW_PROTOCOL=lore-offline"

// Offline reports LORE_OFFLINE=1 in env.
func Offline(env []string) bool { return lookup(env, EnvOffline) == On }

// NoFetch reports LORE_NO_FETCH=1 (or LORE_OFFLINE=1, which refuses more).
func NoFetch(env []string) bool { return Offline(env) || lookup(env, EnvNoFetch) == On }

// NoStoreFetch reports LORE_NO_STORE_FETCH=1 (or a switch that refuses
// more: LORE_NO_FETCH, LORE_OFFLINE). Only the paths that fetch a store
// clone read it.
func NoStoreFetch(env []string) bool { return NoFetch(env) || lookup(env, EnvNoStoreFetch) == On }

// lookup is the last value of name in env ("" when unset).
func lookup(env []string, name string) string {
	v := ""
	for _, kv := range env {
		if k, val, ok := strings.Cut(kv, "="); ok && k == name {
			v = val
		}
	}
	return v
}

// fetchSubcommands read from a remote; pushSubcommands write to one or
// make a new clone of one.
var (
	fetchSubcommands = map[string]bool{"fetch": true, "ls-remote": true, "pull": true}
	pushSubcommands  = map[string]bool{"push": true, "clone": true}
)

// Subcommand is the git subcommand of args (git's arguments without the
// binary): the first argument after the global options, skipping the
// value of -C and -c. "" when there is none.
func Subcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-C" || a == "-c":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return a
		}
	}
	return ""
}

// NetRefused reports whether the git call args must not run under env's
// network switches, with the line saying why (a runner returns it as the
// call's error without starting git).
func NetRefused(env, args []string) (string, bool) {
	sub := Subcommand(args)
	switch {
	case Offline(env) && (fetchSubcommands[sub] || pushSubcommands[sub]):
		return "lore: git " + sub + " not run: " + EnvOffline + "=1 (this run reads what is on disk, it reaches no remote)", true
	case NoFetch(env) && fetchSubcommands[sub]:
		return "lore: git " + sub + " not run: " + EnvNoFetch + "=1 (no fetch from any remote in this run)", true
	}
	return "", false
}

// StoreNetRefused is NetRefused for a git call on a store clone: it also
// refuses a fetch under LORE_NO_STORE_FETCH (the store clones were
// fetched once for the whole run).
func StoreNetRefused(env, args []string) (string, bool) {
	if why, refused := NetRefused(env, args); refused {
		return why, true
	}
	if sub := Subcommand(args); NoStoreFetch(env) && fetchSubcommands[sub] {
		return "lore: git " + sub + " not run: " + EnvNoStoreFetch + "=1 (the store clones were fetched once for this whole run)", true
	}
	return "", false
}
