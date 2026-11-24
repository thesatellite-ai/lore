package content

import (
	"context"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"github.com/thesatellite-ai/lore/store"
)

// Scope is which rows a read returns by their repo_id (v0.1.17
// saas/cmd/cli/repo_scope.go, as ops/memory keeps it).
type Scope string

// Scopes.
const (
	// ScopeMaster: only project-wide rows (no repo_id).
	ScopeMaster Scope = "master"
	// ScopeInherit: --repo X: X's rows plus the project-wide ones.
	ScopeInherit Scope = "inherit"
	// ScopeRepoOnly: --repo X --no-inherit: only X's.
	ScopeRepoOnly Scope = "repo_only"
	// ScopeAll: --all-repos: every row.
	ScopeAll Scope = "all"
)

// ScopeOf applies v0.1.17's precedence: --all-repos, then --master-only,
// then --no-inherit (only with a repo), then --repo; else project-wide.
func ScopeOf(repoID string, noInherit, masterOnly, allRepos bool) Scope {
	switch {
	case allRepos:
		return ScopeAll
	case masterOnly:
		return ScopeMaster
	case noInherit && repoID != "":
		return ScopeRepoOnly
	case repoID != "":
		return ScopeInherit
	}
	return ScopeMaster
}

// Keeps reports whether a row with repo_id got belongs in the scope of
// repo want.
func (s Scope) Keeps(got, want string) bool {
	switch s {
	case ScopeAll:
		return true
	case ScopeInherit:
		return got == "" || got == want
	case ScopeRepoOnly:
		return got == want
	case ScopeMaster:
	}
	return got == ""
}

// ReadScope resolves a read's --repo and returns the scope and repo id.
func (o Ops) ReadScope(ctx context.Context, repo string, noInherit, masterOnly, allRepos bool) (Scope, string, error) {
	id := ""
	if repo != "" {
		r, err := o.Env.RepoID(ctx, repo)
		if err != nil {
			return "", "", err
		}
		id = r.String()
	}
	return ScopeOf(id, noInherit, masterOnly, allRepos), id, nil
}

// ProjectScope is which projects' rows a list or search of a content kind
// keeps (ProjectScopeOf). Without --project it is the calling repo's
// project, plus every row of a store folder: a write into a folder takes
// the folder's own project (engine placeFolderProject, D-718), so a
// repo-project test alone hid every note written with `--to
// <store>/<folder>` from `<kind> list` and `<kind> search` while `lore
// search` found it (devco D-965). Rows another repo wrote into the repo
// store or a store's root still stay out unless --project names them
// (v0.1.17). With --project only that project's rows are kept.
type ProjectScope struct {
	id    string // "" = every project
	named bool   // --project was given: no folder exception
}

// Keeps reports whether row r, read from src, is in the project scope.
func (p ProjectScope) Keeps(src store.Source, r row.Row) bool {
	switch {
	case p.id == "", Field(r, FieldProject) == p.id:
		return true
	case p.named:
		return false
	}
	return engine.IsFolderSource(src)
}

// ProjectScopeOf resolves a read's --project flag (ProjectFilter) into
// the scope its list and search keep.
func (o Ops) ProjectScopeOf(ctx context.Context, flag string) (ProjectScope, error) {
	id, err := o.ProjectFilter(ctx, flag)
	if err != nil {
		return ProjectScope{}, err
	}
	return ProjectScope{id: id, named: flag != ""}, nil
}

// Hidden counts what a list leaves out (metalist.Hidden).
type Hidden = metalist.Hidden

// KeepCounted is a list's scope test that counts what it hides: a row
// scoped to a repo outside sc is left out and counted (as scoped to a
// folder when it has a dir: scope, its folder read through dirs; folder
// scopes are rows of the repo store); an archived row the list will drop
// (withArchived false: the metadata narrowing drops it) is counted and
// kept for that step.
func KeepCounted(ctx context.Context, h *Hidden, dirs *metalist.Dirs, sc Scope, repo string, withArchived bool, r row.Row) bool {
	if !sc.Keeps(Field(r, FieldRepo), repo) {
		h.OutOfScope(dirs.Of(ctx, store.SourceRepo, r))
		return false
	}
	if !withArchived && Field(r, FieldArchivedAt) != "" {
		h.Archived++
	}
	return true
}
