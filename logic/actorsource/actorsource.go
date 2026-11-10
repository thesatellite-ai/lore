// Package actorsource decides, for one actor held in several stores, which
// value is the truth and what state every other copy is in (the
// SOURCE_OF_TRUTH actor rules, owner 2026-11-09):
//
//	solo  the setup identity (the identity chain's answer) is the source
//	team  the org roster store's actors row is the source
//
// Every other copy carries origin_store (the roster store's sto_ id, or
// setup:<name> for solo) and origin_version (the roster row's HLC, or a
// short hash of the identity fields copied). A copy whose origin_version
// differs from the source's is stale and is refreshed on the next write
// lore makes to its store.
//
//	Source + []Copy ──▶ StateOf (source, current, stale, no origin stamp,
//	                     source unreachable)
//	                ──▶ Shown (the source's value; else the newest copy,
//	                     marked)
//	                ──▶ NeedsRefresh / Patch (what a refresh writes)
//
// Layer L1: imports model only; pure, no I/O.
package actorsource

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/thesatellite-ai/lore/model/hlc"
)

// The actors fields a copy carries from its source (schema.Builtin actors).
const (
	FieldDisplayName = "display_name"
	FieldKind        = "kind"
	FieldStableKey   = "stable_key"
	FieldHandle      = "handle"
	// FieldKeyAliases holds a person's earlier keys after an email change
	// (comma separated, oldest first; never in a projected copy).
	FieldKeyAliases    = "key_aliases"
	FieldOriginStore   = "origin_store"
	FieldOriginVersion = "origin_version"
)

// Identity origins: the origin_store of a copy whose source is an
// identity block rather than a roster row (IsIdentityOrigin).
const (
	// OriginSetupPrefix starts a solo setup's: setup:<name>.
	OriginSetupPrefix = "setup:"
	// OriginOrgPrefix starts an org entry's identity (a person not on the
	// roster yet): org:<org>.
	OriginOrgPrefix = "org:"
	// OriginIdentityPrefix starts any other identity step's:
	// identity:top, identity:git, identity:other.
	OriginIdentityPrefix = "identity:"
)

// versionHexLen is the length of a solo origin_version: 12 hex digits of
// a SHA-256 (48 bits; it only has to change when the identity changes).
const versionHexLen = 12

// Values are the identity fields a source holds and a copy repeats.
type Values struct {
	Name      string
	Kind      string
	StableKey string
	Handle    string
	// Aliases are the earlier keys after an email change, joined by
	// AliasSep, oldest first ("" when none).
	Aliases string
}

// AliasSep joins Values.Aliases (and the key_aliases field).
const AliasSep = ", "

// JoinAliases is keys as a Values.Aliases text (empty keys left out).
func JoinAliases(keys []string) string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return strings.Join(out, AliasSep)
}

