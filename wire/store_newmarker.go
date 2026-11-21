package wire

// store_newmarker.go — a store's first lore-store.yaml names its org in the
// same commit (devco:D-721 follow-up). An admin's `lore sync` names the org
// and its main store in every other store of the org (nameOrgInStores);
// before this, the first write into a brand-new empty store (store folder
// add, org link, a row write) made the marker without them, so the sync
// right after added a second commit to a repo that had just become a store.
// Every place that makes a new marker asks orgMarkForStore first, so the
// store's first commit already says what that sync would write.
//
//	withStoreDir (store folder add …) ─┐
//	org link (LinkWrite.NewMarker)     ├─▶ orgMarkForStore ─▶ org:, org_store:
//	backend first row write (WithMarkerOrg) ─┘   in the new marker

import (
	"cmp"
	"context"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/storeaddr"
)

// orgMarkForStore is the org name and main store url a new lore-store.yaml
// of the store at url names: set when an org whose admin this machine's
// person is on the roster (orgRosterAdmin, H-109) lists url as one of its
// stores other than the main one, exactly what an admin's sync would name
// in it later (nameOrgInStores). "", "" otherwise: a member's or a solo
// store's new marker names no org, as before.
func orgMarkForStore(ctx context.Context, home, url string) (org, main string) {
	return orgMarkFor(ctx, home, url, func(org string) bool { return orgRosterAdmin(ctx, home, org) })
}

// orgMarkFor is orgMarkForStore with the admin question passed in (admin
// reports whether this machine's person administers the org entry named).
func orgMarkFor(ctx context.Context, home, url string, admin func(org string) bool) (org, main string) {
	if home == "" {
		return "", ""
	}
	cfg, _, err := orgcfg.LoadConfig(home)
	if err != nil || cfg == nil {
		return "", ""
	}
	same := sameStoreAs(url)
	if same == nil {
		return "", ""
	}
	for i := range cfg.Orgs {
		o := &cfg.Orgs[i]
		m := o.OrgFolderStore()
		if m == nil || same(m.URL) || !admin(o.Name) {
			continue
		}
		for _, sc := range o.Stores {
			if sc.OrgFolder != "" || storeaddr.Same(sc.URL, m.URL) || !same(sc.URL) {
				continue
			}
			name := o.Name
			g := &orgcfg.GitLinks{Held: orgStoreHeld, Home: home, Config: cfg}
			if sg, serr := g.LocalSuggestion(ctx, m.URL, m.Branch); serr == nil {
				name = cmp.Or(sg.Doc.Name, o.Name) // the org's own name, not this machine's entry name
			}
			return name, m.URL
		}
	}
	return "", ""
}

// newStoreMarker is rowfs.NewMarker for the store at url, naming its org
// when orgMarkForStore knows it.
func newStoreMarker(ctx context.Context, home, url string, wall func() time.Time) (rowfs.Marker, error) {
	m, err := rowfs.NewMarker(wall())
	if err != nil {
		return rowfs.Marker{}, err
	}
	m.Org, m.OrgStore = orgMarkForStore(ctx, home, url)
	return m, nil
}

// markerOrgOption is the git backend's WithMarkerOrg for the lore home: a
// store clone's first row write names the org in the marker it adds.
func markerOrgOption(home string) git.Option {
	return git.WithMarkerOrg(func(ctx context.Context, remote string) (string, string) {
		return orgMarkForStore(ctx, home, remote)
	})
}
