package orgconf

// source.go — the org entry as a copy (SOURCE_OF_TRUTH row 9): the
// store's <org folder>/.lore/lore-org.yaml is the source of the shared
// fields (stores, own, repos, defaults); the entry in ~/.lore/config.yaml
// copies them and keeps its machine-local fields (the name label,
// identity, live and the rest). defaults is copied too (owner report,
// 0.1.18-dev): org import already copied it, but refresh left it behind,
// so `lore config show` kept the team's old defaults.client_repos while
// lore-org.yaml said otherwise. The copy carries its origin in `source:`:
//
//	source:
//	  store: sto_…      # the store whose lore-org.yaml was read
//	  version: <blob>   # git blob id of that lore-org.yaml
//
// StateOf compares the stamp with the file the store holds now.

import (
	"maps"
	"slices"
)

// OrgSource stamps an org entry's copied fields with their origin.
type OrgSource struct {
	// Store is the sto_ id of the store holding lore-org.yaml.
	Store string `yaml:"store"`
	// Version is the git blob id of the lore-org.yaml copied.
	Version string `yaml:"version"`
}

// Shared is the part of an org entry lore-org.yaml is the source of.
type Shared struct {
	Stores   []Store
	Own      []string
	Repos    []string
	Defaults OrgDefaults
}

// SharedOf is o's copied fields.
func SharedOf(o *Org) Shared {
	return Shared{Stores: o.Stores, Own: o.Own, Repos: o.Repos, Defaults: o.Defaults}
}

// Equal reports whether a and b hold the same fields (an empty list or
// map and none are equal; a store's empty branch is the default branch).
func (a Shared) Equal(b Shared) bool {
	return slices.EqualFunc(a.Stores, b.Stores, func(x, y Store) bool {
		return x.URL == y.URL && x.EffectiveBranch() == y.EffectiveBranch() && x.OrgFolder == y.OrgFolder
	}) && slices.Equal(a.Own, b.Own) && slices.Equal(a.Repos, b.Repos) &&
		maps.Equal(a.Defaults.ClientRepos, b.Defaults.ClientRepos)
}

// SourceState is how an entry's copy compares with the store's file.
type SourceState string

// Source states.
const (
	// SourceCurrent: the stamp names the file the store holds now.
	SourceCurrent SourceState = "current"
	// SourceStale: stamped from an older version of the file.
	SourceStale SourceState = "stale"
	// SourceUnstamped: no stamp (an entry from an older lore or written by
	// hand); its fields may or may not match the file.
	SourceUnstamped SourceState = "unstamped"
	// SourceOtherStore: stamped from another store's file.
	SourceOtherStore SourceState = "other_store"
)

// StateOf compares o's stamp with lore-org.yaml version in store storeID.
func StateOf(o *Org, storeID, version string) SourceState {
	switch {
	case o.Source == nil || o.Source.Version == "":
		return SourceUnstamped
	case o.Source.Store != storeID:
		return SourceOtherStore
	case o.Source.Version != version:
		return SourceStale
	}
	return SourceCurrent
}

// Refresh copies the shared fields into o and stamps it; it reports
// whether any copied field changed.
func Refresh(o *Org, sh Shared, src OrgSource) bool {
	changed := !SharedOf(o).Equal(sh)
	o.Stores, o.Own, o.Repos = slices.Clone(sh.Stores), slices.Clone(sh.Own), slices.Clone(sh.Repos)
	o.Defaults = OrgDefaults{ClientRepos: maps.Clone(sh.Defaults.ClientRepos)}
	o.Source = &src
	return changed
}
