package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/model/errs"
)

// Environment every git call runs with (ported from
// saas/pkg/aicoder/gitref/git.go): a fixed locale so the messages this
// package reads are English, and no credential prompt, so a store that
// needs a login fails instead of hanging a command.
const (
	envLocale   = "LC_ALL=C"
	envNoPrompt = "GIT_TERMINAL_PROMPT=0"
)

// runner runs one git binary.
type runner struct {
	bin string
	// owned is true for the lore-owned store clone: commits there never
	// run hooks or signing configured for the person's own repos.
	owned bool
	// noHooks is a folder that does not exist, set as core.hooksPath in an
	// owned clone so no global hooks run inside it.
	noHooks string
	// routeArgs / routeEnv are the store clone's ssh route (WithRoute, Q5).
	routeArgs []string
	routeEnv  []string
	// accessNote is added to the error of a failed network call: the access
	// rule the remote is reached through (WithAccessNote); "" = none.
	accessNote string
	// remote is the store url origin points at (OpenClone), named in the
	// --trace line of a network call; "" when unknown.
	remote string
}

// tracedSubcommands are the git subcommands that reach the remote: the
// ones --trace prints (gitref.WithTracer, issue 15), so the host the call
// used (the setup's ssh alias) is visible. Local commands are not traced.
var tracedSubcommands = map[string]bool{"fetch": true, "push": true, "ls-remote": true, "clone": true, "pull": true}

// netRefusedExit is the exit status of a call the network switches refused
// (git's own status for a remote it could not reach).
const netRefusedExit = 128

// gitError is a git run that exited non-zero.
type gitError struct {
	cmd    string // the subcommand, never the arguments (they may hold a url)
	code   int
	stderr string
	// access names the access rule a network call went through ("" none).
	access string
}

// Error renders the subcommand, exit status and git's message, then the
// access rule the call went through.
func (e *gitError) Error() string {
	s := "git " + e.cmd + " exited " + strconv.Itoa(e.code) + ": " + strings.TrimSpace(e.stderr)
	if e.access != "" {
		s += " — " + e.access
	}
	return s
}

// exitCode returns the exit status of a failed git run; ok is false when
// git did not run at all (missing binary, cancelled context).
func exitCode(err error) (int, bool) {
	var ge *gitError
	if errors.As(err, &ge) {
		return ge.code, true
	}
	return 0, false
}

// stderrOf returns git's message of a failed run ("" otherwise).
func stderrOf(err error) string {
	var ge *gitError
	if errors.As(err, &ge) {
		return ge.stderr
	}
	return ""
}

// call is one git invocation.
type call struct {
	dir   string
	args  []string
	env   []string
	stdin []byte
}

// run runs git and returns stdout.
func (r runner) run(ctx context.Context, c call) ([]byte, error) {
	args := c.args
	if r.owned {
		// core.autocrlf=false: row files are bytes lore hashes and
		// compares, never text to convert. Git for Windows defaults to
		// autocrlf=true, which checked rows out with CRLF, so a second
		// clone read other bytes than the first wrote.
		args = append([]string{"-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + r.noHooks, "-c", "core.autocrlf=false", "-c", ownDriverOff}, args...)
	}
	if len(r.routeArgs) > 0 {
		args = append(append([]string(nil), r.routeArgs...), args...)
	}
	// LORE_OFFLINE / LORE_NO_FETCH / LORE_NO_STORE_FETCH (gitenv.StoreNetRefused): the call is not
	// run, and answers as git does when the remote is not there
	if why, refused := gitenv.StoreNetRefused(os.Environ(), c.args); refused {
		return nil, &gitError{cmd: gitenv.Subcommand(c.args), code: netRefusedExit, stderr: why}
	}
	// The Cmd is built directly rather than through exec.CommandContext:
	// r.bin is an absolute path resolved once by exec.LookPath (lookGit),
	// every argument is a separate argv entry built by this package (no
	// shell ever parses them), and cancellation is wired below.
	cmd := &exec.Cmd{Path: r.bin, Args: append([]string{r.bin}, args...), Dir: c.dir}
	cmd.Env = append(gitenv.Clean(os.Environ()), envLocale, envNoPrompt)
	cmd.Env = append(cmd.Env, r.routeEnv...)
	cmd.Env = append(cmd.Env, c.env...)
	if c.stdin != nil {
		cmd.Stdin = bytes.NewReader(c.stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := runCancellable(ctx, cmd)
	if trace := gitref.TracerFrom(ctx); trace != nil && tracedSubcommands[subcommand(c.args)] {
		trace(gitref.TraceCall{Args: args, Remote: r.remote, Env: append(append([]string(nil), r.routeEnv...), c.env...), Took: time.Since(start), Err: err})
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ctx.Err() == nil {
			ge := &gitError{cmd: subcommand(c.args), code: ee.ExitCode(), stderr: stderr.String()}
			if tracedSubcommands[ge.cmd] {
				ge.access = r.accessNote
			}
			return stdout.Bytes(), ge
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

// runCancellable runs cmd and kills it when ctx ends first.
func runCancellable(ctx context.Context, cmd *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			// The kill can only fail when the process already exited, which
			// Wait below reports; there is nothing else to do with it.
			if kerr := cmd.Process.Kill(); kerr != nil {
				return
			}
		case <-done:
		}
	}()
	err := cmd.Wait()
	close(done)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// lookGit resolves the git program once, so every run uses one absolute
// path and a missing git fails clearly at open, not in the middle of a
// write.
func lookGit(bin string) (string, error) {
	p, err := exec.LookPath(bin)
	if err != nil {
		return "", errs.Wrap(errs.Internal, "git: the git program was not found", err).WithHint("install git or pass its path")
	}
	return p, nil
}

// subcommand is the first non-option argument, for error messages.
func subcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-C" || args[i] == "-c":
			i++
		case !strings.HasPrefix(args[i], "-"):
			return args[i]
		}
	}
	return "?"
}

// trimmed is stdout without the trailing newline.
func trimmed(b []byte) string { return strings.TrimSpace(string(b)) }

// splitZ splits NUL-separated git output, dropping empty entries.
func splitZ(b []byte) []string {
	var out []string
	for p := range bytes.SplitSeq(b, []byte{0}) {
		if len(p) > 0 {
			out = append(out, string(p))
		}
	}
	return out
}
