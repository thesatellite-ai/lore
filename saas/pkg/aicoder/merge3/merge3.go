// Package merge3 performs a field-level three-way merge of flat JSON objects.
//
// Why it exists: git merges text line by line. Two branches that edit
// different fields of the same lore row both also bump `updated_at`, so
// git's line merge reports a conflict even though nothing really clashes.
// A field-level merge resolves that automatically and stays LOUD only where
// two people genuinely changed the same thing (LORE_SYNC_SPEC.md §9).
//
// The package is generic: callers decide per key whether a clash is resolved
// by "newest side wins" or reported as a conflict, and which side is newest.
// It knows nothing about lore, tables or git.
package merge3

import (
	"fmt"
	"sort"
	"strings"

	"saas/pkg/aicoder/canonjson"
)

// Strategy says how a key is resolved when BOTH sides changed it to
// different values.
type Strategy int

const (
	// StrategyConflict reports the clash. For string values the merged value
	// carries git-style conflict markers so a human can resolve it in place.
	StrategyConflict Strategy = iota
	// StrategyNewest silently takes the newer side's value (the caller says
	// which side is newer). Use only for scalar state where "last write
	// wins" is the expected semantics (status, priority, timestamps).
	StrategyNewest
	// StrategyMax takes the larger of the two values by string comparison.
	// Intended for RFC3339 UTC timestamps such as updated_at, which compare
	// correctly as strings.
	StrategyMax
)

// Side identifies one side of the merge.
type Side int

const (
	// SideOurs is the current branch (git's %A).
	SideOurs Side = iota
	// SideTheirs is the branch being merged in (git's %B).
	SideTheirs
)

// Conflict markers, identical in shape to git's so editors highlight them.
const (
	// MarkerOurs opens a conflict; our side follows.
	MarkerOurs = "<<<<<<< ours"
	// MarkerSplit separates our side from theirs.
	MarkerSplit = "======="
	// MarkerTheirs closes a conflict after their side.
	MarkerTheirs = ">>>>>>> theirs"
)

// Policy configures a merge.
type Policy struct {
	// StrategyFor returns the strategy for a key. nil means every key uses
	// StrategyConflict.
	StrategyFor func(key string) Strategy
	// Newer says which side wins StrategyNewest clashes.
	Newer Side
}

// Conflict describes one key both sides changed incompatibly.
type Conflict struct {
	// Key is the top-level object key both sides changed.
	Key string
	// Ours and Theirs are the two values as JSON decodes them (string,
	// float64/json.Number, bool, nil, []any, map[string]any). merge3 is
	// schema-agnostic, so the caller narrows them using its own schema.
	Ours   any
	Theirs any
}

// Result is the outcome of Merge.
//
// Merged always holds a complete object: for conflicting keys it holds a
// string with conflict markers (string values) or the ours value (others),
// so it can be written to disk for a human to finish.
type Result struct {
	Merged    map[string]any
	Conflicts []Conflict
	// AutoResolved lists keys a StrategyNewest / StrategyMax rule settled,
	// so callers can tell the user which silent decisions were taken.
	AutoResolved []string
}

// presence models "key absent" distinctly from "key present with null".
type presence struct {
	ok  bool
	val any
}

// Merge three-way merges base → ours and base → theirs.
//
// base may be nil (both sides added the object independently: an add/add
// merge). Rules per key, in order:
//   - unchanged on both sides, or changed identically → that value
//   - changed on one side only → that side's value (including deletion)
//   - changed on both sides differently → the key's Strategy
func Merge(base, ours, theirs map[string]any, p Policy) (Result, error) {
	keys := unionKeys(base, ours, theirs)
	res := Result{Merged: make(map[string]any, len(keys))}
	for _, k := range keys {
		b, o, t := get(base, k), get(ours, k), get(theirs, k)
		oEqT, err := samePresence(o, t)
		if err != nil {
			return Result{}, err
		}
		oEqB, err := samePresence(o, b)
		if err != nil {
			return Result{}, err
		}
		tEqB, err := samePresence(t, b)
		if err != nil {
			return Result{}, err
		}
		switch {
		case oEqT:
			put(res.Merged, k, o)
		case oEqB:
			put(res.Merged, k, t)
		case tEqB:
			put(res.Merged, k, o)
		default:
			resolveClash(&res, k, o, t, p)
		}
	}
	sort.Strings(res.AutoResolved)
	return res, nil
}

