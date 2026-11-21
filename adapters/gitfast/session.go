// Package gitfast answers the read-only git commands lore runs on every
// command without starting a git process, for the life of one lore
// process (devco:D-418).
//
// Why it exists: one everyday command (task list, search, render) started
// 110-300 git processes, mostly the same few questions asked by different
// layers — where is the work tree, what does refs/lore/live hold, what is
// at <commit>:<path>, what is the remote's url. A git process costs ~2 ms
// on Linux and ~17-23 ms on macOS (Apple's /usr/bin/git is an xcrun
// launcher), so the Mac paid 0.7-1 s per command in process starts alone.
//
// How: a Session travels in the context (With / From; the CLI root makes
// one per process). Every git runner asks Session.Answer before it starts
// git. Answer reads the repository files itself — refs and packed-refs,
// loose and packed objects, trees — for the invocation shapes lore uses,
// keeps the answers of content-addressed reads (an object id never names
// other content) and of config reads (one `git config --list` per
// repository, dropped on any write), and says "not handled" for anything
// else, so the runner runs git exactly as before.
//
// Behaviour stays identical: an answer is given only when it is exactly
// what git prints for that invocation; every unusual layout (reftable,
// sha256, replace refs, grafts, shallow, core.worktree, includes in the
// repository config, a repository owned by another user, Windows) and
// every failure whose message git would print falls back to git. Ref
// values are read from the files on every call (never cached), so a write
// by another process, a hook or a runner that bypasses the Session is seen
// at once.
package gitfast

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/thesatellite-ai/lore/logic/gitenv"
)

// Session holds the per-process repository readers and memos. Safe for
// concurrent use; the zero value is not usable, use New.
type Session struct {
	// git runs the git program for the one exec the Session makes itself
	// (the config snapshot); nil = exec from PATH.
	git func(ctx context.Context, dir string, args []string) ([]byte, error)

	mu    sync.Mutex
	repos map[string]*repo // discovery dir -> repo (nil: not readable here)
	// noRepo: discovery dir -> git finds no repository there (noRepoAt)
	noRepo map[string]bool
	// immutable holds answers that depend only on object ids.
	immutable map[string]Reply
	// configs holds `config --list -z --show-scope` per dir + -c options.
	configs map[string]*configSnap
	// gen moves on every write, so a config snapshot read while a write
	// ran is not kept.
	gen     uint64
	writing int

	disabled bool

	// cacheDir is where answers are kept across processes (SetCacheDir);
	// "" = only for this process.
	cacheDir string
}

// CacheDirName is the Session's folder under the lore home's cache folder.
const CacheDirName = "gitfast"

// SetCacheDir lets s keep answers across processes in dir/gitfast. dir is
// the lore home's cache folder; it must exist already (a read never
// creates the lore home), and only the gitfast folder inside is made.
func (s *Session) SetCacheDir(dir string) {
	if s == nil || dir == "" {
		return
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return
	}
	s.cacheDir = filepath.Join(dir, CacheDirName)
}

// Reply is an answer: git's standard output and exit status (0 success;
// non-zero with no standard error, the quiet "no" of --verify --quiet,
// symbolic-ref --quiet, check-ref-format, merge-base --is-ancestor).
type Reply struct {
	Out  []byte
	Code int
	// Stderr is what git prints on standard error for this answer: empty
	// for the quiet answers (an unset key, a missing ref), git's own
	// message for "not a git repository" (noRepoReply), so an error built
	// around it reads as before.
	Stderr string
}

// ctxKey keys the Session in a context.
type ctxKey struct{}

// EnvOff turns the Session off (every call runs git): LORE_GIT_FAST=0.
const EnvOff = "LORE_GIT_FAST"

// New makes an empty Session.
func New() *Session {
	return &Session{repos: map[string]*repo{}, noRepo: map[string]bool{}, immutable: map[string]Reply{}, configs: map[string]*configSnap{}, disabled: os.Getenv(EnvOff) == "0"}
}

// With returns ctx carrying s.
func With(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// From returns the Session in ctx, else the one Install put in place for
// the running command, else nil.
func From(ctx context.Context) *Session {
	if s, ok := ctx.Value(ctxKey{}).(*Session); ok && s != nil {
		return s
	}
	return running.Load()
}

// running is the Session of the command this process is running (Install).
// Code that starts from context.Background() (about thirty places: cache
// ports with no context, lock waits, scanners) reaches git without the
// command's ctx; it still shares the command's Session through this.
var running atomic.Pointer[Session]

// Install makes s the Session of the running command for callers whose
// ctx carries none, and returns the function that puts the previous one
// back. One lore command per process at a time (app.Run's contract); a
// test that runs commands one after another gets a fresh Session each.
func Install(s *Session) (restore func()) {
	prev := running.Swap(s)
	return func() { running.Store(prev) }
}

// Close releases open pack files.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.repos {
		if r != nil && r.objs != nil && !r.objsShared {
			r.objs.close()
		}
	}
	clear(s.repos)
}

