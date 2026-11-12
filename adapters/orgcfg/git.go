// git.go — the one place this package runs git (remotes of a repo; the
// store clones the link source reads).

package orgcfg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/gitenv"
)

// gitRun runs git with args and returns its stdout. The Cmd is built
// directly rather than through exec.CommandContext: bin is the absolute
// path exec.LookPath resolved, every argument is a separate argv entry
// built by this package (no shell parses them), and the process is killed
// when ctx ends.
func gitRun(ctx context.Context, args ...string) (string, error) {
	return gitRunEnv(ctx, nil, args...)
}

// gitRunEnv is gitRun with extra KEY=VALUE environment entries (nil: the
// inherited environment, minus gitenv.RepoLocating, only).
func gitRunEnv(ctx context.Context, env []string, args ...string) (string, error) {
	bin, err := exec.LookPath(gitBin)
	if err != nil {
		return "", fmt.Errorf("orgcfg: git is not installed: %w", err)
	}
	if why, refused := gitenv.NetRefused(os.Environ(), args); refused {
		return "", errors.New(why) // LORE_OFFLINE / LORE_NO_FETCH: not run
	}
	cmd := &exec.Cmd{Path: bin, Args: append([]string{bin}, args...)}
	// Never the bare inherited environment: inside a git hook it carries
	// GIT_DIR and friends, and `git -C <dir> ...` would act on the hook's
	// repository instead of dir (issue #6). gitenv documents what is kept.
	cmd.Env = append(gitenv.Clean(os.Environ()), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("orgcfg: start git: %w", err)
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
	werr := cmd.Wait()
	close(done)
	if werr != nil {
		return stdout.String(), fmt.Errorf("orgcfg: git %s: %w: %s", strings.Join(args, " "), werr, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// Router gives the per-call git options and environment that reach one
// store url as its setup's account (ONBOARDING_DESIGN 3.4, Q5: the
// `-c url.git@<alias>:.insteadOf=git@github.com:` pair, or
// GIT_SSH_COMMAND for a key without an alias). wire's storeRouter is the
// one implementation: it answers only for a store a setup owns, and
// nothing for any other url. nil = every call runs as git is configured.
type Router func(ctx context.Context, url string) (args, env []string)

// netGit runs one git network call (fetch, ls-remote, push) in the store
// clone dir, through r for the url dir's origin points at. Every network
// call of this package goes through here, so none can miss the route;
// local calls (rev-parse, ls-tree, cat-file …) use gitRun.
//
// With a tracer in ctx (--trace, gitref.WithTracer) each call is printed
// with the host it reached, so the setup's alias is visible (issue 15).
func netGit(ctx context.Context, r Router, dir string, args ...string) (string, error) {
	var pre, env []string
	trace := gitref.TracerFrom(ctx)
	remote := ""
	if r != nil || trace != nil {
		if u, err := gitRun(ctx, "-C", dir, "config", "--get", "remote."+remoteName+".url"); err == nil {
			remote = strings.TrimSpace(u)
			if r != nil {
				pre, env = r(ctx, remote)
			}
		}
	}
	full := append(append(append([]string(nil), pre...), "-C", dir), args...)
	start := time.Now()
	out, err := gitRunEnv(ctx, env, full...)
	if trace != nil {
		trace(gitref.TraceCall{Args: full, Remote: remote, Env: env, Took: time.Since(start), Err: err})
	}
	return out, err
}
