package wire

// stalelink.go — a repo linked by its remote in a store the org entry no
// longer lists (L-7). Links are read only from the stores an org entry
// lists, so once a store is dropped from lore-org.yaml (and `lore org
// refresh` ran) its links are no longer seen: the repo reads as having no
// lore project, and a `--to <org>/<folder> --yes` could create that folder
// in another store of the org. The store's clone usually still sits on
// this machine; its folders' _meta.json links say where the repo was
// linked, offline:
//
//	clones under ~/.lore/stores ─▶ <clone>/<folder>/_meta.json links[]
//	   remote ∈ this repo's remotes, clone origin in no org entry ─▶ staleLink

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/model/errs"
)

// staleLink is a link of this repo's remote kept in a store clone whose
// store no org entry lists any more.
type staleLink struct {
	StoreURL string // the clone's origin, masked for messages
	origin   string // the clone's origin as configured
	Folder   string
	Remote   string
}

// folderLinks is the part of a store folder's _meta.json read here.
type folderLinks struct {
	Links []struct {
		Remote string `json:"remote"`
	} `json:"links"`
}

// findStaleLink looks for one (see the file comment); ok is false when
// none is found or nothing can be read.
func (a *App) findStaleLink(ctx context.Context) (staleLink, bool) {
	remotes, _ := orgcfg.Remotes(ctx, a.projectDir())
	if len(remotes) == 0 {
		return staleLink{}, false
	}
	want := map[string]bool{}
	for _, r := range remotes {
		want[r] = true
	}
	listed := a.storeOrgs() // store key -> org, for every store an org entry lists
	clones, err := gitbackend.ScanClones(a.opts.Home)
	if err != nil {
		return staleLink{}, false
	}
	for _, c := range clones {
		if c.State != gitbackend.CloneWhole || c.Origin == "" {
			continue
		}
		if k, ok := storeKey(c.Origin); ok && listed[k] != "" {
			continue // the entry still lists it: its links are read as usual
		}
		folders, ferr := rowfs.StoreFolders(c.Paths.Dir)
		if ferr != nil {
			continue
		}
		for _, f := range folders {
			b, rerr := os.ReadFile(filepath.Join(c.Paths.Dir, filepath.FromSlash(rowfs.LayoutStore.StorePath(f, rowfs.MetaFileName))))
			if rerr != nil {
				continue
			}
			var m folderLinks
			if json.Unmarshal(b, &m) != nil {
				continue
			}
			for _, l := range m.Links {
				if want[l.Remote] {
					return staleLink{StoreURL: repourl.Mask(c.Origin), origin: c.Origin, Folder: f, Remote: l.Remote}, true
				}
			}
		}
	}
	return staleLink{}, false
}

// staleLinkOrg is the org entry a stale link most likely belonged to: the
// one that resolves for this repo now (its own: patterns), "" when none.
func (a *App) staleLinkOrg(ctx context.Context) string {
	if a.opts.Org == nil {
		return ""
	}
	o, err := a.opts.Org(ctx)
	if err != nil || o == nil {
		return ""
	}
	return o.Name
}

// refuseStaleLinkFolder is L-7's refusal of `--to <to>` creating folder
// folderName in src: this repo's remote was linked to a folder of that name
// in a store the org entry no longer lists, so creating it here would put
// the row in a different store than the link named. nil when no such link
// is found. Both the repo's target resolution (targets.go) and a write in a
// git repo lore is not set up in (targetoutside.go, F-15) ask it before
// making the folder; the second once skipped it and made the folder in the
// org's first store.
func (a *App) refuseStaleLinkFolder(ctx context.Context, to, folderName string, src attach.Store) error {
	sl, ok := a.findStaleLink(ctx)
	if !ok || sl.Folder != folderName || sameStoreURL(src.URL, sl.origin) {
		return nil
	}
	return errs.New(errs.Refused, "--to "+to+" would create folder "+folderName+" in "+repourl.Mask(src.URL)+", but this repo's remote was linked to folder "+folderName+" of store "+sl.StoreURL+", which the org entry no longer lists").
		WithHint("re-add " + sl.StoreURL + " to the org's lore-org.yaml and run `lore org refresh`, then write again; to create a new folder here anyway, give it another name")
}