// resolveClash applies the key's strategy when both sides changed it.
func resolveClash(res *Result, k string, o, t presence, p Policy) {
	strategy := StrategyConflict
	if p.StrategyFor != nil {
		strategy = p.StrategyFor(k)
	}
	switch strategy {
	case StrategyNewest:
		if p.Newer == SideTheirs {
			put(res.Merged, k, t)
		} else {
			put(res.Merged, k, o)
		}
		res.AutoResolved = append(res.AutoResolved, k)
		return
	case StrategyMax:
		os, oIsStr := o.val.(string)
		ts, tIsStr := t.val.(string)
		if o.ok && t.ok && oIsStr && tIsStr {
			if ts > os {
				put(res.Merged, k, t)
			} else {
				put(res.Merged, k, o)
			}
			res.AutoResolved = append(res.AutoResolved, k)
			return
		}
		// Non-string or deleted on one side: no meaningful max — fall
		// through to a reported conflict rather than guess.
	}
	res.Conflicts = append(res.Conflicts, Conflict{Key: k, Ours: o.val, Theirs: t.val})
	os, oIsStr := o.val.(string)
	ts, tIsStr := t.val.(string)
	if o.ok && t.ok && oIsStr && tIsStr {
		res.Merged[k] = MarkConflict(os, ts)
		return
	}
	put(res.Merged, k, o)
}

// MarkConflict renders a string conflict with git-style markers.
func MarkConflict(ours, theirs string) string {
	return MarkerOurs + "\n" + ours + "\n" + MarkerSplit + "\n" + theirs + "\n" + MarkerTheirs
}

// ContainsConflictMarkers reports whether s holds an unresolved
// MarkConflict block (or a raw git conflict hunk). Importers use it to
// refuse half-resolved files instead of storing marker text as knowledge.
func ContainsConflictMarkers(s string) bool {
	hasOurs, hasSplit, hasTheirs := false, false, false
	for line := range strings.SplitSeq(s, "\n") {
		switch {
		case strings.HasPrefix(line, "<<<<<<< "):
			hasOurs = true
		case line == MarkerSplit:
			hasSplit = true
		case strings.HasPrefix(line, ">>>>>>> "):
			hasTheirs = true
		}
	}
	return hasOurs && hasSplit && hasTheirs
}

func unionKeys(objs ...map[string]any) []string {
	seen := map[string]struct{}{}
	for _, o := range objs {
		for k := range o {
			seen[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func get(obj map[string]any, k string) presence {
	if obj == nil {
		return presence{}
	}
	v, ok := obj[k]
	return presence{ok: ok, val: v}
}

func put(obj map[string]any, k string, p presence) {
	if p.ok {
		obj[k] = p.val
	}
}

// samePresence compares two values structurally via their canonical JSON
// text, so 1 == 1 regardless of json.Number vs int64 and maps compare by
// content, not identity.
func samePresence(a, b presence) (bool, error) {
	if a.ok != b.ok {
		return false, nil
	}
	if !a.ok {
		return true, nil
	}
	as, err := canonjson.CompactValue(a.val)
	if err != nil {
		return false, fmt.Errorf("merge3: %w", err)
	}
	bs, err := canonjson.CompactValue(b.val)
	if err != nil {
		return false, fmt.Errorf("merge3: %w", err)
	}
	return as == bs, nil
}
