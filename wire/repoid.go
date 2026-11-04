package wire

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	opsinfra "github.com/thesatellite-ai/lore/ops/infra"
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

// The App is the repo identity verbs' RepoFiles (ops/infra): the
// checkout's _meta.json, its raw git remotes, its folder name and the
// acting actor.
var _ opsinfra.RepoFiles = (*App)(nil)

// ReadRepoMeta implements opsinfra.RepoFiles.
func (a *App) ReadRepoMeta(ctx context.Context) (rowfs.RepoMeta, bool, error) {
	top, err := a.knownRepoTop(ctx)
	if err != nil {
		return rowfs.RepoMeta{}, false, err
	}
	return rowfs.ReadRepoMeta(top)
}

// WriteRepoMeta implements opsinfra.RepoFiles. The file is committed by
// the person with their code, like any row file of the repo store.
func (a *App) WriteRepoMeta(ctx context.Context, m rowfs.RepoMeta) error {
	top, err := a.knownRepoTop(ctx)
	if err != nil {
		return err
	}
	return rowfs.WriteRepoMeta(top, m)
}

// remoteURLKeys matches every remote's fetch URL key in git config. A
// remote name may contain dots, so the name is everything between
// "remote." and the last ".url".
const remoteURLKeys = `^remote\..*\.url$`

// RepoRemotes implements opsinfra.RepoFiles: remote.<name>.url as written
// in git config, in config order. Why not `git remote get-url`: it applies
// url.<base>.insteadOf, so a corporate mirror rewrite would replace the
// address the repo is known by (the v0.1.17 rule of rawRemoteURLs). No
// remote at all (git exits 1) is an empty list.
func (a *App) RepoRemotes(ctx context.Context) ([]opsinfra.Remote, error) {
	top, err := a.knownRepoTop(ctx)
	if err != nil {
		return nil, err
	}
	out, err := gitRun{dir: top, args: []string{"config", "--get-regexp", remoteURLKeys}}.output(ctx)
	if err != nil {
		return nil, nil
	}
	var res []opsinfra.Remote
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		key, val, ok := strings.Cut(line, " ")
		val = strings.TrimSpace(val)
		if !ok || val == "" {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
		res = append(res, opsinfra.Remote{Name: name, URL: val})
	}
	return res, nil
}

// RepoDirName implements opsinfra.RepoFiles.
func (a *App) RepoDirName(ctx context.Context) (string, error) {
	top, err := a.knownRepoTop(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Base(top), nil
}

// RepoActor implements opsinfra.RepoFiles.
func (a *App) RepoActor(ctx context.Context) string {
	st, err := a.state(ctx)
	if err != nil || st.actor.IsZero() {
		return ""
	}
	return st.actor.String()
}

// EnsureRepoIdentity gives the checkout its permanent repo id when it has
// none (PROV1): `lore init` and `lore sync` call it. minted reports a new
// id (or an adopted v0.1.17 repos row).
func (a *App) EnsureRepoIdentity(ctx context.Context) (repoID string, minted bool, err error) {
	id, minted, err := opsinfra.EnsureIdentity(ctx, a, a)
	return id.Meta.RepoID, minted, err
}

// CheckRepoAddresses is the sync pass of PROVENANCE_DESIGN 10.3 (PROV2,
// PROV3): new remotes of this clone are recorded `unconfirmed`, and every
// unconfirmed address the clone uses is reported for the notice.
func (a *App) CheckRepoAddresses(ctx context.Context) (opsinfra.AddressReport, error) {
	return opsinfra.CheckAddresses(ctx, a, a)
}

// RepoAddressStatus is the read-only view of `lore doctor` (Repo
// addresses, and Q5's repo_id/remote mismatch).
func (a *App) RepoAddressStatus(ctx context.Context) (opsinfra.AddressReport, error) {
	return opsinfra.AddressStatus(ctx, a, a)
}

// RepoTop implements ops/scope.RepoTopper: the code repository's top
// folder, so --scope dir:<path> checks the folder exists (CONTEXT_BUDGET
// 6.2.3).
func (a *App) RepoTop(ctx context.Context) (string, error) { return a.knownRepoTop(ctx) }
