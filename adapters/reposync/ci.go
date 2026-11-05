package reposync

// ci.go — `lore sync ci-merge`: settle lore-data merge conflicts without a
// person (ported from saas/cmd/cli sync_ci.go; LORE_TEAM_SPEC command table,
// LORE_SYNC 9.1).
//
// Why it exists: a host's web merge button (GitHub, GitLab) never runs
// custom merge drivers, so two pull requests that edited the same lore row
// show a conflict there even when lore's field-level driver would merge
// them cleanly. This does, on a machine where lore runs (a CI job), what a
// developer would do by hand: merge the base branch into the PR branch
// with lore's drivers and commit, but ONLY when that settles every
// conflict and every conflict was in lore's own files. Anything else is
// left untouched for a person; it never pushes (the caller does).
//
//	clean checkout? ─no─▶ E_INVALID_INPUT
//	CheckBase (RX7 key loosening, OV64) ─▶ its refusal
//	probe (plain text merge, nothing touched)
//	  up to date ─▶ up-to-date · no conflict ─▶ clean
//	  a conflict outside lore files ─▶ needs-human (nothing changed)
//	  --dry-run ─▶ merged (would)
//	merge --no-commit with lore's drivers (git -c, attributes file)
//	  anything left unmerged ─▶ abort ─▶ needs-human
//	  else commit (--no-verify) ─▶ merged

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// Outcome is what ci-merge decided. The generated workflow branches on
// these exact strings (action.yml.tmpl), so they are part of the command's
// contract: renaming one breaks every installed workflow.
type Outcome string

// Outcomes, in decision order.
const (
	// UpToDate: the base is already contained in the branch.
	UpToDate Outcome = "up-to-date"
	// Clean: a plain merge has no conflicts; the host can merge it.
	Clean Outcome = "clean"
	// Merged: lore's drivers settled every conflict and the merge was
	// committed locally; the caller pushes it.
	Merged Outcome = "merged"
	// NeedsHuman: a conflict lore may not or cannot settle; nothing changed.
	NeedsHuman Outcome = "needs-human"
)

// Outcomes is every outcome (tests check each is printed).
var Outcomes = []Outcome{UpToDate, Clean, Merged, NeedsHuman}

// BinaryName is the program name the generated workflow calls.
const BinaryName = "lore"

// CIMergeCommand is the command line the generated workflow runs (without
// its flags). The template prints it and ParseAction finds the merge step
// by it, so the two can never disagree.
const CIMergeCommand = BinaryName + " sync ci-merge"

// Data paths relative to a lore project, slash separated as git prints.
const (
	dataRel   = ".lore/data"
	loreMDRel = ".lore/LORE.md"
	rootRel   = "."
)

// Merge driver names the attributes refer to (v0.1.17 sync_wire.go).
const (
	driverLore     = "lore"
	driverKeepOurs = "lore-keep-ours"
)

// AttributeLines are the git attributes lore's merge needs, relative to the
// project root: row files merge with lore's field-level driver, the
// generated LORE.md keeps ours (re-rendered after the merge).
var AttributeLines = []string{
	".lore/data/**/*.json text eol=lf merge=" + driverLore + " linguist-generated=true",
	".lore/data/*.json text eol=lf merge=" + driverLore + " linguist-generated=true",
	loreMDRel + " text eol=lf merge=" + driverKeepOurs + " linguist-generated=true",
}

// CIMergeResult is what ci-merge reports (the --json data keys).
type CIMergeResult struct {
	Outcome  Outcome  `json:"outcome"`
	Base     string   `json:"base"`
	Commit   string   `json:"commit,omitempty"`
	Merged   []string `json:"merged,omitempty"`
	Blocking []string `json:"blocking,omitempty"`
	Reason   string   `json:"reason,omitempty"`
}

// CIMergeOptions are ci-merge's inputs.
type CIMergeOptions struct {
	// Dir is the lore project folder (inside the git work tree).
	Dir string
	// Base is the ref merged in (origin/main).
	Base string
	// DryRun reports what would happen and changes nothing.
	DryRun bool
	// Driver is the program git runs as the merge driver (an absolute path,
	// single-quoted when needed; BinaryName when empty). Supplied per git
	// invocation, never written to the repository's config.
	Driver string
	// CheckBase, when set, runs before any merge is tried (RX7: a key row
	// the branch loosened without a changed_by is refused, OV64); its
	// error is returned as is.
	CheckBase func(ctx context.Context, dir, base string) error
}

