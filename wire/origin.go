package wire

import (
	"context"
	"strings"
	"sync"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/machine"
	"github.com/thesatellite-ai/lore/logic/origin"
	"github.com/thesatellite-ai/lore/model/ids"
)

// provenance is the engine.Provenance of one opened App: the facts of the
// checkout the command runs in (repo id, work dir, git HEAD) and of the
// writer (lore version, machine id), for `_o` and `_w`
// (PROVENANCE_DESIGN 5.2, 6.2). The git HEAD is read once per open (a
// command's rows are born at one HEAD); the repo id is read on every
// create, because `lore init` mints it in the same process before its
// seed rows are written.
type provenance struct {
	repoTop string
	workDir string
	version string
	machine ids.MachineID

	once sync.Once
	git  origin.Git
}

// newProvenance builds the provenance of an App over the repo whose top
// folder is repoTop. The machine id is derived once here; when the OS has
// none, `_w` simply carries no `m` (never a made-up id).
func newProvenance(opts Options, repoTop string) *provenance {
	p := &provenance{repoTop: repoTop, workDir: opts.WorkDir, version: opts.Version}
	if p.workDir == "" {
		p.workDir = opts.RepoDir
	}
	read := opts.Machine
	if read == nil {
		read = machine.ID
	}
	if m, err := read(); err == nil {
		p.machine = m
	}
	return p
}

// Place implements engine.Provenance.
func (p *provenance) Place(ctx context.Context) origin.Facts {
	p.once.Do(func() { p.git = readGitHead(ctx, p.repoTop) })
	f := origin.Facts{
		// PROV6: the folder the command ran in, relative to the repo root,
		// omitted at the root; never the absolute path.
		Dir: origin.RelDir(p.repoTop, p.workDir),
		Git: p.git,
	}
	if id, ok := repoIDAt(ctx, p.repoTop); ok {
		f.Repo = id
	}
	// `_o.project`: the project the command ran in (PROVENANCE 5.1), read
	// on every create like the repo id (`lore init` writes it first).
	if pid, ok := repoMetaProjectID(p.repoTop); ok {
		f.Project = pid
	}
	return f
}

// Upstream implements engine.Provenance: `git rev-parse --abbrev-ref
// <branch>@{upstream}` succeeds (PROVENANCE_DESIGN 8.1, PROV8).
func (p *provenance) Upstream(ctx context.Context, branch string) bool {
	if branch == "" || strings.HasPrefix(branch, "-") {
		return false
	}
	_, err := gitRun{dir: p.repoTop, args: []string{"rev-parse", "--abbrev-ref", "--symbolic-full-name", branch + "@{upstream}"}}.output(ctx)
	return err == nil
}

// Writer implements engine.Provenance.
func (p *provenance) Writer() (string, ids.MachineID) { return p.version, p.machine }

// readGitHead reads HEAD's state at top (PROVENANCE_DESIGN 5.1, 11.7,
// 11.8): on a branch (name + commit), detached (commit), unborn (a repo
// with no commit yet) or none (not a git repo, or git missing).
func readGitHead(ctx context.Context, top string) origin.Git {
	commit := ""
	if out, err := (gitRun{dir: top, args: []string{"rev-parse", "--verify", "--quiet", "HEAD"}}).output(ctx); err == nil {
		commit = strings.TrimSpace(string(out))
	}
	if out, err := (gitRun{dir: top, args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}}).output(ctx); err == nil {
		if commit == "" {
			return origin.Git{State: origin.GitUnborn}
		}
		return origin.Git{State: origin.GitBranch, Branch: strings.TrimSpace(string(out)), Commit: commit}
	}
	if commit != "" {
		return origin.Git{State: origin.GitDetached, Commit: commit}
	}
	if _, err := (gitRun{dir: top, args: []string{"rev-parse", "--git-dir"}}).output(ctx); err == nil {
		return origin.Git{State: origin.GitUnborn}
	}
	return origin.Git{State: origin.GitNone}
}

// repoIDAt is the permanent repo id of the checkout at top (PROV1,
// `_o.repo`): the same read as App.RepoIdentity (the repo_id of
// .lore/data/_meta.json), done on the top folder the open state already
// holds, because the engine's provenance is built inside open, before
// the App has a state to ask. ok=false leaves `_o.repo` out (nothing is
// guessed, PROV10).
func repoIDAt(_ context.Context, top string) (string, bool) {
	m, _, err := rowfs.ReadRepoMeta(top)
	if err != nil || m.RepoID == "" {
		return "", false
	}
	return m.RepoID, true
}
