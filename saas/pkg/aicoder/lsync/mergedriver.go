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

// NewerWins lists the fields where both sides set different values and the
// side with the newer updated_at won (status, priority, dates, references).
// Bookkeeping keys settled on every merge (updated_at, _v, _table) are left
// out: they carry no decision a person needs to hear about.
func (o MergeOutcome) NewerWins() []string {
	var out []string
	for _, k := range o.AutoResolved {
		if k != updatedAtColumn && k != keyVersion && k != keyTable {
			out = append(out, k)
		}
	}
	return out
}

// MergeFiles is the body of `lore merge-driver %O %A %B %P` (git passes the
// ancestor, ours, theirs and the path; the result must be written to ours).
//
// Row files get a field-level three-way merge (§9): updated_at takes the
// max, scalar state takes the newer side, text clashes get conflict markers
// INSIDE the JSON string value — the file stays valid JSON, and the importer
// refuses marker text, so an unresolved conflict can never be imported as
// knowledge. _purged.json merges as a set union (it fails, leaving ours untouched,
// only when a side's id list has the wrong shape). Anything
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
	oDoc, oErr := decodeDriverSide(ours)
	tDoc, tErr := decodeDriverSide(theirs)
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
	if path.Base(filepath.ToSlash(repoPath)) == MetaFileName {
		strategy = metaStrategy
	} else if tbl, _ := oDoc[keyTable].(string); tbl != "" {
		reg, _ := NewRegistry()
		if t, ok := reg.Table(tbl); ok {
			strategy = StrategyFor(t)
		}
	}
	// F4 step 1: settle the file first (add/add, delete versus edit,
	// two tombstones), then field level with the newer side chosen by hlc
	// when both carry one, else by updated_at (step 2).
	bF := merge3.Absent()
	if bDoc != nil {
		bF = driverSide(bDoc)
	}
	oF, tF := driverSide(oDoc), driverSide(tDoc)
	newer := merge3.SideOurs
	if docIsNewer(tDoc, oDoc) {
		newer = merge3.SideTheirs
	}
	fr, err := merge3.MergeFile(bF, oF, tF, merge3.Policy{StrategyFor: strategy, Newer: newer})
	if err != nil {
		return MergeOutcome{}, err
	}
	if fr.File.Kind != merge3.FileRow {
		// A tombstone (or nothing): written as is, no field decisions.
		doc := fr.File.Doc
		if doc == nil {
			doc = oDoc
		}
		out, err := canonjson.Encode(doc)
		if err != nil {
			return MergeOutcome{}, err
		}
		if err := os.WriteFile(oursPath, out, dataFileMode); err != nil {
			return MergeOutcome{}, fmt.Errorf("lsync: write merge result: %w", err)
		}
		return MergeOutcome{}, nil
	}
	res := fr.Fields
	if fr.Decision != merge3.DecisionFieldMerge {
		res = merge3.Result{Merged: fr.File.Doc}
	}
	if fr.Decision == merge3.DecisionModifyBeatsDelete {
		res.AutoResolved = append(res.AutoResolved, keyDeletedVsEdited)
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

// keyDeletedVsEdited is reported in AutoResolved when one side deleted
// the row (a tombstone) and the other edited it: the edit was kept (F4).
const keyDeletedVsEdited = "(deleted on one side, edited on the other: kept)"

// liveBlockKey is the `_live` block of a live file (§3.3); its hlc orders
// edits and deleted:true marks a tombstone.
const liveBlockKey = "_live"

// decodeDriverSide decodes one side: a row file, or a live tombstone
// (which carries only the id and its `_live` block, no `_v`).
func decodeDriverSide(b []byte) (map[string]any, error) {
	doc, err := decodeDoc(b)
	if err == nil {
		return doc, nil
	}
	raw, rerr := canonjson.Decode(b, canonjson.DefaultMaxDepth)
	if rerr == nil && driverSide(raw).Kind == merge3.FileTombstone {
		return raw, nil
	}
	return nil, err
}

// driverSide classifies a decoded side: a tombstone carries
// `_live.deleted: true`, anything else is a row.
func driverSide(doc map[string]any) merge3.File {
	if blk, ok := doc[liveBlockKey].(map[string]any); ok {
		if del, _ := blk["deleted"].(bool); del {
			return merge3.Tombstone(doc)
		}
	}
	return merge3.Row(doc)
}

// docIsNewer reports whether a is the newer edit: by `_live.hlc` when both
// carry one (it orders edits even when clocks disagree, F4), else by
// updated_at.
func docIsNewer(a, b map[string]any) bool {
	ha, hb := liveHLC(a), liveHLC(b)
	if ha != "" && hb != "" {
		return ha > hb // fixed-width form: string order is hlc order
	}
	return firstIsNewer(a, b)
}

func liveHLC(doc map[string]any) string {
	blk, ok := doc[liveBlockKey].(map[string]any)
	if !ok {
		return ""
	}
	s, _ := blk["hlc"].(string)
	return s
}

// metaStrategy merges _meta.json: links is a set (two concurrent `lore org
// link` runs both survive, never markers); anything else is a loud
// conflict.
func metaStrategy(key string) merge3.Strategy {
	if key == metaKeyLinks {
		return merge3.StrategySet
	}
	if key == keyVersion {
		return merge3.StrategyMax
	}
	return merge3.StrategyConflict
}

// metaKeyLinks is the list of links in a store project's _meta.json (F11).
const metaKeyLinks = "links"

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

// mergePurged unions two _purged.json id lists. A side whose id list has
// the wrong shape fails the merge (ours is left as it was and git reports a
// conflict): writing a union without that side's ids would let the rows it
// purged come back.
func mergePurged(oursPath string, oDoc, tDoc map[string]any) (MergeOutcome, error) {
	set := map[string]bool{}
	for _, d := range []map[string]any{oDoc, tDoc} {
		ids, err := purgedIDList(d)
		if err != nil {
			return MergeOutcome{}, fmt.Errorf("lsync: merge %s: %w", PurgedFileName, err)
		}
		for _, id := range ids {
			set[id] = true
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
