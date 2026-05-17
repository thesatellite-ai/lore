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
	"context"
	"fmt"
	"os"
	"saas/pkg/constants"

	"dbent/gen/ent"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"

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
// scopeRebind is the decision from resolveScopeRebind. The caller applies
// it via the entity's own SetRepoID / ClearRepoID (ent UpdateOne builders
// share no common interface, so the switch lives at the call site)
type scopeRebind struct {
	set    bool
	repoID string
	clear  bool
}

// bindScopeRebindFlags adds the explicit scope-mutation flags to an `edit`
// command. These are deliberately distinct from the context-only --repo
// (bound by bindCommonFlags) so re-scoping is never silent or ambiguous
func bindScopeRebindFlags(cmd *cobra.Command, rebindRepo *string, rebindMaster *bool) {
	cmd.Flags().StringVar(rebindRepo, constants.FlagRebindRepo, "",
		"move this row to repo <mount> (rebinds repo_id; distinct from context --repo)")
	cmd.Flags().BoolVar(rebindMaster, constants.FlagRebindMaster, false,
		"move this row to master scope (clears repo_id)")
}

// resolveScopeRebind interprets the edit-time scope flags:
//
//   - --rebind-repo=<mount> → resolve + set repo_id (loud error if unknown)
//   - --rebind-master       → clear repo_id (master scope)
//   - bare --repo on edit   → loud stderr note; --repo is context-only here
//     and never mutated scope (this was the silent no-op bug)
//
// Returns the decision; the caller applies it with the entity's builder.
func resolveScopeRebind(cmd *cobra.Command, ctx context.Context, client *ent.Client, projectID, rebindRepo string, rebindMaster bool) (scopeRebind, error) {
	ch := cmd.Flags().Changed
	switch {
	case ch(constants.FlagRebindRepo):
		if rebindRepo == "" {
			return scopeRebind{}, errcodes.New(errcodes.InvalidInput,
				"--rebind-repo requires a repo mount_name or rep_id (use --rebind-master to clear)")
		}
		rid, err := resolveRepoID(ctx, client, projectID, rebindRepo)
		if err != nil {
			return scopeRebind{}, err
		}
		return scopeRebind{set: true, repoID: rid}, nil
	case rebindMaster:
		return scopeRebind{clear: true}, nil
	case ch(constants.FlagRepo):
		fmt.Fprintln(os.Stderr, style.Warn(
			"note: --repo on edit selects context, not scope — this row's scope is "+
				"UNCHANGED. Use --rebind-repo=<mount> to move it, --rebind-master to "+
				"clear, or `add --supersedes=<id> --repo=` for an audited re-scope."))
	}
	return scopeRebind{}, nil
}

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
