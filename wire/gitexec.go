package wire

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/logic/gitenv"
	"github.com/thesatellite-ai/lore/model/errs"
)

// gitRun is one git invocation of this package (live-ref reads, marker and
// migration commits). Why wire runs git itself instead of through the git
// backend: these are repo-level reads of refs/lore/live and one-off commits
// of files the backend does not own (store markers, lore-org.yaml).
type gitRun struct {
	dir  string   // working directory ("" = the process's)
	env  []string // extra KEY=VALUE entries on top of the process's environment (see gitEnv)
	args []string // git arguments, one argv entry each
}

// output runs g and returns its stdout. A failure is E_INTERNAL carrying
// git's first argument and trimmed stderr (never the whole argv, which may
// hold a commit subject or paths the caller already names).
//
// The Cmd is built directly rather than through exec.CommandContext: the
// binary is the absolute path exec.LookPath resolved, every argument is a
// separate argv entry built by this package (no shell parses them), and
// runCancellable kills the process when ctx ends.
func (g gitRun) output(ctx context.Context) ([]byte, error) {
	bin, err := exec.LookPath(git.DefaultBinary)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "wire: git is not installed", err)
	}
	if why, refused := gitenv.RepoNetRefused(os.Environ(), g.args); refused {
		return nil, errs.New(errs.Offline, why) // LORE_OFFLINE: not run (LORE_NO_FETCH covers store clones only)
	}
	cmd := &exec.Cmd{Path: bin, Args: append([]string{bin}, g.args...), Dir: g.dir, Env: gitEnv(g.env)}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if rerr := runCancellable(ctx, cmd); rerr != nil {
		sub := ""
		if len(g.args) > 0 {
			sub = g.args[0]
		}
		return nil, errs.Wrap(errs.Internal, "wire: git "+sub+": "+strings.TrimSpace(stderr.String()), rerr)
	}
	return stdout.Bytes(), nil
}

// runCancellable starts cmd and kills it when ctx ends first; it returns
// ctx's error in that case, else cmd's.
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
			// A failed kill means the process already exited; Wait reports it.
			_ = cmd.Process.Kill()
		case <-done:
		}
	}()
	err := cmd.Wait()
	close(done)
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	return err
}

// gitEnv is the child environment of a gitRun: this process's environment
// without the repository-locating variables a git hook exports, so g.dir
// alone names the repository (logic/gitenv, issue #6), then extra. extra is
// appended after the clean, so a caller that means one of those variables
// (a temporary GIT_INDEX_FILE) still gets it.
func gitEnv(extra []string) []string {
	return append(gitenv.Clean(os.Environ()), extra...)
}