// repoFor discovers (once) the repository dir is in.
// noRepoAt reports, once per folder, whether git finds no repository at
// or above dir (noRepoAbove).
func (s *Session) noRepoAt(dir string) bool {
	s.mu.Lock()
	none, seen := s.noRepo[dir]
	s.mu.Unlock()
	if seen {
		return none
	}
	none = noRepoAbove(dir)
	s.mu.Lock()
	s.noRepo[dir] = none
	s.mu.Unlock()
	return none
}

func (s *Session) repoFor(dir string) *repo {
	s.mu.Lock()
	r, seen := s.repos[dir]
	s.mu.Unlock()
	if seen {
		return r
	}
	r, ok := discover(dir)
	if !ok {
		r = nil
	} else if !r.objectsPlain() {
		r = nil
	}
	s.mu.Lock()
	s.repos[dir] = r
	s.mu.Unlock()
	return r
}

// objectsPlain reports whether the object store has none of the things
// that make git read objects differently (replace refs, grafts, shallow).
func (r *repo) objectsPlain() bool {
	if fileExists(filepath.Join(r.common, "info", "grafts")) || fileExists(filepath.Join(r.common, "shallow")) {
		return false
	}
	if os.Getenv("GIT_NO_REPLACE_OBJECTS") == "" {
		if entries, err := os.ReadDir(filepath.Join(r.common, "refs", "replace")); err == nil && len(entries) > 0 {
			return false
		}
		packed, ok := r.packedRefs()
		if !ok {
			return false
		}
		for n := range packed {
			if strings.HasPrefix(n, "refs/replace/") {
				return false
			}
		}
	}
	return true
}

// Answer answers one git invocation without a process when it can.
// handled=false: run git as usual, then call Record. A write (anything not
// known to be a read) drops the config snapshots before it runs.
func (s *Session) Answer(ctx context.Context, dir string, args, env []string, stdin []byte) (Reply, bool) {
	if s == nil || s.disabled {
		return Reply{}, false
	}
	inv, ok := parseInvocation(dir, args)
	if !ok || !isRead(inv) || !envHarmless(env) {
		s.noteWriteStart()
		return Reply{}, false
	}
	index := ""
	if len(env) != 0 {
		var leaves bool
		if index, leaves = envLeavesAnswer(inv, env, s.repoFor(inv.dir)); !leaves {
			return Reply{}, false // a read whose answer the env changes: git
		}
	}
	if index != "" {
		// a hook's temporary index (`git commit -a`): the reads answered
		// here read that file instead of .git/index; nothing is looked up
		// in or kept for the shared memos (devco:D-420)
		r := s.repoFor(inv.dir)
		if r == nil || inv.sub == "config" || (inv.sub == "rev-parse" && asksHooks(inv)) || (inv.sub == "log" && !s.logPlain(ctx, inv)) {
			return Reply{}, false
		}
		return answerRead(r.withIndex(index), inv, stdin)
	}
	if key, imm := immutableKey(inv, stdin); imm {
		s.mu.Lock()
		rep, hit := s.immutable[key]
		s.mu.Unlock()
		if hit {
			return clone(rep), true
		}
	}
	if inv.sub == "config" {
		return s.answerConfig(ctx, inv)
	}
	r := s.repoFor(inv.dir)
	if r == nil {
		if inv.sub == "rev-parse" && needsRepo(inv.args) && s.noRepoAt(inv.dir) {
			return noRepoReply(), true // devco:D-826: a read outside any repo starts no git
		}
		return Reply{}, false
	}
	if persistable(inv, stdin) {
		if path := s.persistPath(r, inv); path != "" {
			if rep, hit := persistGet(path); hit {
				return rep, true
			}
		}
	}
	if inv.sub == "log" && !s.logPlain(ctx, inv) {
		return Reply{}, false
	}
	if inv.sub == "rev-parse" && asksHooks(inv) {
		inv.hooksDir = s.hooksDir(ctx, r, inv)
	}
	rep, ok := answerRead(r, inv, stdin)
	if !ok {
		return Reply{}, false
	}
	return rep, true
}

