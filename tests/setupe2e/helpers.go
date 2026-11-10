//go:build setupe2e

package setupe2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RepoRoot finds the module root (the folder with go.mod) from the
// working directory upwards.
func RepoRoot() (string, error) {
	d, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("repo root: %w", err)
	}
	for {
		if b, err := os.ReadFile(filepath.Join(d, "go.mod")); err == nil && strings.Contains(string(b), "module github.com/thesatellite-ai/lore\n") {
			return d, nil
		}
		p := filepath.Dir(d)
		if p == d {
			return "", fmt.Errorf("repo root: no lore go.mod above the working directory")
		}
		d = p
	}
}

// BuildLore builds ./cmd/lore into outDir/lore (CGO off, like a release).
func BuildLore(ctx context.Context, outDir string) (string, error) {
	root, err := RepoRoot()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("build lore: %w", err)
	}
	bin := filepath.Join(outDir, "lore")
	c := exec.CommandContext(ctx, "go", "build", "-trimpath", "-buildvcs=false", "-o", bin, "./cmd/lore")
	c.Dir = root
	c.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := c.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build lore: %w\n%s", err, out)
	}
	return bin, nil
}
