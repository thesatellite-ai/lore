package storegrowth

// import.go — feed a generated stream into a repository that holds the
// seed: `git fast-import` reads it from a pipe (no stream file on disk, so
// 1M commits need no gigabytes of temp space), then the scratch side ref
// is removed.

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Import generates p's history into the repository at dir (bare or not;
// its branch p.Branch must hold the seed) and returns what was written.
func Import(ctx context.Context, dir string, p Plan) (Stats, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "fast-import", "--quiet", "--done")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return Stats{}, fmt.Errorf("storegrowth: import: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return Stats{}, fmt.Errorf("storegrowth: import: %w", err)
	}
	st, genErr := Generate(in, p)
	if genErr == nil {
		_, genErr = in.Write([]byte("done\n"))
	}
	_ = in.Close()
	waitErr := cmd.Wait()
	if genErr != nil {
		return Stats{}, fmt.Errorf("storegrowth: import: %w", genErr)
	}
	if waitErr != nil {
		return Stats{}, fmt.Errorf("storegrowth: git fast-import: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	if out, err := exec.CommandContext(ctx, "git", "-C", dir, "update-ref", "-d", SideRef).CombinedOutput(); err != nil && st.SideCommits > 0 && st.Merges > 0 {
		return Stats{}, fmt.Errorf("storegrowth: drop %s: %w: %s", SideRef, err, out)
	}
	return st, nil
}