// Record notes a git run the caller made after Answer said not handled:
// content-addressed answers are kept; a write moves the generation.
// exited=false (git did not run to an exit status) records nothing.
func (s *Session) Record(dir string, args, env []string, stdin []byte, out []byte, code int, exited bool) {
	if s == nil || s.disabled {
		return
	}
	inv, ok := parseInvocation(dir, args)
	if !ok || !isRead(inv) || !envHarmless(env) {
		s.noteWriteEnd()
		return
	}
	if !exited || len(env) != 0 {
		return // an answer given under extra env is never kept
	}
	if key, imm := immutableKey(inv, stdin); imm {
		s.mu.Lock()
		s.immutable[key] = clone(Reply{Out: out, Code: code})
		s.mu.Unlock()
	}
	if persistable(inv, stdin) && (code == 0 || (code == 1 && inv.sub == "grep")) {
		if r := s.repoFor(inv.dir); r != nil {
			if path := s.persistPath(r, inv); path != "" {
				persistPut(path, out, code)
			}
		}
	}
}

// noteWriteStart drops config snapshots before a write runs.
func (s *Session) noteWriteStart() {
	s.mu.Lock()
	s.writing++
	s.gen++
	clear(s.configs)
	s.mu.Unlock()
}

// noteWriteEnd drops them again after it ran.
func (s *Session) noteWriteEnd() {
	s.mu.Lock()
	if s.writing > 0 {
		s.writing--
	}
	s.gen++
	clear(s.configs)
	s.mu.Unlock()
}

// clone copies a reply so no caller can change a kept answer.
func clone(r Reply) Reply {
	return Reply{Out: append([]byte(nil), r.Out...), Code: r.Code, Stderr: r.Stderr}
}

// execGit runs git for the Session's own reads (the config snapshot).
func (s *Session) execGit(ctx context.Context, dir string, args []string) ([]byte, error) {
	if s.git != nil {
		return s.git(ctx, dir, args)
	}
	bin, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}
	cmd := &exec.Cmd{Path: bin, Args: append([]string{bin}, args...), Dir: dir}
	cmd.Env = append(gitenv.Clean(os.Environ()), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
	return cmd.Output()
}

// Try is Answer on the Session in ctx (handled=false without one).
func Try(ctx context.Context, dir string, args, env []string, stdin []byte) (Reply, bool) {
	return From(ctx).Answer(ctx, dir, args, env, stdin)
}

// Note is Record on the Session in ctx (nothing without one).
func Note(ctx context.Context, dir string, args, env []string, stdin, out []byte, code int, exited bool) {
	From(ctx).Record(dir, args, env, stdin, out, code, exited)
}

// Output is the seam for git runners whose callers read *exec.ExitError
// themselves: a successful answer from the Session in ctx is returned
// without a process; anything else runs run (the caller's own exec) and
// records its result, so a write still drops the config snapshots.
func Output(ctx context.Context, dir string, args, env []string, stdin []byte, run func() ([]byte, error)) ([]byte, error) {
	if rep, ok := Try(ctx, dir, args, env, stdin); ok && rep.Code == 0 {
		return rep.Out, nil
	}
	out, err := run()
	code, exited := 0, err == nil
	var ee *exec.ExitError
	if errors.As(err, &ee) && ctx.Err() == nil {
		code, exited = ee.ExitCode(), true
	}
	Note(ctx, dir, args, env, stdin, out, code, exited)
	return out, err
}

// Bracket runs run (a git command whose output must come from git itself,
// e.g. with standard error merged in) and tells the Session in ctx about
// it, so a write drops the config snapshots like any other.
func Bracket(ctx context.Context, dir string, args, env []string, stdin []byte, run func() ([]byte, error)) ([]byte, error) {
	s := From(ctx)
	if s == nil || s.disabled {
		return run()
	}
	inv, ok := parseInvocation(dir, args)
	write := !ok || !isRead(inv) || !envHarmless(env)
	if write {
		s.noteWriteStart()
		defer s.noteWriteEnd()
	}
	return run()
}

// ExitError is the error of an exit status the Session answered without
// a process; its text is what os/exec prints for that status ("exit
// status 1"), so a message built around it reads as before.
type ExitError struct{ Code int }

// Error implements error.
func (e *ExitError) Error() string { return "exit status " + strconv.Itoa(e.Code) }

// ExitCode is the status, as *exec.ExitError has it.
func (e *ExitError) ExitCode() int { return e.Code }

