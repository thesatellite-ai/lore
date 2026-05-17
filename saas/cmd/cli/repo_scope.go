// repo_scope.go — shared repo-scoping decision for list/search commands
//
// Knowledge entities (memory, rule, decision, hotfix, pattern) carry a
// nullable repo_id: NULL = project-master scope, set = repo-specific.
// `add`/`edit` persist it and `lore search` filters by it. Before this
// file, the per-kind `list` commands ignored repo_id entirely (project
// filter only) — so `lore <kind> list --repo X` returned the same rows
// for every repo, which led at least one agent to wrongly conclude
// per-repo scoping didn't exist and fall back to prefix tags.
//
// This centralizes the scope decision so `list` and `search` apply
// IDENTICAL semantics (Rule 2 — single source, no logic drift).
package main

import (
	"saas/pkg/constants"

	"github.com/spf13/cobra"
)

type repoScopeMode int

const (
	// scopeInherit: --repo set, default — repo rows PLUS master rows
	scopeInherit repoScopeMode = iota
	// scopeRepoOnly: --repo + --no-inherit — strictly this repo
	scopeRepoOnly
	// scopeMasterOnly: --master-only, or no --repo at all (master is the
	// safe default — repo-specific rows shouldn't leak into an unscoped list)
	scopeMasterOnly
	// scopeAll: --all-repos — every row regardless of repo
	scopeAll
)

type repoScopeFlags struct {
	allRepos   bool
	masterOnly bool
	noInherit  bool
}

func bindRepoScopeFlags(cmd *cobra.Command, f *repoScopeFlags) {
	cmd.Flags().BoolVar(&f.allRepos, constants.FlagAllRepos, false,
		"list across every repo (ignore --repo)")
	cmd.Flags().BoolVar(&f.masterOnly, constants.FlagMasterOnly, false,
		"only master-scoped rows (repo_id IS NULL)")
	cmd.Flags().BoolVar(&f.noInherit, constants.FlagNoInherit, false,
		"strict scope: --repo only, no master inheritance")
}

// resolveRepoScope encodes the precedence (mirrors search.go's original
// memory switch verbatim so list and search never diverge):
//
//	--all-repos          → every repo
//	--master-only        → repo_id IS NULL
//	--no-inherit + repo  → that repo only
//	--no-inherit         → repo_id IS NULL
//	repo set             → repo + master (inherit)
//	default (no repo)    → repo_id IS NULL (master only)
func resolveRepoScope(f repoScopeFlags, repoID string) repoScopeMode {
	switch {
	case f.allRepos:
		return scopeAll
	case f.masterOnly:
		return scopeMasterOnly
	case f.noInherit && repoID != "":
		return scopeRepoOnly
	case f.noInherit:
		return scopeMasterOnly
	case repoID != "":
		return scopeInherit
	default:
		return scopeMasterOnly
	}
}