// SplitAliases is the keys of a Values.Aliases text.
func SplitAliases(s string) []string {
	var out []string
	for _, k := range strings.Split(s, strings.TrimSpace(AliasSep)) {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// Source is where an actor's truth lives, as this machine sees it.
type Source struct {
	// Known is true when a source exists for the actor (a roster row, or
	// the caller's own identity). An actor with no known source (another
	// person in a solo store, an agent seen only through its rows) has
	// copies only.
	Known bool
	// Reachable is true when Values and Version were read now. A known
	// source that could not be read (the roster store is not attached or
	// fails) is not reachable.
	Reachable bool
	// Origin is what a copy's origin_store names: the roster store's sto_
	// id, or setup:<name>.
	Origin string
	// Where names the source in messages: "acme/org", "setup solo1".
	Where string
	// Values is the source's value (only when Reachable).
	Values Values
	// Version is the source's version: the roster row's HLC text, or
	// IdentityVersion of the identity (only when Reachable).
	Version string
}

// Copy is one store's actors row for the actor.
type Copy struct {
	// Store is the attached store holding it.
	Store string
	// Values are the copy's own fields (StableKey and Handle are empty in
	// a client or F18 copy).
	Values Values
	// OriginStore and OriginVersion are its stamp ("" when unstamped).
	OriginStore   string
	OriginVersion string
	// HLC is the copy's last edit (the newest copy wins when the source
	// cannot be read).
	HLC hlc.HLC
	// IsSource is true for the source row itself (the roster store's row).
	IsSource bool
	// Projected is true for a copy lore writes as the F18 projection (id,
	// display name and kind only: a client store, or the repo store of a
	// repo with no org). It never carries the origin stamp,
	// since a store id or an HLC naming the agency's writer must not reach
	// a client's committed files (the leak guard, logic/leakcheck); its
	// state is judged by its values against the source's instead.
	Projected bool
}

// State is a copy's state against its source.
type State string

// States.
const (
	StateSource      State = "source"
	StateCurrent     State = "current"
	StateStale       State = "stale"
	StateUnstamped   State = "no origin stamp"
	StateUnreachable State = "source unreachable"
)

// IdentityVersion is a solo origin_version: a short hash of the identity
// fields a copy repeats, so any change to name, kind, key or handle gives
// a new version and an unchanged identity always gives the same one.
func IdentityVersion(v Values) string {
	parts := []string{v.Name, v.Kind, v.StableKey, v.Handle}
	if v.Aliases != "" {
		parts = append(parts, v.Aliases) // only when set: an identity with no aliases keeps its version
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:versionHexLen]
}

// StateOf is c's state against src. A copy is current only when its stamp
// names the source's version; an unstamped copy is "no origin stamp"
// whatever its values; with the source unreadable a stamped copy cannot be
// judged.
func StateOf(src Source, c Copy) State {
	switch {
	case c.IsSource:
		return StateSource
	case c.Projected && !src.Reachable:
		return StateUnreachable
	case c.Projected && len(valuePatch(src, c)) == 0:
		return StateCurrent
	case c.Projected:
		return StateStale
	case c.OriginVersion == "":
		return StateUnstamped
	case !src.Reachable:
		return StateUnreachable
	case c.OriginVersion == src.Version && c.OriginStore == src.Origin:
		return StateCurrent
	}
	return StateStale
}

// NeedsRefresh reports whether a write to c's store should rewrite c from
// src: the source was read, c is not the source, and its stamp is not the
// source's version (stale or unstamped).
func NeedsRefresh(src Source, c Copy) bool {
	if !src.Reachable || c.IsSource {
		return false
	}
	s := StateOf(src, c)
	return s == StateStale || s == StateUnstamped
}

// Patch is the field update that makes c current: display_name and kind
// from the source when they differ, stable_key and handle only when the
// copy already holds that field (a client or F18 copy never gains an
// email), and the origin stamp. Empty source values never clear a field.
func Patch(src Source, c Copy) map[string]string {
	out := valuePatch(src, c)
	if c.Projected {
		return out // a client copy never carries the stamp (Copy.Projected)
	}
	setField(out, FieldOriginStore, src.Origin, c.OriginStore, false)
	setField(out, FieldOriginVersion, src.Version, c.OriginVersion, false)
	return out
}

// valuePatch is Patch without the origin stamp: the copied values that
// differ from the source's.
func valuePatch(src Source, c Copy) map[string]string {
	out := map[string]string{}
	setField(out, FieldDisplayName, src.Values.Name, c.Values.Name, false)
	setField(out, FieldKind, src.Values.Kind, c.Values.Kind, false)
	setField(out, FieldStableKey, src.Values.StableKey, c.Values.StableKey, true)
	setField(out, FieldHandle, src.Values.Handle, c.Values.Handle, true)
	if !c.Projected {
		// a full copy gains the old keys with the new stable_key; the F18
		// projection never carries an email (old or new)
		setField(out, FieldKeyAliases, src.Values.Aliases, c.Values.Aliases, false)
	}
	return out
}

// setField puts want into out[field] when it is a change: not empty, not
// what the copy has, and (onlyIfHeld) only for a field the copy holds.
func setField(out map[string]string, field, want, have string, onlyIfHeld bool) {
	if want == "" || want == have || (onlyIfHeld && have == "") {
		return
	}
	out[field] = want
}

// Counts is a per-actor tally of copy states (the stores column of actor
// list). The source row itself is not counted.
type Counts struct {
	Current     int `json:"current"`
	Stale       int `json:"stale"`
	Unstamped   int `json:"unstamped"`
	Unreachable int `json:"unreachable"`
}

// CountsOf tallies copies against src.
func CountsOf(src Source, copies []Copy) Counts {
	var n Counts
	for _, c := range copies {
		switch StateOf(src, c) {
		case StateCurrent:
			n.Current++
		case StateStale:
			n.Stale++
		case StateUnstamped:
			n.Unstamped++
		case StateUnreachable:
			n.Unreachable++
		case StateSource:
		}
	}
	return n
}

// Shown is what a merged read shows for an actor: the source's values when
// it was read (Mark ""); otherwise the newest copy (by HLC, the first on a
// tie), with Mark naming the copy. For a known but unreadable source the
// mark is always set; for an actor with no known source it is set only when
// the copies disagree on the name (an agent seen in two stores with one
// name needs no warning).
type Shown struct {
	Values Values
	// From is the store whose copy is shown ("" for the source).
	From string
	Mark string
}

// ShownOf picks the value a merged read shows (see Shown).
func ShownOf(src Source, copies []Copy) Shown {
	if src.Reachable {
		return Shown{Values: src.Values}
	}
	if len(copies) == 0 {
		return Shown{}
	}
	best := copies[0]
	differ := false
	for _, c := range copies[1:] {
		if c.Values.Name != best.Values.Name {
			differ = true
		}
		if c.HLC.Compare(best.HLC) > 0 {
			best = c
		}
	}
	s := Shown{Values: best.Values, From: best.Store}
	if src.Known || differ {
		s.Mark = "(copy from " + best.Store + ", source unreachable)"
	}
	return s
}
