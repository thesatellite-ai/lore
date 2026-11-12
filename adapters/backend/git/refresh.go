package git

// refresh.go — one fetch of a store clone for a run over many repos (owner
// bug 14): `lore fix --all` fetches every store clone once here, then runs
// each repo's lore fix with LORE_NO_FETCH=1 (logic/gitenv), so a store that
// forty repos share is fetched once instead of forty times or more.

import (
	"context"
	"errors"

	"github.com/thesatellite-ai/lore/model/errs"
)

// RefreshClone fetches the store clone at dir from its origin as a first
// open does (every branch and every live ref, pruned) and writes the fetch
// stamp. A remote that does not answer is errs.Offline; nothing local
// changes then.
func RefreshClone(ctx context.Context, dir string, opts ...Option) error {
	cfg := build(opts)
	p, err := lookGit(cfg.bin)
	if err != nil {
		return err
	}
	b := &Backend{git: runner{bin: p, remote: OriginAt(dir)}, cfg: cfg}
	if cfg.route != nil {
		b.git.routeArgs, b.git.routeEnv = cfg.route(ctx, b.git.remote)
	}
	remote := "remote." + RemoteName + "."
	_, err = b.git.run(ctx, call{dir: dir, args: []string{"-c", remote + "fetch=" + headsRefspec, "-c", remote + "fetch=" + liveRefspec,
		"fetch", "--quiet", "--no-tags", "--prune", "--no-write-fetch-head", RemoteName}})
	if err == nil {
		b.stampFetch(ctx, dir)
		return nil
	}
	if _, ran := exitCode(err); !ran {
		return err
	}
	return errs.Wrap(errs.Offline, "git: fetch the store clone "+dir, errors.Join(errOffline, err))
}
