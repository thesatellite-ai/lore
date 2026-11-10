package onboard

// leftovers.go — what config.yaml still holds about "me" after the setups
// that used it are gone (owner report 2026-11-08): a permanent delete
// without "also clear identity / personal.store" leaves the top-level
// identity: and personal.store behind, and nothing on Home said so. Home
// lists each leftover with a Clear button (setup.leftovers.clear).
//
// A value is a leftover only when something left it: a purge (a
// trash.purged tombstone carries its fingerprint) or the v0.1.17
// migration (identity.yaml.migrated names it). A value you set yourself
// (lore identity set, lore store init me) on a machine with no setup that
// uses it is yours, not a leftover: Home offered to clear it (2026-11-08).
//
// "Used" is decided from setups[] only (disabled entries count, they come
// back on enable; trashed ones do not, they match nothing):
//
//	identity:        a solo setup with no identity of its own, or with the
//	                 same identity.key (the first default solo setup copies
//	                 its identity there, setupapply 4.2). Member and admin
//	                 setups read orgs[].identity, never this one.
//	personal.store   a setup whose personal_store is empty or "inherit"
//	                 (inherit reaches it), or names the same url.

import (
	"slices"
	"strings"
)

// LeftoverKind names what a leftover is: a closed set (LeftoverKinds).
type LeftoverKind string

// Leftover kinds, each one key of ~/.lore/config.yaml.
const (
	// LeftoverIdentity is the top-level identity: block.
	LeftoverIdentity LeftoverKind = "identity"
	// LeftoverPersonalStore is personal.store.
	LeftoverPersonalStore LeftoverKind = "personal_store"
)

// leftoverStoreKey is personal.store's dotted key.
const leftoverStoreKey = "personal.store"

// LeftoverKinds lists every kind, identity first (Home's order).
func LeftoverKinds() []LeftoverKind { return []LeftoverKind{LeftoverIdentity, LeftoverPersonalStore} }

// EnumValues lists every kind (op.Enumerated).
func (LeftoverKind) EnumValues() []string { return enumStrings(LeftoverKinds()) }

// Valid reports a known kind.
func (k LeftoverKind) Valid() bool { return k == LeftoverIdentity || k == LeftoverPersonalStore }

// ConfigKey is the dotted key of config.yaml the kind names.
func (k LeftoverKind) ConfigKey() string {
	if k == LeftoverPersonalStore {
		return leftoverStoreKey
	}
	return string(LeftoverIdentity)
}

// Leftover is one key of config.yaml no setup uses.
type Leftover struct {
	Kind LeftoverKind `json:"kind"`
	// Key is the dotted config key ("identity", "personal.store").
	Key string `json:"key"`
	// Value is what it holds: the identity's key (with its name when it
	// has one), or the store url.
	Value string `json:"value"`
}

// Leftovers lists the top-level identity and personal.store when c holds
// them and no setup in c.Setups uses them; empty (never nil) when nothing
// is left.
func Leftovers(c Config) []Leftover {
	out := []Leftover{}
	if id, ok := identityLeftover(c); ok {
		out = append(out, id)
	}
	if st, ok := storeLeftover(c); ok {
		out = append(out, st)
	}
	return out
}

// LeftoverOf returns the leftover of kind k in c, false when c holds none
// (absent, or a setup uses it).
func LeftoverOf(c Config, k LeftoverKind) (Leftover, bool) {
	for _, l := range Leftovers(c) {
		if l.Kind == k {
			return l, true
		}
	}
	return Leftover{}, false
}

// identityLeftover: the top-level identity, when no solo setup uses it.
func identityLeftover(c Config) (Leftover, bool) {
	top := c.TopIdentity
	if top.IsZero() {
		return Leftover{}, false
	}
	key := strings.TrimSpace(top.Key)
	if !leftByPurge(c, func(p PurgedSetup) bool { return p.IdentitySHA != "" && p.IdentitySHA == IdentityFingerprint(key) }) &&
		!(c.MigratedIdentityKey != "" && strings.EqualFold(c.MigratedIdentityKey, key)) {
		return Leftover{}, false
	}
	for _, s := range c.Setups {
		if s.Kind != KindSolo {
			continue
		}
		own := strings.TrimSpace(s.Identity.Key)
		if own == "" || own == key {
			return Leftover{}, false
		}
	}
	val := key
	if n := strings.TrimSpace(top.Name); n != "" {
		if val == "" {
			val = n
		} else {
			val = n + " (" + key + ")"
		}
	}
	return Leftover{Kind: LeftoverIdentity, Key: LeftoverIdentity.ConfigKey(), Value: val}, true
}

// storeLeftover: personal.store, when no setup inherits or names it.
func storeLeftover(c Config) (Leftover, bool) {
	url := strings.TrimSpace(c.SharedStore)
	if url == "" || !leftByPurge(c, func(p PurgedSetup) bool { return p.StoreSHA != "" && p.StoreSHA == StoreFingerprint(url) }) {
		return Leftover{}, false
	}
	for _, s := range c.Setups {
		ps := strings.TrimSpace(s.PersonalStore)
		if ps == "" || ps == PersonalStoreInherit || ps == url {
			return Leftover{}, false
		}
	}
	return Leftover{Kind: LeftoverPersonalStore, Key: LeftoverPersonalStore.ConfigKey(), Value: url}, true
}

// leftByPurge reports whether a purge tombstone of c matches.
func leftByPurge(c Config, match func(PurgedSetup) bool) bool {
	return slices.ContainsFunc(c.Purged, match)
}
