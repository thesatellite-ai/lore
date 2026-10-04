package main

// `lore sync ci-merge` — resolve lore-data merge conflicts without a person.
//
// Why it exists: a host's web merge button (GitHub, GitLab) never runs custom
// merge drivers, so two pull requests that edited the same lore row show a
// conflict there even when lore's field-level driver would merge them
// cleanly. This command does, on a machine where lore runs (a CI job), what a
// developer would otherwise do by hand: merge the base branch into the PR
// branch with lore's drivers, and commit — but ONLY when that settles every
// conflict and every conflict was in lore's own files. Anything else (a code
// conflict, the same text edited on both sides) is left untouched for a
// person; the command never pushes a half-merged branch, and never pushes
// at all (the caller does).

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/spf13/cobra"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/gitsetup"
	"saas/pkg/aicoder/style"
	"saas/pkg/constants"
)

// ciMergeOutcome is what `lore sync ci-merge` decided. The generated workflow
// branches on these exact strings (sync_action.yml.tmpl), so they are part of
// the command's contract: renaming one breaks every installed workflow.
type ciMergeOutcome string

// Outcomes of `lore sync ci-merge`.
const (
	// ciMergeUpToDate: the base is already contained in the branch.
	ciMergeUpToDate ciMergeOutcome = "up-to-date"
	// ciMergeClean: a plain merge has no conflicts; the host can merge it.
	ciMergeClean ciMergeOutcome = "clean"
	// ciMergeMerged: lore's drivers settled every conflict and the merge was
	// committed locally; the caller pushes it.
	ciMergeMerged ciMergeOutcome = "merged"
	// ciMergeNeedsHuman: a conflict lore may not or cannot settle; nothing
	// was changed.
	ciMergeNeedsHuman ciMergeOutcome = "needs-human"
)

// ciMergeOutcomes is every outcome, in decision order (tests check each is
// printed and documented).
var ciMergeOutcomes = []ciMergeOutcome{ciMergeUpToDate, ciMergeClean, ciMergeMerged, ciMergeNeedsHuman}

// ciMergeCommand is the command line the generated workflow runs (without
// its flags). The template prints it and parseSyncAction finds the merge step
// by it, so the two can never disagree.
const ciMergeCommand = BinaryName + " sync ci-merge"

// jsonKindSyncCIMerge is the JSON envelope kind of `lore sync ci-merge`.
const jsonKindSyncCIMerge = "sync.ci-merge"

// ciMergeResult is what `lore sync ci-merge` reports.
type ciMergeResult struct {
	// Outcome is one of the ciMerge* constants.
	Outcome ciMergeOutcome `json:"outcome"`
	// Base is the ref that was merged in.
	Base string `json:"base"`
	// Commit is the merge commit (Outcome merged only).
	Commit string `json:"commit,omitempty"`
	// Merged lists the lore files whose conflicts lore's driver settled.
	Merged []string `json:"merged,omitempty"`
	// Blocking lists the paths a person must resolve (Outcome needs-human).
	Blocking []string `json:"blocking,omitempty"`
	// Reason explains a needs-human outcome in one sentence.
	Reason string `json:"reason,omitempty"`
}

