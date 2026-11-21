package orgcfg

// storefloor.go — the lore version an org's stores need (devco:D-735).
// An admin declares it with `lore org config set --key min_lore_version`:
// every folder's _meta.json in each store gets min_lore_version raised to
// it, in one commit per store. Every lore since 0.1.18 already checks that
// floor before it writes a folder (formatgate.Check), so an older client
// turns read-only there (reads work, writes refuse with its upgrade line)
// without needing a new release, while a current lore writes as before.
// The floor is only ever raised: the _meta.json merge keeps the higher
// version on both sides (formatgate.Merge).
//
//	RaiseStoreFloor ─▶ clone of the store ─▶ every <folder>/_meta.json at tip
//	                ─▶ rowfs.RaiseFloorDoc ─▶ one commit (pushed only with Push)
//	StoreFloor      ─▶ the lowest floor of those files ("" when one has none)

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/formatgate"
)

const storeFloorMessage = "lore: org %s: stores need lore %s or later"

// StoreFloorEdit raises the floor of every folder of one store.
type StoreFloorEdit struct {
	// URL is the store, Branch its branch ("" = the store's default).
	URL, Branch string
	// Org names the org in the commit message.
	Org string
	// Floor is the version every folder's min_lore_version is raised to.
	Floor string
	// Push sends the commit to the store now.
	Push bool
	// Signature signs the commit.
	Signature gitref.Signature
	// OrgFile, for the store holding the org folder OrgFolder, also
	// records Floor as lore-org.yaml's min_lore_version in the same
	// commit: every member's org entry copies it from there, and each
	// folder, store and live ref a current lore creates for the org later
	// gets it (devco:D-735).
	OrgFile   bool
	OrgFolder string
}

// StoreFloorResult says what RaiseStoreFloor did.
type StoreFloorResult struct {
	StoreURL, Branch string
	// Folders counts the _meta.json files raised; 0 means every folder
	// already declared Floor or higher (nothing committed).
	Folders int
	// Pushed, Offline and NoPush as for an org file edit.
	Pushed, Offline, NoPush bool
	// Empty: the store has no commit yet, so no folder to raise.
	Empty bool
	// OrgFile: lore-org.yaml's min_lore_version was raised (OrgFile set).
	OrgFile bool
}

// RaiseStoreFloor commits min_lore_version e.Floor into every folder's
// _meta.json of the store e.URL (a folder with a higher floor keeps it).
func (g *GitLinks) RaiseStoreFloor(ctx context.Context, e StoreFloorEdit) (StoreFloorResult, error) {
	if !formatgate.ValidVersion(e.Floor) {
		return StoreFloorResult{}, fmt.Errorf("orgcfg: %q is not a release version (major.minor.patch)", e.Floor)
	}
	norm, dir, err := g.openClone(ctx, e.URL, false)
	if err != nil {
		return StoreFloorResult{}, err
	}
	branch := e.Branch
	if branch == "" {
		branch = g.DefaultBranch(ctx, norm)
	}
	res := StoreFloorResult{StoreURL: norm, Branch: branch, NoPush: !e.Push}
	cr, err := commitStore(ctx, storeCommit{Dir: dir, Store: norm, Branch: branch, NoPush: !e.Push, PushPending: e.Push, Route: g.Route,
		Message: fmt.Sprintf(storeFloorMessage, e.Org, e.Floor), Signature: e.Signature,
		Change: func(ctx context.Context, repo *gitref.Repo, tip string) (map[string][]byte, error) {
			metas, lerr := metaFilesAt(ctx, repo, tip)
			if lerr != nil {
				return nil, lerr
			}
			out := map[string][]byte{}
			for _, p := range metas {
				raw, found, rerr := repo.ReadFile(ctx, tip, p)
				if rerr != nil || !found {
					return nil, rerr
				}
				body, changed, ferr := rowfs.RaiseFloorDoc(raw, p, e.Floor)
				if ferr != nil {
					return nil, orgFileError(p, norm, ferr)
				}
				if changed {
					out[p] = body
				}
			}
			res.Folders = len(out)
			if e.OrgFile {
				p, body, changed, ferr := orgFileFloorChange(ctx, repo, dir, tip, e.OrgFolder, e.Floor)
				if ferr != nil {
					return nil, orgFileError(p, norm, ferr)
				}
				if changed {
					out[p], res.OrgFile = body, true
				}
			}
			return out, nil
		}})
	res.Pushed, res.Offline = cr.Pushed, cr.Offline
	if errors.Is(err, ErrStoreEmpty) {
		// a store listed before its first write has no folder yet
		res.Empty = true
		return res, nil
	}
	return res, err
}

// orgFileFloorChange is the org folder's lore-org.yaml at tip (the version
// an admin last wrote, as every org file edit reads it) with floor as its
// min_lore_version; changed is false when it already declares floor or
// higher. A store whose org folder holds no lore-org.yaml is refused.
func orgFileFloorChange(ctx context.Context, repo *gitref.Repo, dir, tip, folder, floor string) (p string, body []byte, changed bool, err error) {
	p = orgFilePath(folder)
	raw, found, err := repo.ReadFile(ctx, tip, p)
	if err != nil {
		return p, nil, false, err
	}
	if !found {
		return p, nil, false, ErrNoOrgFile
	}
	body, changed, err = raiseOrgFileFloor(acceptedOrgRaw(ctx, dir, tip, folder, raw), floor)
	return p, body, changed, err
}

// StoreFloor is the lowest min_lore_version the folders of the store
// declare in this machine's clone ("" when a folder declares none, or the
// store has no folder yet): the version every folder of it needs.
func (g *GitLinks) StoreFloor(ctx context.Context, url, branch string) (string, error) {
	norm, dir, err := g.openClone(ctx, url, false)
	if err != nil {
		return "", err
	}
	if branch == "" {
		branch = g.DefaultBranch(ctx, norm)
	}
	tip, ok := branchTip(ctx, dir, branch)
	if !ok {
		return "", nil
	}
	repo, err := gitref.Open(ctx, dir, nil)
	if err != nil {
		return "", err
	}
	metas, err := metaFilesAt(ctx, repo, tip)
	if err != nil || len(metas) == 0 {
		return "", err
	}
	low := ""
	for i, p := range metas {
		raw, found, rerr := repo.ReadFile(ctx, tip, p)
		if rerr != nil || !found {
			return "", rerr
		}
		f, ferr := rowfs.FloorOfDoc(raw, p)
		if ferr != nil {
			return "", ferr
		}
		if f == "" || !formatgate.ValidVersion(f) {
			return "", nil
		}
		if i == 0 || formatgate.VersionBelow(f, low) {
			low = f
		}
	}
	return low, nil
}

// metaFilesAt lists the folder _meta.json files of the store tree at tip
// (a folder's identity and format declaration, one per folder).
func metaFilesAt(ctx context.Context, repo *gitref.Repo, tip string) ([]string, error) {
	out, err := repo.Run(ctx, gitref.Invocation{Args: []string{"ls-tree", "-r", "-z", "--name-only", tip}})
	if err != nil {
		return nil, fmt.Errorf("orgcfg: list files at %s: %w", tip, err)
	}
	var metas []string
	for _, p := range bytes.Split(out, []byte{0}) {
		s := string(p)
		if path.Base(s) == rowfs.MetaFileName {
			metas = append(metas, s)
		}
	}
	return metas, nil
}
