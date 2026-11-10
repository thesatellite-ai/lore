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
// It knows nothing about lore, tables or git. Values are the closed
// canonjson.Value type (no `any`); moved from saas/pkg/aicoder/merge3
// (PASS3 P3-PORT-LIVE), whose shell converts the old map[string]any API.
package merge3

import (
	"fmt"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/logic/canonjson"
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
	// StrategySet merges JSON arrays that are sets (`_meta.json` links,
	// labels) three ways: an element either side added is kept, an element
	// either side removed from base is dropped, nothing is ever reported as
	// a conflict. Ours' order comes first, then theirs' additions. A side
	// that deleted the key or holds a non-array falls back to a conflict.
	StrategySet
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
	// Ours and Theirs are the two values (null when that side deleted the
	// key). merge3 is schema-agnostic, so the caller narrows them using its
	// own schema.
	Ours   canonjson.Value
	Theirs canonjson.Value
}

// Result is the outcome of Merge.
//
// Merged always holds a complete object: for conflicting keys it holds a
// string with conflict markers (string values) or the ours value (others),
// so it can be written to disk for a human to finish.
type Result struct {
	Merged    canonjson.Object
	Conflicts []Conflict
	// AutoResolved lists keys a StrategyNewest / StrategyMax rule settled,
	// so callers can tell the user which silent decisions were taken.
	AutoResolved []string
}

// presence models "key absent" distinctly from "key present with null".
type presence struct {
	ok  bool
	val canonjson.Value
}

// Merge three-way merges base → ours and base → theirs.
//
// base may be nil (both sides added the object independently: an add/add
// merge). Rules per key, in order:
//   - unchanged on both sides, or changed identically → that value
//   - changed on one side only → that side's value (including deletion)
//   - changed on both sides differently → the key's Strategy
func Merge(base, ours, theirs canonjson.Object, p Policy) (Result, error) {
	keys := unionKeys(base, ours, theirs)
	res := Result{Merged: make(canonjson.Object, len(keys))}
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
			if err := resolveClash(&res, k, b, o, t, p); err != nil {
				return Result{}, err
			}
		}
	}
	sort.Strings(res.AutoResolved)
	return res, nil
}

// resolveClash applies the key's strategy when both sides changed it.
func resolveClash(res *Result, k string, b, o, t presence, p Policy) error {
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
		return nil
	case StrategyMax:
		os, oIsStr := o.val.AsString()
		ts, tIsStr := t.val.AsString()
		if o.ok && t.ok && oIsStr && tIsStr {
			if ts > os {
				put(res.Merged, k, t)
			} else {
				put(res.Merged, k, o)
			}
			res.AutoResolved = append(res.AutoResolved, k)
			return nil
		}
		// Non-string or deleted on one side: no meaningful max — fall
		// through to a reported conflict rather than guess.
	case StrategySet:
		if merged, ok, err := mergeSet(b, o, t); err != nil {
			return err
		} else if ok {
			res.Merged[k] = canonjson.Array(merged...)
			res.AutoResolved = append(res.AutoResolved, k)
			return nil
		}
	case StrategyConflict:
		// reported below
	}
	res.Conflicts = append(res.Conflicts, Conflict{Key: k, Ours: o.val, Theirs: t.val})
	os, oIsStr := o.val.AsString()
	ts, tIsStr := t.val.AsString()
	if o.ok && t.ok && oIsStr && tIsStr {
		res.Merged[k] = canonjson.String(MarkConflict(os, ts))
		return nil
	}
	put(res.Merged, k, o)
	return nil
}

// mergeSet is StrategySet's three-way set merge. ok is false when ours or
// theirs is absent or not an array (the clash is then reported). A base
// that is absent or not an array counts as the empty set.
func mergeSet(b, o, t presence) ([]canonjson.Value, bool, error) {
	ol, oOK := o.val.AsArray()
	tl, tOK := t.val.AsArray()
	if !o.ok || !t.ok || !oOK || !tOK {
		return nil, false, nil
	}
	bl, _ := b.val.AsArray()
	keyset := func(list []canonjson.Value) (map[string]bool, error) {
		set := make(map[string]bool, len(list))
		for _, v := range list {
			key, err := canonjson.CompactValue(v)
			if err != nil {
				return nil, fmt.Errorf("merge3: %w", err)
			}
			set[key] = true
		}
		return set, nil
	}
	inBase, err := keyset(bl)
	if err != nil {
		return nil, false, err
	}
	inOurs, err := keyset(ol)
	if err != nil {
		return nil, false, err
	}
	inTheirs, err := keyset(tl)
	if err != nil {
		return nil, false, err
	}
	out := make([]canonjson.Value, 0, len(ol)+len(tl))
	seen := map[string]bool{}
	add := func(v canonjson.Value, key string) {
		if !seen[key] {
			seen[key] = true
			out = append(out, v)
		}
	}
	for _, v := range ol {
		key, _ := canonjson.CompactValue(v) // encoded once already above
		// Keep what theirs still has, and what ours added.
		if inTheirs[key] || !inBase[key] {
			add(v, key)
		}
	}
	for _, v := range tl {
		key, _ := canonjson.CompactValue(v)
		// Theirs' additions; an element ours removed from base stays gone.
		if !inOurs[key] && !inBase[key] {
			add(v, key)
		}
	}
	return out, true, nil
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

func unionKeys(objs ...canonjson.Object) []string {
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

func get(obj canonjson.Object, k string) presence {
	if obj == nil {
		return presence{}
	}
	v, ok := obj[k]
	return presence{ok: ok, val: v}
}

func put(obj canonjson.Object, k string, p presence) {
	if p.ok {
		obj[k] = p.val
	}
}

// samePresence compares two values structurally via their canonical JSON
// text, so objects compare by content and a bad number is an error, as in
// v0.1.17.
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