func newSyncCIMergeCommand() *cobra.Command {
	var base string
	var dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:   "ci-merge --base <ref>",
		Short: "Merge a base branch into this branch when only lore data conflicts (for CI)",
		Long: `Merges <ref> into the current branch with lore's field-level merge driver and
commits the merge, but only when a plain merge would conflict, every conflict
is in .lore/data or .lore/LORE.md, and lore's driver settles all of them.
Otherwise nothing is changed and the outcome says why.

It never pushes; the workflow installed by ` + "`lore sync install-action`" + ` runs it and
pushes the result. Run it locally to see what that workflow would do.

Outcomes (also in --json): up-to-date, clean (the host can merge as is),
merged (committed here, ready to push), needs-human (left unchanged).
Exits 0 for every outcome; a non-zero exit means the command itself failed.`,
		Example: `  lore sync ci-merge --base origin/main
  lore sync ci-merge --base origin/main --dry-run
  lore sync ci-merge --base origin/main --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(base) == "" {
				return errcodes.New(errcodes.InvalidInput, "--base is required").WithHint("e.g. --base origin/main")
			}
			res, err := runCIMerge(cmd.Context(), ".", base, dryRun)
			if err != nil {
				return err
			}
			if asJSON {
				printJSON(jsonKindSyncCIMerge, res, 0)
				return nil
			}
			printCIMerge(res, dryRun)
			return nil
		},
	}
	cmd.Flags().StringVar(&base, constants.FlagBase, "", "ref to merge in (e.g. origin/main)")
	cmd.Flags().BoolVar(&dryRun, constants.FlagDryRun, false, "report what would happen; change nothing")
	cmd.Flags().BoolVar(&asJSON, constants.FlagJSON, false, "JSON output")
	return cmd
}

// runCIMerge implements `lore sync ci-merge` for the project at dir.
func runCIMerge(ctx context.Context, dir, base string, dryRun bool) (ciMergeResult, error) {
	res := ciMergeResult{Base: base}
	repo, err := gitsetup.Open(ctx, dir)
	if err != nil {
		return res, errcodes.New(errcodes.Unsupported, "lore sync ci-merge needs a git repository").WithCause(err)
	}
	clean, err := gitsetup.IsClean(ctx, dir)
	if err != nil {
		return res, errcodes.New(errcodes.Internal, "git status").WithCause(err)
	}
	if !clean {
		return res, errcodes.New(errcodes.InvalidInput, "the work tree has changes; ci-merge only runs on a clean checkout").
			WithHint("commit or stash them first")
	}
	probe, err := gitsetup.ProbeMerge(ctx, dir, base)
	if err != nil {
		return res, errcodes.New(errcodes.Internal, "test merge of "+base).WithCause(err)
	}
	switch {
	case probe.UpToDate:
		res.Outcome = ciMergeUpToDate
		return res, nil
	case len(probe.Conflicted) == 0:
		res.Outcome = ciMergeClean
		return res, nil
	}
	prefix := lorePathPrefix(repo.Toplevel, dir)
	var foreign []string
	for _, p := range probe.Conflicted {
		if !isLoreManagedPath(prefix, p) {
			foreign = append(foreign, p)
		}
	}
	if len(foreign) > 0 {
		res.Outcome, res.Blocking = ciMergeNeedsHuman, foreign
		res.Reason = "conflicts outside lore data; a person must merge this branch"
		return res, nil
	}
	if dryRun {
		// The driver's verdict needs the real merge; report what would be
		// attempted.
		res.Outcome, res.Merged = ciMergeMerged, probe.Conflicted
		return res, nil
	}
	if err := gitsetup.MergeNoCommit(ctx, dir, base, mergeDriverConfig()); err != nil {
		return res, errcodes.New(errcodes.Internal, "merge "+base).WithCause(err)
	}
	left, err := gitsetup.Unmerged(ctx, dir)
	if err != nil {
		return res, abortCIMerge(ctx, dir, errcodes.New(errcodes.Internal, "list unmerged files").WithCause(err))
	}
	if len(left) > 0 {
		if err := gitsetup.AbortMerge(ctx, dir); err != nil {
			return res, errcodes.New(errcodes.Internal, "abort merge").WithCause(err)
		}
		res.Outcome, res.Blocking = ciMergeNeedsHuman, left
		res.Reason = "the same text was changed on both sides; settle it with `lore <entity> edit` after merging locally"
		return res, nil
	}
	commit, err := gitsetup.CommitMerge(ctx, dir, ciMergeMessage(base, len(probe.Conflicted)))
	if err != nil {
		return res, abortCIMerge(ctx, dir, errcodes.New(errcodes.Internal, "commit merge").WithCause(err))
	}
	res.Outcome, res.Commit, res.Merged = ciMergeMerged, commit, probe.Conflicted
	return res, nil
}

// abortCIMerge undoes the in-progress merge before returning cause, so a
// failed run never leaves a half-merged checkout behind.
func abortCIMerge(ctx context.Context, dir string, cause error) error {
	_ = gitsetup.AbortMerge(ctx, dir) // best effort; cause is the error to report
	return cause
}

// ciMergeMessage is the merge commit's message.
func ciMergeMessage(base string, files int) string {
	return fmt.Sprintf("Merge %s (lore data: %d file(s) merged field by field by lore sync ci-merge)", base, files)
}

// lorePathPrefix is the project's directory relative to the repository root
// ("" at the root, else "<dir>/"), slash separated, as git prints paths.
func lorePathPrefix(toplevel, dir string) string {
	rel := projectRelPath(toplevel, dir)
	if isRepoRoot(rel) {
		return ""
	}
	return rel + "/"
}

// isLoreManagedPath reports whether a repository-relative path is one of the
// files lore writes and its driver can merge: a row or control file under
// .lore/data, or the generated .lore/LORE.md.
func isLoreManagedPath(prefix, p string) bool {
	return strings.HasPrefix(p, prefix+dataRel+"/") || p == prefix+loreMDRel
}

// printCIMerge renders a ci-merge result for a terminal.
func printCIMerge(res ciMergeResult, dryRun bool) {
	switch res.Outcome {
	case ciMergeUpToDate:
		fmt.Printf("%s %s is already merged into this branch\n", style.Success("✓"), res.Base)
	case ciMergeClean:
		fmt.Printf("%s merging %s has no conflicts; the host can merge it as is\n", style.Success("✓"), res.Base)
	case ciMergeMerged:
		verb := "merged and committed"
		if dryRun {
			verb = "would merge"
		}
		fmt.Printf("%s %s %s: %d lore file(s) settled field by field\n", style.Success("✓"), verb, res.Base, len(res.Merged))
		for _, p := range res.Merged {
			fmt.Printf("  %s\n", p)
		}
		if res.Commit != "" {
			fmt.Printf("  commit %s — push it to update the pull request\n", res.Commit)
		}
	case ciMergeNeedsHuman:
		fmt.Printf("%s %s — nothing changed\n", style.Warn("!"), res.Reason)
		for _, p := range res.Blocking {
			fmt.Printf("  %s\n", path.Clean(p))
		}
	}
}
