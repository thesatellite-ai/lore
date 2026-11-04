package wire

import (
	"context"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
)

// RepoIdentity is the permanent repo id of this checkout: the repo_id of
// its .lore/data/_meta.json (PROV1). ok is false when the checkout has no
// identity yet (a v0.1.17 checkout before its first 0.1.18 sync or `lore
// init`), when the file cannot be read, or when the code repository cannot
// be opened; the caller then simply leaves the repo out (rows store
// `_o.repo` only when it is known, PROV10: nothing is guessed).
//
// It only reads: minting the id is EnsureRepoIdentity's job, so a read
// path never writes a file.
func (a *App) RepoIdentity(ctx context.Context) (repoID string, ok bool) {
	top, err := a.knownRepoTop(ctx)
	if err != nil {
		return "", false
	}
	m, _, err := rowfs.ReadRepoMeta(top)
	if err != nil || m.RepoID == "" {
		return "", false
	}
	return m.RepoID, true
}

// knownRepoTop is the code repository's top folder, from the state
// already open when there is one (so a call made during a write opens
// nothing new), else from the repo-only state.
func (a *App) knownRepoTop(ctx context.Context) (string, error) {
	a.mu.Lock()
	st := a.st
	a.mu.Unlock()
	if st != nil {
		return st.repoTop, nil
	}
	return a.repoTop(ctx)
}