// CIMerge implements `lore sync ci-merge` (see the file comment).
func CIMerge(ctx context.Context, o CIMergeOptions) (CIMergeResult, error) {
	res := CIMergeResult{Base: o.Base}
	if strings.TrimSpace(o.Base) == "" {
		return res, errs.New(errs.InvalidInput, "--base is required").WithHint("e.g. --base origin/main")
	}
	top, err := toplevel(ctx, o.Dir)
	if err != nil {
		return res, errs.Wrap(errs.Unsupported, "lore sync ci-merge needs a git repository", err)
	}
	clean, err := isClean(ctx, o.Dir)
	if err != nil {
		return res, errs.Wrap(errs.Internal, "git status", err)
	}
	if !clean {
		return res, errs.New(errs.InvalidInput, "the work tree has changes; ci-merge only runs on a clean checkout").
			WithHint("commit or stash them first")
	}
	if o.CheckBase != nil {
		if err := o.CheckBase(ctx, o.Dir, o.Base); err != nil {
			return res, err
		}
	}
	probe, err := probeMerge(ctx, o.Dir, o.Base)
	if err != nil {
		return res, errs.Wrap(errs.Internal, "test merge of "+o.Base, err)
	}
	switch {
	case probe.upToDate:
		res.Outcome = UpToDate
		return res, nil
	case len(probe.conflicted) == 0:
		res.Outcome = Clean
		return res, nil
	}
	prefix := PathPrefix(top, o.Dir)
	var foreign []string
	for _, p := range probe.conflicted {
		if !IsLoreManagedPath(prefix, p) {
			foreign = append(foreign, p)
		}
	}
	if len(foreign) > 0 {
		res.Outcome, res.Blocking = NeedsHuman, foreign
		res.Reason = "conflicts outside lore data; a person must merge this branch"
		return res, nil
	}
	if o.DryRun {
		// The driver's verdict needs the real merge; report the attempt.
		res.Outcome, res.Merged = Merged, probe.conflicted
		return res, nil
	}
	cfg, cleanup, err := mergeConfig(prefix, o.Driver)
	if err != nil {
		return res, errs.Wrap(errs.Internal, "merge attributes", err)
	}
	defer cleanup()
	if mergeErr := mergeNoCommit(ctx, o.Dir, o.Base, cfg); mergeErr != nil {
		return res, errs.Wrap(errs.Internal, "merge "+o.Base, mergeErr)
	}
	left, err := unmerged(ctx, o.Dir)
	if err != nil {
		return res, abortWith(ctx, o.Dir, errs.Wrap(errs.Internal, "list unmerged files", err))
	}
	if len(left) > 0 {
		if abortErr := abortMerge(ctx, o.Dir); abortErr != nil {
			return res, errs.Wrap(errs.Internal, "abort merge", abortErr)
		}
		res.Outcome, res.Blocking = NeedsHuman, left
		res.Reason = "the same text was changed on both sides; settle it with `lore <entity> edit` after merging locally"
		return res, nil
	}
	commit, err := commitMerge(ctx, o.Dir, ciMergeMessage(o.Base, len(probe.conflicted)))
	if err != nil {
		return res, abortWith(ctx, o.Dir, errs.Wrap(errs.Internal, "commit merge", err))
	}
	res.Outcome, res.Commit, res.Merged = Merged, commit, probe.conflicted
	return res, nil
}

// mergeConfig is the per-invocation git config of the merge: lore's two
// drivers, and an attributes file (core.attributesFile, below the repo's
// own .gitattributes in priority) binding lore's files to them, so the
// merge works in a clone whose .gitattributes was never committed.
func mergeConfig(prefix, driver string) ([][2]string, func(), error) {
	if driver == "" {
		driver = BinaryName
	}
	f, err := os.CreateTemp("", "lore-ci-merge-attributes-*")
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { _ = os.Remove(f.Name()) } // a leftover temp file harms nothing
	var b strings.Builder
	for _, l := range AttributeLines {
		b.WriteString(prefix + l + "\n")
	}
	if _, err := f.WriteString(b.String()); err != nil {
		_ = f.Close() // the write error is the one to report
		cleanup()
		return nil, func() {}, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return [][2]string{
		{"core.attributesFile", filepath.ToSlash(f.Name())},
		{"merge." + driverLore + ".name", "lore field-level row merge"},
		{"merge." + driverLore + ".driver", driver + " merge-driver %O %A %B %P"},
		{"merge." + driverKeepOurs + ".name", "lore: keep ours, re-render after merge"},
		{"merge." + driverKeepOurs + ".driver", driver + " merge-driver --keep-ours %O %A %B %P"},
	}, cleanup, nil
}

// abortWith undoes the in-progress merge before returning cause, so a
// failed run never leaves a half-merged checkout behind.
func abortWith(ctx context.Context, dir string, cause error) error {
	_ = abortMerge(ctx, dir) // best effort; cause is the error to report
	return cause
}

// ciMergeMessage is the merge commit's message.
func ciMergeMessage(base string, files int) string {
	return fmt.Sprintf("Merge %s (lore data: %d file(s) merged field by field by lore sync ci-merge)", base, files)
}

// PathPrefix is the project's folder relative to the repository root ("" at
// the root or outside it, else "<dir>/"), slash separated as git prints.
func PathPrefix(top, dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	if real, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
		abs = real
	}
	if real, evalErr := filepath.EvalSymlinks(top); evalErr == nil {
		top = real
	}
	rel, err := filepath.Rel(top, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	rel = filepath.ToSlash(rel)
	if rel == "" || rel == rootRel {
		return ""
	}
	return rel + "/"
}

// IsLoreManagedPath reports whether a repository-relative path is one of
// the files lore writes and its driver can merge: a row or control file
// under .lore/data, or the generated .lore/LORE.md.
func IsLoreManagedPath(prefix, p string) bool {
	return strings.HasPrefix(p, prefix+dataRel+"/") || p == prefix+loreMDRel
}

// CIMergeLines renders a ci-merge result for a terminal.
func CIMergeLines(res CIMergeResult, dryRun bool) []string {
	var out []string
	switch res.Outcome {
	case UpToDate:
		out = append(out, fmt.Sprintf("✓ %s is already merged into this branch", res.Base))
	case Clean:
		out = append(out, fmt.Sprintf("✓ merging %s has no conflicts; the host can merge it as is", res.Base))
	case Merged:
		verb := "merged and committed"
		if dryRun {
			verb = "would merge"
		}
		out = append(out, fmt.Sprintf("✓ %s %s: %d lore file(s) settled field by field", verb, res.Base, len(res.Merged)))
		for _, p := range res.Merged {
			out = append(out, "  "+p)
		}
		if res.Commit != "" {
			out = append(out, fmt.Sprintf("  commit %s — push it to update the pull request", res.Commit))
		}
	case NeedsHuman:
		out = append(out, fmt.Sprintf("! %s — nothing changed", res.Reason))
		for _, p := range res.Blocking {
			out = append(out, "  "+path.Clean(p))
		}
	}
	return out
}
