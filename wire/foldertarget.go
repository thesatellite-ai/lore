package wire

// foldertarget.go — which store a `<store>/<folder>` target means (owner
// report 2026-11-12: `lore store adopt <org>/<folder>` answered "<org> has
// no folder <folder>" for a folder of the org's second store, because the
// org's name opens the org's first store).
//
// Folder names are unique across an org's stores (store folder add
// refuses a name another store of the org holds; orgfolders.go), so the
// org and the folder name one place:
//
//	<store>/<folder> ─▶ the named store holds the folder ─▶ that store
//	                 ─▶ else the store belongs to an org and another store
//	                    of that org holds it ─▶ that store (by its url)
//	                 ─▶ else unchanged: the caller's own "has no folder"
//	                    answer, with folderMissDetail (the stores searched
//	                    and the folders found)
//	<folder> alone   ─▶ the one store of the org holding it, else
//	                    E_NOT_FOUND naming the stores searched and the
//	                    folders found
//
// Readers: store adopt, store folder remove, and every `--to` target
// (move, task/memory add --to …: resolveAlias for a bare folder,
// attachFolderTarget for <store>/<folder>).

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
)

// folderPlace is where a folder target resolved to: Name is what the
// store commands take for the store (the name as given, or the url of the
// org's store that holds the folder), Label how output names it.
type folderPlace struct {
	Name  string
	Label string
}

// folderStore resolves the store of a `<name>/<folderName>` target (name
// "" for a bare folder name). A miss with a name returns the name
// unchanged, so the caller answers with its own "has no
// folder" refusal plus folderMissDetail.
func (a *App) folderStore(ctx context.Context, verb, name, folderName string) (folderPlace, error) {
	if name == "" {
		return a.bareFolderStore(ctx, verb, folderName)
	}
	src, err := a.lookupStore(ctx, verb, name)
	if err != nil {
		return folderPlace{}, err
	}
	here := folderPlace{Name: name, Label: a.storeLabel(name, src)}
	if folderName == rowfs.FolderRoot {
		return here, nil
	}
	if f, ok := a.otherOrgStoreFolder(ctx, src, folderName); ok {
		return folderPlaceOf(f), nil
	}
	return here, nil
}

// otherOrgStoreFolder finds the folder in another store of src's org when
// src lacks it; ok false otherwise (src stands: it holds the folder, it
// is no org's store, or no store of its org holds the folder).
func (a *App) otherOrgStoreFolder(ctx context.Context, src attach.Store, folderName string) (orgFolder, bool) {
	if has, err := a.folderExists(ctx, src, folderName); err != nil || has {
		return orgFolder{}, false
	}
	sk, ok := storeKey(src.URL)
	if !ok {
		return orgFolder{}, false
	}
	org := a.storeOrgs()[sk]
	if org == "" {
		return orgFolder{}, false
	}
	f, ok := a.orgFolders(ctx).match(folderName, false)
	if !ok || f.Org != org {
		return orgFolder{}, false
	}
	if k, kok := storeKey(f.URL); kok && k == sk {
		return orgFolder{}, false
	}
	return f, true
}

// bareFolderTarget rewrites `--to <folder>` to `<org>/<folder>` when to
// names no store (no reserved name, stores.yaml entry or org store) and
// exactly that folder is in one of the org's stores. ok is false
// otherwise: the engine then answers "no store named … is attached".
func (a *App) bareFolderTarget(ctx context.Context, to string) (string, bool) {
	if slices.Contains(attach.Reserved(), to) {
		return "", false
	}
	if _, found, _, err := a.baseStore(ctx, to); err != nil || found {
		return "", false
	}
	a.mu.Lock()
	extra := slices.ContainsFunc(a.extra, func(s attach.Store) bool { return string(s.Name) == to })
	a.mu.Unlock()
	if extra {
		return "", false
	}
	f, ok := a.orgFolders(ctx).match(to, false)
	if !ok || f.Org == "" {
		return "", false
	}
	return f.Org + targetSep + f.Folder, true
}

// bareFolderStore is folderStore for a folder named alone.
func (a *App) bareFolderStore(ctx context.Context, verb, folderName string) (folderPlace, error) {
	scan := a.orgFolders(ctx)
	if f, ok := scan.match(folderName, false); ok {
		return folderPlaceOf(f), nil
	}
	if len(scan.Searched) == 0 && len(scan.Unread) == 0 {
		return folderPlace{}, errs.New(errs.InvalidInput, verb+": \""+folderName+"\" names no folder; give <store>/<folder> (a folder named alone is looked up in the org's stores, and this command sees no org)").
			WithHint("e.g. lore store adopt team/" + folderName + " (`lore store folder list` shows the folders)")
	}
	return folderPlace{}, errs.New(errs.NotFound, verb+": no store of the org has a folder "+folderName+scan.missDetail()+scan.foundDetail()).
		WithHint("name it as `lore store folder list` shows it, or <store>/<folder>; make it first with `lore store folder add --store <store> --folder " + folderName + "`")
}

// folderPlaceOf is the place of a folder found in the org's stores: named by
// its store's url (the name every store command takes for an org store).
func folderPlaceOf(f orgFolder) folderPlace {
	return folderPlace{Name: f.URL, Label: f.Label}
}

// folderMissDetail is what a "has no folder" refusal adds when the
// command sees an org: the org's stores searched and the folders found
// there ("" with no org).
func (a *App) folderMissDetail(ctx context.Context) string {
	scan := a.orgFolders(ctx)
	if len(scan.Searched) == 0 {
		return ""
	}
	return scan.missDetail() + scan.foundDetail()
}

// foundDetail names the folders the scan found, each with its store.
func (s orgFolderScan) foundDetail() string {
	if len(s.Folders) == 0 {
		return "; no folders found"
	}
	names := make([]string, 0, len(s.Folders))
	for _, f := range s.Folders {
		names = append(names, f.Folder+" ("+f.Label+")")
	}
	slices.Sort(names)
	return "; folders found: " + strings.Join(names, ", ")
}
