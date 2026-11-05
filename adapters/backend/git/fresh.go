package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// Fetch stamp (RES 3.6 resources.stale_after, P2-RES-7):
//
//	fetch ok ──► stampFetch: <git common dir>/lore-fetched-at = RFC 3339 UTC
//	Fresh    ──► read the stamp + the worktree's HEAD commit
//
// The stamp lives inside the clone's own git directory, beside the data it
// describes, so it is per clone and per machine and never committed.

// fetchStampFile is the stamp's name in the git common directory.
const fetchStampFile = "lore-fetched-at"

var _ store.Freshness = (*Backend)(nil)

// stampFetch records a successful fetch run in dir (best effort: a stamp
// that cannot be written only makes Fresh report "unknown").
func (b *Backend) stampFetch(ctx context.Context, dir string) {
	p, err := b.stampPath(ctx, dir)
	if err != nil {
		return
	}
	_ = os.WriteFile(p, []byte(b.cfg.now().UTC().Format(time.RFC3339)+"\n"), 0o600) // best effort, see doc
}

// stampPath is the stamp file of the clone dir belongs to.
func (b *Backend) stampPath(ctx context.Context, dir string) (string, error) {
	out, err := b.git.run(ctx, call{dir: dir, args: []string{"rev-parse", "--git-common-dir"}})
	if err != nil {
		return "", err
	}
	common := trimmed(out)
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	return filepath.Join(common, fetchStampFile), nil
}

// Fresh implements store.Freshness. A repo-mode backend (the repo store,
// no remote of lore's) and a clone never fetched report Known false.
func (b *Backend) Fresh(ctx context.Context) (store.Fresh, error) {
	if b.mode != modeClone {
		return store.Fresh{}, nil
	}
	p, err := b.stampPath(ctx, b.work)
	if err != nil {
		return store.Fresh{}, errs.Wrap(errs.Internal, "git: find the fetch stamp", err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return store.Fresh{}, nil // never fetched here
	}
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(string(raw)))
	if err != nil {
		return store.Fresh{}, nil
	}
	commit, err := b.head(ctx)
	if err != nil {
		return store.Fresh{}, err
	}
	return store.Fresh{Known: true, FetchedAt: at, Commit: commit}, nil
}
