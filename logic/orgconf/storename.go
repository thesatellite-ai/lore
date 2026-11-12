package orgconf

// storename.go — every org store's short name (owner request 2026-11-12,
// brief 3.6): `lore store list` used to name an org's second store by its
// whole url, and a `<store>/<folder>` target had to spell that url. Each
// store of an org now has a short name: stores[].name when set, else the
// repo name of its url without ".git" (git@host:acme/lore-x.git → lore-x).
//
//	Store.ShortName ─▶ Name, else RepoName(URL)
//	Org.validate    ─▶ checkShortNames: a name that is not a folder-shaped
//	                   word, a reserved word, or two stores of one org with
//	                   the same short name ─▶ refused naming both urls and
//	                   "set stores[].name in lore-org.yaml"
//
// The same check runs on a store's lore-org.yaml when lore reads it
// (adapters/orgcfg), so a team file with two `lore-x` stores is refused
// where it is read, not first in a teammate's config.yaml.

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/thesatellite-ai/lore/logic/repourl"
)

// StoreNameKey is the lore-org.yaml / config.yaml key of a store's short
// name, named in refusals so the person knows what to set.
const StoreNameKey = "stores[].name"

// OrgFileName is the team's shared org file, named in refusals.
const OrgFileName = "lore-org.yaml"

// ShortName is the store's short name: Name when set, else RepoName(URL)
// in lower case (repo names are case-insensitive on the common hosts, and
// a short name is typed like an org name).
func (s Store) ShortName() string {
	if n := strings.TrimSpace(s.Name); n != "" {
		return n
	}
	return strings.ToLower(RepoName(s.URL))
}

// RepoName is the last path segment of a store url without ".git":
// git@host:acme/lore-x.git, ssh://git@host/acme/lore-x and
// https://host/acme/lore-x.git all give "lore-x". "" when the url has no
// path segment.
func RepoName(raw string) string {
	s := strings.TrimSpace(raw)
	if q := strings.IndexAny(s, "?#"); q >= 0 {
		s = s[:q]
	}
	switch {
	case strings.Contains(s, "://"):
		if u, err := url.Parse(s); err == nil {
			s = u.Path
		}
	case strings.Contains(s, ":") && !strings.HasPrefix(s, "/"):
		_, s, _ = strings.Cut(s, ":")
	}
	s = strings.TrimSuffix(strings.TrimRight(s, "/"), ".git")
	base := path.Base(s)
	if base == "." || base == "/" {
		return ""
	}
	return base
}

// shortNameRe is a short name's shape: lower case, like an org name, plus '_'.
var shortNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// reservedStoreNames already mean something as a store argument or --to
// target, so no org store may be called by them.
var reservedStoreNames = map[string]string{
	"team":     "it is the --to target for the team store",
	TargetMe:   "it is the --to target for your personal store",
	TargetRepo: "it is the --to target for this repo",
}

// ValidateStoreShortName checks a store short name: one folder-shaped
// word (it is typed as `<store>/<folder>`), not reserved, not url-shaped.
func ValidateStoreShortName(name string) error {
	if err := ValidateFolderName(name); err != nil {
		return err
	}
	if !shortNameRe.MatchString(name) {
		return errors.New("use lowercase letters, digits, '.', '_' or '-', starting with a letter or digit")
	}
	if why, bad := reservedStoreNames[name]; bad {
		return fmt.Errorf("%q is reserved: %s", name, why)
	}
	if strings.Contains(name, "@") {
		return errors.New("a short name may not contain '@' (it would read as a url)")
	}
	return nil
}

// ShortNameStore is one store as checkShortNames sees it: its url as
// written, its short name, and whether the name was set (stores[].name).
type ShortNameStore struct {
	URL, Name string
	Set       bool
}

// CheckShortNames reports every bad or repeated short name among one
// org's stores, each prefixed with stores[i]. Two entries of one url (two
// branches of one store) share a name without a refusal: they are one
// store. file names the file the fix goes into (OrgFileName for a team
// file, config.yaml for a machine's copy).
func CheckShortNames(stores []ShortNameStore, file string) []error {
	var all []error
	first := map[string]int{}
	for i, s := range stores {
		w := fmt.Sprintf("stores[%d]", i)
		if s.Set {
			if err := ValidateStoreShortName(s.Name); err != nil {
				all = append(all, fmt.Errorf("%s.name %q: %w", w, s.Name, err))
				continue
			}
		}
		if s.Name == "" {
			continue // a url with no path is refused by the url check
		}
		j, dup := first[s.Name]
		if !dup {
			first[s.Name] = i
			continue
		}
		if sameStoreURL(stores[j].URL, s.URL) {
			continue
		}
		all = append(all, fmt.Errorf("%s: the short name %q is also stores[%d]'s (%s and %s); set %s in %s to tell them apart",
			w, s.Name, j, stores[j].URL, s.URL, StoreNameKey, file))
	}
	return all
}

// sameStoreURL reports whether two urls are one store spelled two ways.
func sameStoreURL(a, b string) bool {
	if a == b {
		return true
	}
	na, ea := normalizeStore(a)
	nb, eb := normalizeStore(b)
	return ea == nil && eb == nil && na == nb
}

// shortNameStores is o's stores as CheckShortNames takes them.
func (o *Org) shortNameStores() []ShortNameStore {
	out := make([]ShortNameStore, 0, len(o.Stores))
	for _, s := range o.Stores {
		out = append(out, ShortNameStore{URL: s.URL, Name: s.ShortName(), Set: strings.TrimSpace(s.Name) != ""})
	}
	return out
}

// StoreByShortName is the store of o whose short name is name, or nil.
func (o *Org) StoreByShortName(name string) *Store {
	for i := range o.Stores {
		if o.Stores[i].ShortName() == name {
			return &o.Stores[i]
		}
	}
	return nil
}

// normalizeStore is repourl.Normalize (host/path), the one store identity.
func normalizeStore(raw string) (string, error) { return repourl.NormalizeStore(raw) }
