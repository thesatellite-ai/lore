package wire

// idfolder_personal.go — a row named by id that lives in a folder of the
// personal store (devco:D-755). `lore memory add --to me/<folder>` keeps a
// note in that folder, and a repo attaches only `me` (the store root) and
// the folders its own setup names, so every write by id (task done, edit,
// delete, row move, row edit) on such a note answered E_NOT_FOUND unless
// the repo happened to attach that very folder. The org folders were
// already searched for an id (idfolder.go in a repo, rowLookupOutside from
// a plain folder or the home folder); the personal store's folders were
// not. Ids are unique, so the personal store's folders are now searched
// the same way, after the org's:
//
//	op with <id> ─▶ attached stores hold it ─▶ nothing to do
//	             └▶ org store folders ─▶ personal store folders (me/<folder>)
//	                  ─▶ found: attach "me/<folder>" (unrecorded) + a note
//
// Only the personal store `me` names in this folder (effectivePersonalURL)
// is read: "me/<folder>" must resolve to the store the row is in.

import (
	"context"
)

// personalIDScan is every folder of the personal store `me` names here
// (its root aside: `me` attaches it in every repo), as folders named
// "me/<folder>" (personalFolderScan, the one list of them). An empty scan
// when no personal store is set up for this folder (personal_store: none);
// a clone it could not read is in Unread, so a miss can say it was not
// looked in.
func (a *App) personalIDScan(ctx context.Context) orgFolderScan {
	mc, err := readMachineConfig(a.opts.Home)
	if err != nil {
		return orgFolderScan{Unread: map[string]error{}} // a broken config is doctor's to report
	}
	return a.personalFolderScan(ctx, effectivePersonalURL(ctx, a.opts.Home, a.projectDir(), mc), "")
}

// withPersonalFolders is scan with the personal store's folders after its
// own (personalIDScan), each folder on disk once: in the personal store's
// own clone the clone scan already holds them.
func (a *App) withPersonalFolders(ctx context.Context, scan orgFolderScan) orgFolderScan {
	me := a.personalIDScan(ctx)
	seen := map[string]bool{}
	for _, f := range scan.Folders {
		seen[f.Dir+"\x00"+f.Folder] = true
	}
	for _, f := range me.Folders {
		if !seen[f.Dir+"\x00"+f.Folder] {
			scan.Folders = append(scan.Folders, f)
		}
	}
	if scan.Unread == nil {
		scan.Unread = map[string]error{}
	}
	for k, v := range me.Unread {
		scan.Unread[k] = v
	}
	scan.Searched = append(scan.Searched, me.Searched...)
	scan.NoAccess = append(scan.NoAccess, me.NoAccess...)
	return scan
}
