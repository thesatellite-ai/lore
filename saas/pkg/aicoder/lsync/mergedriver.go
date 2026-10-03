package lsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"

	"saas/pkg/aicoder/canonjson"
	"saas/pkg/aicoder/merge3"
)

// gitBinary is the git executable the text-merge fallback shells out to.
// The merge driver only ever runs underneath git, so it is on PATH.
const gitBinary = "git"

// MergeOutcome is the result of MergeFiles.
type MergeOutcome struct {
	// Conflicted is true when a human must finish the merge (git's driver
	// contract: exit non-zero). The file then holds conflict markers.
	Conflicted bool
	// AutoResolved lists keys settled by newest-wins / max rules.
	AutoResolved []string
	// Fallback is true when the files were not lore rows and git's own
	// line merge was used instead.
	Fallback bool
}

// MergeFiles is the body of `lore merge-driver %O %A %B %P` (git passes the
// ancestor, ours, theirs and the path; the result must be written to ours).
//
// Row files get a field-level three-way merge (§9): updated_at takes the
// max, scalar state takes the newer side, text clashes get conflict markers
// INSIDE the JSON string value — the file stays valid JSON, and the importer
// refuses marker text, so an unresolved conflict can never be imported as
// knowledge. _purged.json merges as a set union (never conflicts). Anything
// that is not a lore document (or is a newer format this binary cannot
// fully represent) falls back to `git merge-file`.
func MergeFiles(ctx context.Context, basePath, oursPath, theirsPath, repoPath string) (MergeOutcome, error) {
	base, err := os.ReadFile(basePath)
	if err != nil {
		return MergeOutcome{}, fmt.Errorf("lsync: read base: %w", err)
	}
	ours, err := os.ReadFile(oursPath)
	if err != nil {
		return MergeOutcome{}, fmt.Errorf("lsync: read ours: %w", err)
	}
	theirs, err := os.ReadFile(theirsPath)
	if err != nil {
		return MergeOutcome{}, fmt.Errorf("lsync: read theirs: %w", err)
	}
	oDoc, oErr := decodeDoc(ours)
	tDoc, tErr := decodeDoc(theirs)
	var bDoc map[string]any
	if len(bytes.TrimSpace(base)) > 0 {
		bDoc, err = decodeDoc(base)
		if err != nil {
			return textMerge(ctx, basePath, oursPath, theirsPath)
		}
	}
	if oErr != nil || tErr != nil {
		return textMerge(ctx, basePath, oursPath, theirsPath)
	}
	if path.Base(filepath.ToSlash(repoPath)) == PurgedFileName {
		return mergePurged(oursPath, oDoc, tDoc)
	}

	strategy := func(string) merge3.Strategy { return merge3.StrategyConflict }
	if tbl, _ := oDoc[keyTable].(string); tbl != "" {
		reg, _ := NewRegistry()
		if t, ok := reg.Table(tbl); ok {
			strategy = StrategyFor(t)
		}
	}
	newer := merge3.SideOurs
	if firstIsNewer(tDoc, oDoc) {
		newer = merge3.SideTheirs
	}
	res, err := merge3.Merge(bDoc, oDoc, tDoc, merge3.Policy{StrategyFor: strategy, Newer: newer})
	if err != nil {
		return MergeOutcome{}, err
	}
	for _, c := range res.Conflicts {
		if _, isStr := res.Merged[c.Key].(string); isStr && c.Ours != nil {
			if _, oursStr := c.Ours.(string); oursStr {
				continue // already carries MarkConflict markers
			}
		}
		// Non-string clash (JSON object, number, deletion): make it visible
		// as marked text so the conflict cannot hide in a valid-looking file.
		res.Merged[c.Key] = merge3.MarkConflict(compactOrNull(c.Ours), compactOrNull(c.Theirs))
	}
	out, err := canonjson.Encode(res.Merged)
	if err != nil {
		return MergeOutcome{}, err
	}
	if err := os.WriteFile(oursPath, out, dataFileMode); err != nil {
		return MergeOutcome{}, fmt.Errorf("lsync: write merge result: %w", err)
	}
	return MergeOutcome{Conflicted: len(res.Conflicts) > 0, AutoResolved: res.AutoResolved}, nil
}

func compactOrNull(v any) string {
	if v == nil {
		return "null"
	}
	s, err := canonjson.CompactValue(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return s
}

// mergePurged unions two _purged.json id lists.
func mergePurged(oursPath string, oDoc, tDoc map[string]any) (MergeOutcome, error) {
	set := map[string]bool{}
	for _, d := range []map[string]any{oDoc, tDoc} {
		list, _ := d[purgedKeyIDs].([]any)
		for _, v := range list {
			if s, ok := v.(string); ok {
				set[s] = true
			}
		}
	}
	b, err := encodePurged(set)
	if err != nil {
		return MergeOutcome{}, err
	}
	if err := os.WriteFile(oursPath, b, dataFileMode); err != nil {
		return MergeOutcome{}, fmt.Errorf("lsync: write merge result: %w", err)
	}
	return MergeOutcome{}, nil
}

// textMerge runs git's own three-way line merge in place on oursPath.
// `git merge-file` exits with the number of conflicts (>0) or a negative
// value / error code on failure.
func textMerge(ctx context.Context, basePath, oursPath, theirsPath string) (MergeOutcome, error) {
	cmd := exec.CommandContext(ctx, gitBinary, "merge-file", "-L", "ours", "-L", "base", "-L", "theirs", oursPath, basePath, theirsPath)
	err := cmd.Run()
	if err == nil {
		return MergeOutcome{Fallback: true}, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() > 0 && exitErr.ExitCode() < 128 {
		return MergeOutcome{Fallback: true, Conflicted: true}, nil
	}
	return MergeOutcome{}, fmt.Errorf("lsync: git merge-file: %w", err)
}
