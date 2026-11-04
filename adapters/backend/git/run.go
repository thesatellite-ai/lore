package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"

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

// repoLocatingEnv are inherited variables that would point git at another
// repository than the one named with -C (a hook or a parent git sets them).
var repoLocatingEnv = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR",
	"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_PREFIX", "GIT_NAMESPACE", "GIT_QUARANTINE_PATH",
}

// runner runs one git binary.
type runner struct {
	bin string
	// owned is true for the lore-owned store clone: commits there never
	// run hooks or signing configured for the person's own repos.
	owned bool
	// noHooks is a folder that does not exist, set as core.hooksPath in an
	// owned clone so no global hooks run inside it.
	noHooks string
}

// gitError is a git run that exited non-zero.
type gitError struct {
	cmd    string // the subcommand, never the arguments (they may hold a url)
	code   int
	stderr string
}

// Error renders the subcommand, exit status and git's message.
func (e *gitError) Error() string {
	return "git " + e.cmd + " exited " + strconv.Itoa(e.code) + ": " + strings.TrimSpace(e.stderr)
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
		args = append([]string{"-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + r.noHooks}, args...)
	}
	// The Cmd is built directly rather than through exec.CommandContext:
	// r.bin is an absolute path resolved once by exec.LookPath (lookGit),
	// every argument is a separate argv entry built by this package (no
	// shell ever parses them), and cancellation is wired below.
	cmd := &exec.Cmd{Path: r.bin, Args: append([]string{r.bin}, args...), Dir: c.dir}
	cmd.Env = append(cleanEnv(os.Environ()), envLocale, envNoPrompt)
	cmd.Env = append(cmd.Env, c.env...)
	if c.stdin != nil {
		cmd.Stdin = bytes.NewReader(c.stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := runCancellable(ctx, cmd)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ctx.Err() == nil {
			return stdout.Bytes(), &gitError{cmd: subcommand(c.args), code: ee.ExitCode(), stderr: stderr.String()}
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

// cleanEnv drops repoLocatingEnv entries from env.
func cleanEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		keep := true
		for _, bad := range repoLocatingEnv {
			if name == bad {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, kv)
		}
	}
	return out
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
