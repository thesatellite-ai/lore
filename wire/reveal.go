package wire

// reveal.go — App's opsreveal.Port: where lore's own files are on this
// machine, and the one opener call (machine.reveal). The targets are the
// closed set of opsreveal.Target; no caller ever names a path.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/thesatellite-ai/lore/model/errs"
	opsreveal "github.com/thesatellite-ai/lore/ops/reveal"
)

var _ opsreveal.Port = (*App)(nil)

// repoLoreDir is the repo's lore folder name.
const repoLoreDir = ".lore"

// MachinePath implements opsreveal.Port.
func (a *App) MachinePath(ctx context.Context, t opsreveal.Target) (opsreveal.Place, error) {
	var p string
	switch t {
	case opsreveal.TargetLoreHome:
		p = a.opts.Home
	case opsreveal.TargetConfigFile:
		p = configPath(a.opts.Home)
	case opsreveal.TargetRepoLore:
		dir := a.projectDir()
		if dir == "" {
			wd, err := os.Getwd()
			if err != nil {
				return opsreveal.Place{}, errs.Wrap(errs.Internal, "machine reveal: working folder", err)
			}
			dir = wd
		}
		top := gitTopLevel(ctx, dir)
		if top == "" {
			return opsreveal.Place{}, nil
		}
		p = filepath.Join(top, repoLoreDir)
	default:
		return opsreveal.Place{}, errs.New(errs.InvalidInput, "machine reveal: unknown target "+string(t))
	}
	pl := opsreveal.Place{Path: p, IsDir: t != opsreveal.TargetConfigFile}
	st, err := os.Stat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return pl, errs.FileIO("stat", p, err)
	default:
		pl.Exists, pl.IsDir = true, st.IsDir()
	}
	return pl, nil
}

// Open implements opsreveal.Port: argv is a fixed opener and one resolved
// path (opsreveal.Argv); no shell is involved. The opener is reaped in the
// background; its exit status says nothing useful.
func (a *App) Open(ctx context.Context, argv []string) error {
	cmd := exec.CommandContext(context.WithoutCancel(ctx), argv[0], argv[1:]...)
	if err := cmd.Start(); err != nil {
		return errs.Wrap(errs.Unsupported, "machine reveal: start "+argv[0], err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
