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
//	fetch ok ──► stampFetch: <clone>/.git/lore/fetch-stamp = RFC 3339 UTC
//	Fresh    ──► read the stamp + the clone's HEAD commit
//
// The stamp lives in lore's folder inside the clone's own git directory
// (PathsOf StateDir), beside the data it describes, so it is per clone and
// per machine and never committed. The org link code and the push wrapper
// read and write the same file (one "last fetched" per clone).

// FetchStampFile is the stamp's name in the clone's .git/lore folder.
const FetchStampFile = "fetch-stamp"

var _ store.Freshness = (*Backend)(nil)

// stampFetch records a successful fetch run in dir (best effort: a stamp
// that cannot be written only makes Fresh report "unknown").
func (b *Backend) stampFetch(ctx context.Context, dir string) {
	root, err := b.stampRoot(ctx, dir)
	if err != nil {
		return
	}
	defer func() { _ = root.Close() }()
	_ = root.MkdirAll(StateDirName, cloneDirMode)                                                                               // best effort, see doc
	_ = root.WriteFile(filepath.Join(StateDirName, FetchStampFile), []byte(b.cfg.now().UTC().Format(time.RFC3339)+"\n"), 0o600) // best effort, see doc
}

// stampRoot opens the git common directory of the clone dir belongs to;
// the stamp is read and written only inside it (os.Root).
func (b *Backend) stampRoot(ctx context.Context, dir string) (*os.Root, error) {
	out, err := b.git.run(ctx, call{dir: dir, args: []string{"rev-parse", "--git-common-dir"}})
	if err != nil {
		return nil, err
	}
	common := trimmed(out)
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	return os.OpenRoot(common)
}

// Fresh implements store.Freshness. A repo-mode backend (the repo store,
// no remote of lore's) and a clone never fetched report Known false.
func (b *Backend) Fresh(ctx context.Context) (store.Fresh, error) {
	if b.mode != modeClone {
		return store.Fresh{}, nil
	}
	root, err := b.stampRoot(ctx, b.work)
	if err != nil {
		return store.Fresh{}, errs.Wrap(errs.Internal, "git: find the fetch stamp", err)
	}
	defer func() { _ = root.Close() }()
	raw, err := root.ReadFile(filepath.Join(StateDirName, FetchStampFile))
	if err != nil {
		return store.Fresh{}, nil // never fetched here
	}
	at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(raw)))
	if err != nil {
		return store.Fresh{}, nil
	}
	commit, err := b.head(ctx)
	if err != nil {
		return store.Fresh{}, err
	}
	return store.Fresh{Known: true, FetchedAt: at, Commit: commit}, nil
}

// FetchedOnce reports whether the store clone at dir has fetched from its
// remote at least once: its fetch stamp is there. Read without git, for a
// check that only needs to know the remote existed (a doctor fix must not
// offer to create it).
func FetchedOnce(dir string) bool {
	return isFile(filepath.Join(StateDirOf(dir), FetchStampFile))
}
