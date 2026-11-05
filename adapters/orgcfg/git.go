// git.go — the one place this package runs git (remotes of a repo; the
// store clones the link source reads).

package orgcfg

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// gitRun runs git with args and returns its stdout. The Cmd is built
// directly rather than through exec.CommandContext: bin is the absolute
// path exec.LookPath resolved, every argument is a separate argv entry
// built by this package (no shell parses them), and the process is killed
// when ctx ends.
func gitRun(ctx context.Context, args ...string) (string, error) {
	bin, err := exec.LookPath(gitBin)
	if err != nil {
		return "", fmt.Errorf("orgcfg: git is not installed: %w", err)
	}
	cmd := &exec.Cmd{Path: bin, Args: append([]string{bin}, args...)}
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
