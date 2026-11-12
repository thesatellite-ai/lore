package git

import (
	"context"
	"path"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// Adopting a git repository that has commits but no lore-store.yaml
// (OV59, `lore store init <name> <git url>`):
//
//	checkMarker: HEAD has no marker
//	  └─ hostedFiles(HEAD) → every tracked file is a hosting starter file
//	       or a store folder's _meta.json (a folder made in a brand-new
//	       repo before lore wrote the marker with the first write)
//	       yes → passes like an empty store (marker zero): store init, or
//	             the next write (Backend.addMissingMarker), writes the
//	             marker on top of the existing commits
//	       no  → E_NOT_A_STORE naming the other files (a lore store needs a
//	             repository of its own; init never mixes rows into code)

// maxNamedFiles caps how many foreign files the refusal names.
const maxNamedFiles = 5

// isStarterFile reports whether p (slash path from the repo root) is a
// git host's starter file at the top level (README, LICENSE, .gitignore
// …): the shared definition, rowfs.IsStarterFile. A repo holding only
// these is "empty" for lore (OV59 safest reading: adopt what a host's
// "new repository" button makes, refuse anything else). What adopt
// accepts (starter files, folders' _meta.json) is a subset of the files
// a store may hold (rowfs.StoreFileAllowed), pinned by
// TestAdoptAcceptsOnlyStoreFiles: a repo adopted here is never refused
// by the store check afterwards.
func isStarterFile(p string) bool { return rowfs.IsStarterFile(p) }

// isFolderMeta reports whether p is a store folder's _meta.json
// (<folder>/_meta.json, or <folder>/.lore/data/_meta.json from before the
// plain-files round; folder a name lore makes): lore folder
// metadata, which a store that lost its marker may hold and still be
// adopted (owner report 2026-11-11: `store folder add` on a brand-new
// repo pushed the folder without the marker).
func isFolderMeta(p string) bool {
	folder, name, ok := rowfs.LayoutStore.SplitFolderFile(p)
	if !ok { // pushed by a lore before the plain-files round
		folder, name, ok = rowfs.LayoutRepo.SplitFolderFile(p)
	}
	return ok && name == rowfs.MetaFileName && folder != rowfs.FolderRoot
}

// foreignFiles lists the files tracked at commit h that are not hosting
// starter files or store folders' _meta.json.
func (b *Backend) foreignFiles(ctx context.Context, h string) ([]string, error) {
	out, err := b.git.run(ctx, call{dir: b.work, args: []string{"ls-tree", "-r", "--name-only", "-z", h}})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "git: list the files of the store", err)
	}
	var foreign []string
	for _, p := range splitZ(out) {
		if c := path.Clean(p); !isStarterFile(c) && !isFolderMeta(c) {
			foreign = append(foreign, p)
		}
	}
	return foreign, nil
}

// notAStore is the refusal of a repository with commits, no marker and
// other content; it names up to maxNamedFiles of those files (PRINCIPLE
// EXACT ERRORS).
func notAStore(foreign []string) error {
	named := foreign
	more := ""
	if len(named) > maxNamedFiles {
		named, more = named[:maxNamedFiles], ", …"
	}
	return errs.Wrap(errs.NotAStore, "git: the repository has no "+rowfs.MarkerFile+" and holds other files ("+
		strings.Join(named, ", ")+more+"); a lore store needs a repository of its own", store.ErrNotAStore).
		WithHint("use a new, empty repository (a README, LICENSE or .gitignore is fine), or `lore migrate run` for a v0.1.17 store")
}