// FirstArg is args[0] or "" (the subcommand word wire's errors name).
func FirstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// hooksDir is where git runs r's hooks from, as `rev-parse
// --path-format=absolute --git-path hooks` prints it: core.hooksPath
// (absolute, or ~/…) cleaned, else <common dir>/hooks; "" when git must
// say (a relative or empty core.hooksPath, one given with -c).
func (s *Session) hooksDir(ctx context.Context, r *repo, inv invocation) string {
	if slicesContains(inv.dropped, "core.hookspath") || len(inv.cflags) != 0 {
		return ""
	}
	snap, ok := s.configSnapshot(ctx, invocation{dir: inv.dir, sub: "config"})
	if !ok {
		return ""
	}
	val, set := "", false
	for _, e := range snap.entries {
		if e.key == "core.hookspath" {
			if !e.hasValue {
				return ""
			}
			val, set = e.value, true
		}
	}
	if !set {
		return filepath.Join(r.common, "hooks")
	}
	if strings.HasPrefix(val, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		val = filepath.Join(home, val[2:])
	}
	if !filepath.IsAbs(val) {
		return ""
	}
	return filepath.Clean(val)
}

// envHarmless reports whether env holds only variables that cannot point
// git at another repository or change its config: identities and dates of
// new objects, the locale, the prompt switch, and an index file (which
// only index readers see; envLeavesAnswer checks those). Anything else
// counts as a write, as before.
func envHarmless(env []string) bool {
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(name, "GIT_AUTHOR_"), strings.HasPrefix(name, "GIT_COMMITTER_"):
		case name == "LC_ALL", name == "LANG", name == "GIT_TERMINAL_PROMPT", name == "GIT_INDEX_FILE":
		default:
			return false
		}
	}
	return true
}

// envLeavesAnswer reports whether a read handled here can be answered
// with env set, and the index file it must read ("" = the usual one).
// Only an index file matters: a hook of `git commit -a` runs with a
// temporary index (GIT_INDEX_FILE). An absolute path is read in place of
// .git/index by every index reader here (readIndex), so the answer is the
// one git prints (devco:D-420); a relative one leaves only reads that
// never look at an index (refs, objects, trees, config).
func envLeavesAnswer(inv invocation, env []string, r *repo) (string, bool) {
	index := ""
	for _, kv := range env {
		name, val, _ := strings.Cut(kv, "=")
		if name != "GIT_INDEX_FILE" || defaultIndex(r, inv.dir, val) {
			continue // a hook's GIT_INDEX_FILE naming the usual index changes nothing
		}
		if filepath.IsAbs(val) {
			index = filepath.Clean(val)
			continue
		}
		if !indexFree[inv.sub] {
			return "", false
		}
		for _, a := range inv.args {
			if a == "--git-path" || strings.HasPrefix(a, ":") || a == "--cached" || a == "--staged" {
				return "", false
			}
		}
	}
	return index, true
}

// indexFree are the subcommands whose answers handled here never read the
// index.
var indexFree = map[string]bool{
	"config": true, "rev-parse": true, "for-each-ref": true, "symbolic-ref": true, "check-ref-format": true,
	"ls-tree": true, "diff-tree": true, "merge-base": true, "rev-list": true, "log": true,
}

// defaultIndex reports whether an index file path (relative to dir) is
// r's own .git/index.
func defaultIndex(r *repo, dir, p string) bool {
	if r == nil || p == "" {
		return false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	d, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		return false
	}
	return filepath.Join(d, filepath.Base(p)) == filepath.Join(r.gitDir, "index")
}

// logPlain reports whether no config changes what `log --format` prints
// (log.showSignature adds signature lines, i18n.logOutputEncoding
// re-encodes the message).
func (s *Session) logPlain(ctx context.Context, inv invocation) bool {
	snap, ok := s.configSnapshot(ctx, invocation{dir: inv.dir, sub: "config"})
	if !ok {
		return false
	}
	for _, e := range snap.entries {
		if e.key == "log.showsignature" || e.key == "i18n.logoutputencoding" {
			return false
		}
	}
	return true
}

// OutputQuiet is Output for runners whose callers only test err != nil: a
// quiet non-zero answer (an unset config key, a ref that is not there) is
// returned as an *ExitError without a process too.
func OutputQuiet(ctx context.Context, dir string, args, env []string, stdin []byte, run func() ([]byte, error)) ([]byte, error) {
	if rep, ok := Try(ctx, dir, args, env, stdin); ok {
		if rep.Code != 0 {
			return rep.Out, &ExitError{Code: rep.Code}
		}
		return rep.Out, nil
	}
	return Output(ctx, dir, args, env, stdin, run)
}
