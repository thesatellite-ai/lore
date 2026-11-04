package content

import "context"

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
