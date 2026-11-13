package engine

import (
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/store"
)

// ReadStores lists the attached store names a read ACROSS stores walks, in
// precedence order: Stores() minus every name whose Location an earlier
// name (stores.yaml order) already covers.
//
// Why: two stores.yaml entries can attach one place, e.g. `stag` as
// `<url>?branch=staging` and `stag2` as `<url> --branch staging` (issue #1).
// Both names read the same row files, so a list or search that walks
// Stores() shows every row once per name and labels a row written
// `--to stag2` as `[stag]` first. A cross-store read walks this list
// instead, so each row is read once, through the FIRST name: the same rule
// a lookup by id (locateRaw), trash, purge and move already follow
// (Attached.Location). Writes and reads that NAME a store (`--to stag2`,
// `--store stag2`) keep working through either name: only the walk over
// every store skips the later name. A name with no Location (the repo
// store, a test backend) is a place of its own and always kept.
func (e *Engine) ReadStores() []store.Source {
	return onePerLocation(e.order, func(name store.Source) string { return e.stores[name].Location })
}

// onePerLocation keeps the first name of each Location in names, in order;
// a name whose Location is "" is always kept. It is the one decision behind
// ReadStores (kept apart so a test pins it without opening stores).
func onePerLocation(names []store.Source, locationOf func(store.Source) string) []store.Source {
	out := make([]store.Source, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		if loc := locationOf(name); loc != "" {
			if seen[loc] {
				continue
			}
			seen[loc] = true
		}
		out = append(out, name)
	}
	return out
}

// ReadStorer is the optional upgrade an ops Env implements to give the
// stores a cross-store read walks (Engine.ReadStores); wire's App and the
// engine both do.
type ReadStorer interface {
	ReadStores() []store.Source
}

// StoreLister is anything that lists the attached stores (every ops Env).
type StoreLister interface {
	Stores() []store.Source
}

// ReadStoresOf is the store list a read across every store of l walks:
// l.ReadStores() when l implements ReadStorer, else l.Stores() (a test Env
// or a surface with no shared places, where every name is its own place).
// Every cross-store list and search calls this instead of Stores(), so a
// place attached under two names is read once (see Engine.ReadStores).
func ReadStoresOf(l StoreLister) []store.Source {
	if r, ok := l.(ReadStorer); ok {
		return r.ReadStores()
	}
	return slices.Clone(l.Stores())
}

// storeFolderSep joins a store's name and one of its folders in a read
// store's name ("acme/org", "me/acme").
const storeFolderSep = "/"

// NamedSources is the read stores a `--store <name>` filter means: name
// itself and every `<name>/<folder>` attached beside it. An org store's
// rows live in its folders (acme/org for the company, acme/<project> per
// project; E-14: `--store acme` said "(no memories)" while acme/org held
// five), and lore 0.1.17's per-org personal notes keep their org folder,
// attached as me/<org> (D5), so `--store me` lists every personal note
// this repo reads. A name that already names a folder (acme/org) is that
// folder alone.
func NamedSources(l StoreLister, name store.Source) []store.Source {
	out := []store.Source{name}
	if strings.Contains(string(name), storeFolderSep) {
		return out
	}
	for _, s := range ReadStoresOf(l) {
		if s != name && strings.HasPrefix(string(s), string(name)+storeFolderSep) {
			out = append(out, s)
		}
	}
	return out
}
