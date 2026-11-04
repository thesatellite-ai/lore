package memory

import (
	"context"

	"github.com/thesatellite-ai/lore/model/row"
)

// scope is which memories a read returns by their repo_id (v0.1.17
// saas/cmd/cli/repo_scope.go, kept exactly).
type scope string

// Scopes.
const (
	// scopeMaster: only project-wide memories (no repo_id). The default.
	scopeMaster scope = "master"
	// scopeInherit: --repo X: X's memories plus the project-wide ones.
	scopeInherit scope = "inherit"
	// scopeRepoOnly: --repo X --no-inherit: only X's.
	scopeRepoOnly scope = "repo_only"
	// scopeAll: --all-repos: every memory.
	scopeAll scope = "all"
)

// scopeOf applies v0.1.17's precedence: --all-repos, then --master-only,
// then --no-inherit (only with a repo), then --repo; else project-wide.
func scopeOf(repoID string, noInherit, masterOnly, allRepos bool) scope {
	switch {
	case allRepos:
		return scopeAll
	case masterOnly:
		return scopeMaster
	case noInherit && repoID != "":
		return scopeRepoOnly
	case repoID != "":
		return scopeInherit
	}
	return scopeMaster
}

// keeps reports whether a memory with repo_id got belongs in the scope of
// repo want.
func (s scope) keeps(got, want string) bool {
	switch s {
	case scopeAll:
		return true
	case scopeInherit:
		return got == "" || got == want
	case scopeRepoOnly:
		return got == want
	case scopeMaster:
	}
	return got == ""
}

// readScope resolves a read's --repo and returns the scope and repo id.
func (o ops) readScope(ctx context.Context, repo string, noInherit, masterOnly, allRepos bool) (scope, string, error) {
	id := ""
	if repo != "" {
		r, err := o.env.RepoID(ctx, repo)
		if err != nil {
			return "", "", err
		}
		id = r.String()
	}
	return scopeOf(id, noInherit, masterOnly, allRepos), id, nil
}

// repoOf is a memory row's repo_id ("" = project-wide).
func repoOf(r row.Row) string { return field(r, fieldRepo) }
