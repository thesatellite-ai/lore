// Package merge3 is a forwarding shell: the code moved to
// github.com/thesatellite-ai/lore/logic/merge3 (PASS3 P3-PORT-LIVE), which
// merges canonjson.Value objects instead of `any`. This shell keeps the
// v0.1.17 map[string]any API for the old saas importers and converts at
// the edge through the saas canonjson shell (ToValue / FromValue): merged
// numbers come back as json.Number, as from canonjson.Decode. New code
// imports the root package directly.
package merge3

import (
	"fmt"

	rootjson "github.com/thesatellite-ai/lore/logic/canonjson"
	root "github.com/thesatellite-ai/lore/logic/merge3"

	"saas/pkg/aicoder/canonjson"
)

// Strategy forwards to the root package.
type Strategy = root.Strategy

// Side forwards to the root package.
type Side = root.Side

// Policy forwards to the root package.
type Policy = root.Policy

// FileKind forwards to the root package.
type FileKind = root.FileKind

// FileDecision forwards to the root package.
type FileDecision = root.FileDecision

// Constants forwarded from the root package.
const (
	StrategyConflict = root.StrategyConflict
	StrategyNewest   = root.StrategyNewest
	StrategyMax      = root.StrategyMax
	StrategySet      = root.StrategySet

	SideOurs   = root.SideOurs
	SideTheirs = root.SideTheirs

	MarkerOurs   = root.MarkerOurs
	MarkerSplit  = root.MarkerSplit
	MarkerTheirs = root.MarkerTheirs

	FileAbsent    = root.FileAbsent
	FileRow       = root.FileRow
	FileTombstone = root.FileTombstone

	DecisionSame              = root.DecisionSame
	DecisionOursOnly          = root.DecisionOursOnly
	DecisionTheirsOnly        = root.DecisionTheirsOnly
	DecisionFieldMerge        = root.DecisionFieldMerge
	DecisionModifyBeatsDelete = root.DecisionModifyBeatsDelete
	DecisionBothDeleted       = root.DecisionBothDeleted
)

// Conflict is root.Conflict with the values in the v0.1.17 form (string,
// json.Number, bool, nil, []any, map[string]any; nil when deleted).
type Conflict struct {
	Key    string
	Ours   any
	Theirs any
}

// Result is root.Result in the v0.1.17 form.
type Result struct {
	Merged       map[string]any
	Conflicts    []Conflict
	AutoResolved []string
}

// File is root.File in the v0.1.17 form. Invariant: Doc is nil exactly
// when Kind is FileAbsent.
type File struct {
	Kind FileKind
	Doc  map[string]any
}

// FileResult is root.FileResult in the v0.1.17 form.
type FileResult struct {
	File      File
	Decision  FileDecision
	DeletedBy Side
	Fields    Result
}

// Conflicted reports whether a human must finish the merged row.
func (r FileResult) Conflicted() bool { return len(r.Fields.Conflicts) > 0 }

// Absent is the "no file" side.
func Absent() File { return File{Kind: FileAbsent} }

// Row wraps a row document.
func Row(doc map[string]any) File { return File{Kind: FileRow, Doc: doc} }

// Tombstone wraps a tombstone document.
func Tombstone(doc map[string]any) File { return File{Kind: FileTombstone, Doc: doc} }

// MarkConflict forwards to the root package.
func MarkConflict(ours, theirs string) string { return root.MarkConflict(ours, theirs) }

// ContainsConflictMarkers forwards to the root package.
func ContainsConflictMarkers(s string) bool { return root.ContainsConflictMarkers(s) }

// Merge converts the three objects, merges them in the root package and
// converts the result back.
func Merge(base, ours, theirs map[string]any, p Policy) (Result, error) {
	b, err := toObject(base)
	if err != nil {
		return Result{}, err
	}
	o, err := toObject(ours)
	if err != nil {
		return Result{}, err
	}
	t, err := toObject(theirs)
	if err != nil {
		return Result{}, err
	}
	r, err := root.Merge(b, o, t, p)
	if err != nil {
		return Result{}, err
	}
	return fromResult(r), nil
}

// MergeFile converts the three files, merges them in the root package and
// converts the result back.
func MergeFile(base, ours, theirs File, p Policy) (FileResult, error) {
	var in [3]root.File
	for i, f := range []File{base, ours, theirs} {
		doc, err := toObject(f.Doc)
		if err != nil {
			return FileResult{}, err
		}
		in[i] = root.File{Kind: f.Kind, Doc: doc}
	}
	r, err := root.MergeFile(in[0], in[1], in[2], p)
	if err != nil {
		return FileResult{}, err
	}
	return FileResult{File: fromFile(r.File), Decision: r.Decision, DeletedBy: r.DeletedBy, Fields: fromResult(r.Fields)}, nil
}

// toObject converts one v0.1.17 object; nil stays nil (the FileAbsent
// invariant and the add/add base depend on it).
func toObject(m map[string]any) (rootjson.Object, error) {
	if m == nil {
		return nil, nil
	}
	v, err := canonjson.ToValue(m)
	if err != nil {
		return nil, fmt.Errorf("merge3: %w", err)
	}
	o, _ := v.AsObject()
	return o, nil
}

func fromObject(o rootjson.Object) map[string]any {
	if o == nil {
		return nil
	}
	m, _ := canonjson.FromValue(rootjson.ObjectValue(o)).(map[string]any)
	return m
}

func fromFile(f root.File) File { return File{Kind: f.Kind, Doc: fromObject(f.Doc)} }

func fromResult(r root.Result) Result {
	out := Result{Merged: fromObject(r.Merged), AutoResolved: r.AutoResolved}
	for _, c := range r.Conflicts {
		out.Conflicts = append(out.Conflicts, Conflict{Key: c.Key, Ours: canonjson.FromValue(c.Ours), Theirs: canonjson.FromValue(c.Theirs)})
	}
	return out
}
