package wire

// doctor_removedfolder.go — the removed_store_folder check of `lore
// doctor`: a folder this repo's .lore/stores.yaml attaches that its store
// no longer has, because `lore store folder remove` (or a hand edit)
// deleted it. The repo then reads nothing from that folder, and its next
// write there would make the folder again under the same name. Only a
// folder the store's git history shows was deleted is reported: a folder
// attached before its first write has no _meta.json yet and is fine.

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/store"
)

// DoctorCheckRemovedFolder is the check's JSON name.
const DoctorCheckRemovedFolder DoctorCheck = "removed_store_folder"

// removedFormat is the git log format of the commit that deleted a
// folder's _meta.json: short hash, author, date (YYYY-MM-DD).
const removedFormat = "%h%x09%an%x09%as"

// DoctorRemovedFolders reports each stores.yaml folder entry whose folder
// its store removed. It only reads (the store clone may fetch).
func (a *App) DoctorRemovedFolders(ctx context.Context) ([]DoctorFinding, error) {
	top, err := a.storeRepoTop(ctx)
	if err != nil {
		return nil, err
	}
	if top == "" {
		return nil, nil // outside a repo there is no stores.yaml to check
	}
	cfg, err := readStoresFile(top)
	if err != nil {
		return nil, err
	}
	var out []DoctorFinding
	for _, s := range cfg.Stores {
		f := rootFolder(s.Folder)
		if f == "" {
			continue
		}
		if fd, ok := a.removedFolderFinding(ctx, s, f); ok {
			out = append(out, fd)
		}
	}
	return out, nil
}

// removedFolderFinding is the finding for the stores.yaml entry s that
// attaches folder f; ok is false when the store has f, or its history
// does not show f deleted (never made yet, a folder store, unreadable).
func (a *App) removedFolderFinding(ctx context.Context, s attach.Store, f string) (DoctorFinding, bool) {
	dir, err := a.storeDir(ctx, attach.Store{URL: s.URL, Branch: s.Branch})
	if err != nil {
		return DoctorFinding{}, false
	}
	meta := rowfs.LayoutStore.StorePath(f, rowfs.MetaFileName)
	if folderMetaAt(dir, f) {
		return DoctorFinding{}, false
	}
	log, err := gitRun{dir: dir, args: []string{"log", "-1", "--diff-filter=D", "--format=" + removedFormat, "--", meta}}.output(ctx)
	line := strings.TrimSpace(string(log))
	if err != nil || line == "" {
		return DoctorFinding{}, false
	}
	sha, rest, _ := strings.Cut(line, "\t")
	by, day, _ := strings.Cut(rest, "\t")
	return DoctorFinding{Check: DoctorCheckRemovedFolder, Level: DoctorWarning, Store: store.Source(s.Name),
		Message: "store " + string(s.Name) + " attaches folder " + f + ", which the store no longer has (removed in commit " + sha + " by " + by + " on " + day + "): this repo reads nothing from it, and a write here would make the folder again",
		Fix:     "lore store remove " + string(s.Name)}, true
}

// folderMetaAt reports whether the store worktree dir has folder f's
// _meta.json (the file that makes it a folder).
func folderMetaAt(dir, f string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rowfs.LayoutStore.StorePath(f, rowfs.MetaFileName))))
	return err == nil
}
