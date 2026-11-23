package wire

// idfolder_attached.go — a row named by id that moved to a folder of a
// store .lore/stores.yaml attaches at another folder (devco:D-923, bug
// hunt H-181). The by-id search (idfolder.go) walked the org's stores and
// the personal store's folders; a store a repo attaches through its own
// stores.yaml, outside any org, was looked in only at the folder it
// attaches. `lore row move --id X --to notes/billing` from a repo that
// attaches notes at folder product, then `lore meta set --id X` from a
// second repo attaching the same folder, answered "X was deleted from
// notes". Ids are unique, so every folder of every attached store is
// searched, named "<store>/<folder>" as --store and --to take them:
//
//	stores.yaml: notes (url U, folder product)
//	  ─▶ U's folders on disk: product (attached), billing, …
//	  ─▶ scan entries "notes/billing", … (each folder once by its path)

import (
	"context"
	"strings"

	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// attachedStoreScan is every folder of the stores this repo's stores.yaml
// attaches, each folder named under the stores.yaml name's store part (a
// name "notes/billing" is the store notes at folder billing). An empty
// scan outside a repo or when stores.yaml lists no store; an unreadable
// store is in Unread, so a miss can say it was not looked in.
func (a *App) attachedStoreScan(ctx context.Context) orgFolderScan {
	empty := orgFolderScan{Unread: map[string]error{}}
	top, err := a.storeRepoTop(ctx)
	if err != nil || top == "" {
		return empty
	}
	cfg, err := readStoresFile(top)
	if err != nil {
		return empty // a broken stores.yaml is the read's own error to report
	}
	out := empty
	for _, s := range cfg.Stores {
		base, _, _ := strings.Cut(string(s.Name), targetSep)
		got := a.foldersOf(ctx, []opsstores.Entry{{Name: base, URL: s.URL, Branch: s.Branch, Source: opsstores.SourceStoresFile}})
		for i := range got.Folders {
			got.Folders[i].Org = base // "<store>/<folder>": the name --store takes here
		}
		out.Folders = append(out.Folders, got.Folders...)
		for k, v := range got.Unread {
			out.Unread[k] = v
		}
		out.Searched = append(out.Searched, got.Searched...)
		out.NoAccess = append(out.NoAccess, got.NoAccess...)
	}
	return out
}

// withAttachedStoreFolders is scan with the folders of attachedStoreScan
// after its own, each folder on disk once (an org store a repo also
// lists in stores.yaml keeps the org's name for it).
func (a *App) withAttachedStoreFolders(ctx context.Context, scan orgFolderScan) orgFolderScan {
	more := a.attachedStoreScan(ctx)
	seen := map[string]bool{}
	for _, f := range scan.Folders {
		seen[f.Dir+"\x00"+f.Folder] = true
	}
	for _, f := range more.Folders {
		if k := f.Dir + "\x00" + f.Folder; !seen[k] {
			seen[k] = true
			scan.Folders = append(scan.Folders, f)
		}
	}
	if scan.Unread == nil {
		scan.Unread = map[string]error{}
	}
	for k, v := range more.Unread {
		scan.Unread[k] = v
	}
	scan.Searched = append(scan.Searched, more.Searched...)
	scan.NoAccess = append(scan.NoAccess, more.NoAccess...)
	return scan
}
