package orgcfg

// storeorgnote.go — the other stores of an org name the org and its main
// store in their lore-store.yaml (org:, org_store:), so a join by one of
// their urls finds the org (devco:D-721). Only the main store holds
// lore-org.yaml; before this a second store said nothing about its org and
// a join by its url was refused with "the admin has not sent it".
//
//	MarkOrgStore ─▶ clone + fetch ─▶ commitStore: marker at tip (none: the
//	                repo is no lore store yet, NotAStore, nothing written)
//	                ─▶ org:/org_store: already so: Existed ─▶ else set both
//	                ─▶ commit as the org's identity ─▶ push when Push (else
//	                the next `lore sync` sends it with the waiting org
//	                commits)
//	StoreNamesOrg ─▶ the local clone only, no network: does its marker
//	                already name the org (an admin's sync skips it then)

import (
	"context"
	"errors"
	"fmt"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// orgStoreNoteMessage is the commit subject of MarkOrgStore.
const orgStoreNoteMessage = "lore: store of org %s (main store %s)"

// OrgStoreMark asks MarkOrgStore to name an org in one of its stores.
type OrgStoreMark struct {
	// URL and Branch are the store to mark ("" branch: its default).
	URL, Branch string
	// Org is the org's name as its lore-org.yaml names it; MainStore the
	// url of the store holding that file (the url a join works with).
	Org, MainStore string
	// Push pushes the commit; without it the commit waits in the clone
	// for the next `lore sync` (as an org store add without --push).
	Push      bool
	Signature gitref.Signature
}

// OrgStoreMarkResult says what MarkOrgStore did.
type OrgStoreMarkResult struct {
	StoreURL string
	// Existed: the marker already named the org and main store.
	Existed bool
	// NotAStore: the repo holds no lore-store.yaml yet (an empty repo, or
	// one no write reached): nothing written; a later admin sync marks it.
	NotAStore bool
	// Pushed and Offline are commitStore's.
	Pushed, Offline bool
}

// MarkOrgStore writes org: and org_store: into the store's lore-store.yaml
// (devco:D-721). Every other key of the marker is kept. A marker lore
// cannot read is left as it is (Existed: there is nothing safe to write).
func (g *GitLinks) MarkOrgStore(ctx context.Context, m OrgStoreMark) (OrgStoreMarkResult, error) {
	norm, dir, err := g.openClone(ctx, m.URL, false)
	if err != nil {
		return OrgStoreMarkResult{}, err
	}
	branch := m.Branch
	if branch == "" {
		branch = g.DefaultBranch(ctx, norm)
	}
	res := OrgStoreMarkResult{StoreURL: norm}
	cr, err := commitStore(ctx, storeCommit{Dir: dir, Store: norm, Branch: branch, NoPush: !m.Push, PushPending: m.Push, Route: g.Route,
		Message: fmt.Sprintf(orgStoreNoteMessage, m.Org, m.MainStore), Signature: m.Signature,
		Change: func(ctx context.Context, repo *gitref.Repo, tip string) (map[string][]byte, error) {
			raw, found, rerr := repo.ReadFile(ctx, tip, storeMarkerFile)
			if rerr != nil || !found {
				return nil, rerr // no marker: commitStore refused it already (ErrNotAStore)
			}
			b, changed := markerNamingOrg(raw, m.Org, m.MainStore)
			if !changed {
				res.Existed = true
				return nil, nil
			}
			return map[string][]byte{storeMarkerFile: b}, nil
		}})
	if errors.Is(err, ErrNotAStore) || errors.Is(err, ErrStoreEmpty) || errors.Is(err, ErrNoStoreBranch) {
		res.NotAStore = true
		return res, nil
	}
	res.Pushed, res.Offline = cr.Pushed, cr.Offline
	return res, err
}

// StoreNamesOrg reports, from this machine's clone of the store only (no
// fetch), whether its lore-store.yaml at the branch's local tip already
// names org and main (named), and whether there is a clone with a marker
// to look at (cloned). A store this machine has no clone of is
// cloned=false: an admin's sync leaves it to the machines that hold it.
func (g *GitLinks) StoreNamesOrg(ctx context.Context, url, branch, org, main string) (named, cloned bool) {
	norm, err := repourl.NormalizeStore(url)
	if err != nil {
		return false, false
	}
	dir, _ := cloneDir(g.Home, norm)
	if branch == "" {
		branch = g.DefaultBranch(ctx, norm)
	}
	tip, ok := branchTip(ctx, dir, branch)
	if !ok {
		return false, false
	}
	raw, err := gitRun(ctx, "-C", dir, "cat-file", "blob", tip+":"+storeMarkerFile)
	if err != nil {
		return false, false // no marker: not a lore store yet, nothing to name
	}
	_, changed := markerNamingOrg([]byte(raw), org, main)
	return !changed, true
}

// markerNamingOrg is the marker raw with org: org and org_store: main;
// changed is false when it already says so, or when lore cannot read it
// (a marker of a newer lore or a broken one is never rewritten here).
func markerNamingOrg(raw []byte, org, main string) ([]byte, bool) {
	mk, err := rowfs.ParseMarker(raw)
	if err != nil || mk.ID.IsZero() {
		return nil, false // a v0.1.17 marker has no id: its migration rewrites it
	}
	if mk.Org == org && sameStoreURL(mk.OrgStore, main) {
		return nil, false
	}
	mk.Org, mk.OrgStore = org, main
	b, err := mk.Encode()
	if err != nil {
		return nil, false
	}
	return b, true
}
