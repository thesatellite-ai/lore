// Package storeaddr decides which address of a store is the one writes go
// to when this machine names one store at more than one address
// (SOURCE_OF_TRUTH row 7). Layer L1, pure: no files, no git.
//
// A store's identity is the id in its marker (lore-store.yaml `id:`); its
// address is where it is reached. Every place that names a store (the
// repo's .lore/stores.yaml, the org entry's stores in ~/.lore/config.yaml,
// personal.store, a setup's personal_store, and this machine's address
// record written by `lore store set-url`) is one Entry. Entries of one id
// at two addresses are a Split:
//
//	Splits(entries) ─▶ group by store id ─▶ more than one address?
//	                ─▶ primary: the address record, else the address the
//	                   machine config names (an org or personal entry),
//	                   else the first entry in the order given
//	                ─▶ every other address is refused for writes
//	                   (Refusal: E_REFUSED naming both and the fix)
package storeaddr

import (
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
)

// Place says which file holds one copy of a store's address.
type Place string

// Places, in the words the doctor and store list show.
const (
	// PlaceRecord is this machine's address record (set by store set-url).
	PlaceRecord Place = "address record"
	// PlaceStoresFile is the repo's .lore/stores.yaml.
	PlaceStoresFile Place = ".lore/stores.yaml"
	// PlaceOrg is orgs[<org>].stores in ~/.lore/config.yaml.
	PlaceOrg Place = "orgs[].stores"
	// PlacePersonal is personal.store in ~/.lore/config.yaml.
	PlacePersonal Place = "personal.store"
	// PlaceSetup is setups[<setup>].personal_store in ~/.lore/config.yaml.
	PlaceSetup Place = "setups[].personal_store"
)

// inConfig reports whether p is a key of the machine's config.yaml (the
// address book every repo on this machine shares).
func (p Place) inConfig() bool {
	return p == PlaceOrg || p == PlacePersonal || p == PlaceSetup
}

// Entry is one place naming a store.
type Entry struct {
	Place Place
	// Name is the entry's name in its place: the stores.yaml name, the org
	// entry, the setup ("" for personal.store and the record).
	Name string
	// URL is the address as written.
	URL string
	// StoreID is the sto_ id read from the store's marker at URL on this
	// machine; "" when unknown (no local copy), and then the entry is never
	// part of a Split.
	StoreID string
}

// Key is the comparison form of a store address: "host/path" for a remote
// (repourl.Normalize: scheme, login user and .git dropped), the cleaned
// path for a file:// url or a local path; a query (?branch=, ?folder=) is
// not part of the address.
func Key(raw string) string {
	s := strings.TrimSpace(raw)
	if n, err := repourl.Normalize(s); err == nil {
		return n
	}
	if u, err := url.Parse(s); err == nil && u.Scheme == "file" {
		fileuri.FoldDriveHost(u) // file://C:/x and file:///C:/x are one place
		return "file://" + path.Clean(u.Path)
	}
	if strings.HasPrefix(s, "/") {
		return "file://" + path.Clean(s)
	}
	return s
}

// Same reports whether a and b are one address.
func Same(a, b string) bool { return Key(a) == Key(b) }

// Split is one store id reached at more than one address.
type Split struct {
	StoreID string
	// Primary is the address writes go to; Others are every other
	// address, in the order first seen.
	Primary string
	Others  []string
	// Entries are every entry naming the store, in the order given.
	Entries []Entry
}

// Splits returns every store id the entries name at more than one
// address, in the order the ids are first seen. Entries with no StoreID
// are skipped.
func Splits(entries []Entry) []Split {
	var order []string
	by := map[string][]Entry{}
	for _, e := range entries {
		if e.StoreID == "" || strings.TrimSpace(e.URL) == "" {
			continue
		}
		if _, ok := by[e.StoreID]; !ok {
			order = append(order, e.StoreID)
		}
		by[e.StoreID] = append(by[e.StoreID], e)
	}
	var out []Split
	for _, id := range order {
		es := by[id]
		var keys, urls []string
		for _, e := range es {
			if k := Key(e.URL); !slices.Contains(keys, k) {
				keys = append(keys, k)
				urls = append(urls, e.URL)
			}
		}
		if len(keys) < 2 {
			continue
		}
		p := primary(es)
		s := Split{StoreID: id, Primary: p, Entries: es}
		for _, u := range urls {
			if !Same(u, p) {
				s.Others = append(s.Others, u)
			}
		}
		out = append(out, s)
	}
	return out
}

// primary picks the address writes go to (see the package comment).
func primary(es []Entry) string {
	for _, e := range es {
		if e.Place == PlaceRecord {
			return e.URL
		}
	}
	for _, e := range es {
		if e.Place.inConfig() {
			return e.URL
		}
	}
	return es[0].URL
}

// Addresses is the primary first, then the others.
func (s Split) Addresses() []string { return append([]string{s.Primary}, s.Others...) }

// Line is the doctor's warning: "store sto_… is reached at two addresses:
// A, B" ("three", … for more).
func (s Split) Line() string {
	return "store " + s.StoreID + " is reached at " + count(len(s.Others)+1) + " addresses: " + strings.Join(s.Addresses(), ", ")
}

// Fix is the command that makes every entry name the primary address.
func (s Split) Fix() string { return "lore store set-url " + s.StoreID + " " + s.Primary }

// Refusal is the write refusal for url: nil when url is the primary
// address (or not one of the split's addresses), else E_REFUSED naming
// both addresses and `lore store set-url`.
func (s Split) Refusal(name, raw string) error {
	if Same(raw, s.Primary) || !slices.ContainsFunc(s.Others, func(o string) bool { return Same(o, raw) }) {
		return nil
	}
	return errs.New(errs.Refused, "store "+name+" ("+s.StoreID+") is reached at two addresses on this machine: "+raw+" here and "+s.Primary+" elsewhere; lore writes only to "+s.Primary+" until one address is kept").
		WithHint("keep one address: `lore store set-url " + s.StoreID + " " + s.Primary + "` (or name " + raw + " to move the store there); `lore doctor` lists every place naming it")
}

// For returns the split of store id, if any.
func For(splits []Split, id string) (Split, bool) {
	i := slices.IndexFunc(splits, func(s Split) bool { return s.StoreID == id })
	if i < 0 {
		return Split{}, false
	}
	return splits[i], true
}

// count spells small numbers as words for the doctor line.
func count(n int) string {
	switch n {
	case 2:
		return "two"
	case 3:
		return "three"
	case 4:
		return "four"
	}
	return strconv.Itoa(n)
}
