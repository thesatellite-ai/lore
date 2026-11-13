package wire

// storeshown.go — one name per store in output (B-9). A repo attaches
// org stores under names that depend on the repo: in a client repo linked
// to folder bigco_hub of the org's client store, `acme` is that folder,
// while in the org's own repos `acme` is the team store. Lines that say
// which store something happened to (a push, a sync) name the store by
// its own name, the one `lore store list` gives it from any repo (the
// org's name for the store holding the org folder, else its short name),
// plus the folder, and add the repo's name for it when it differs:
//
//	lore: pushed store client-store/bigco_hub (here: acme): commit …
//	store client-store/bigco_hub (here: acme): sent 0, received 0, …
//
// `--to acme` keeps working: only what output prints changes.

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/store"
)

// storeOwnName is the store at url (folder folder) by its own name: the
// orgStoreLabel of url, then "/<folder>" for a folder below the root; ""
// when url is no configured org's store (it has no other name).
func storeOwnName(home, url, folder string) string {
	own := orgStoreLabel(home, url)
	if own == "" {
		return ""
	}
	if folder != "" && folder != rowfs.FolderRoot {
		own += targetSep + folder
	}
	return own
}

// storeShown is name as output prints it: own when it is the same name,
// "own (here: name)" when the repo calls it otherwise, name when the store
// has no own name.
func storeShown(name, own string) string {
	if own == "" || own == name || strings.TrimSuffix(name, targetSep+rowfs.FolderRoot) == own {
		return name // "agency/." is the store root, which own names already
	}
	return own + " (here: " + name + ")"
}

// shownStoreName is storeShown for an attached store this repo's org
// brings (by its attached name); any other name is printed as it is.
func (a *App) shownStoreName(ctx context.Context, name string) string {
	for _, s := range a.orgStoresHere(ctx) {
		if s.Name == store.Source(name) {
			return storeShown(name, storeOwnName(a.opts.Home, s.URL, s.Folder))
		}
	}
	return name
}

// OrgStoreName is the org store at url (or its normalised key) by its own
// name, as `lore store list` names it; url itself when it is no configured
// org's store. The org-file writes (org link, lore-org.yaml) print their
// push with it, like a row write's push (B-9).
func OrgStoreName(home, url string) string {
	if n := orgStoreLabel(home, url); n != "" {
		return n
	}
	// the org-file writes report a normalised key (host/owner/repo), which
	// is no url storeKey reads: match it against each store's key
	want, err := repourl.NormalizeStore(url)
	if err != nil {
		want = url
	}
	cfg, _, err := orgcfg.LoadConfig(home)
	if err != nil || cfg == nil {
		return url
	}
	for _, o := range cfg.Orgs {
		for _, sc := range o.Stores {
			if k, kerr := repourl.NormalizeStore(sc.URL); kerr == nil && k == want {
				if n := orgStoreLabel(home, sc.URL); n != "" {
					return n
				}
			}
		}
	}
	return url
}
