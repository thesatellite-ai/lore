package wire

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// folderIDOf is implemented by the git and folder backends
// (rowfs.Folder.ProjectID).
type folderIDOf interface {
	FolderProjectID(ctx context.Context) (ids.ID[ids.Project], error)
}

// DescribeSource implements store.SourceDescriber: the `_source` object of
// a row of the attached store name.
//
// FLAG1 (owner C): `--json` rows carry {store, store_id, folder,
// folder_id} so scripts key on the permanent sto_/prj_ ids while names stay
// readable. A `<store>/<folder>` target name (`--to northwind/globex`)
// splits into its store and folder; an org store attached at a folder
// under its bare name (`northwind` at `acme`) reports that folder; a store
// attached at its root has no folder. The store id comes from
// lore-store.yaml (absent for a v0.1.17 marker), the folder id from the
// folder's _meta.json. A name this App does not know, or an App not opened
// yet, is described by its name alone; an unreadable _meta.json leaves
// folder_id out rather than failing a read.
func (a *App) DescribeSource(name store.Source) store.SourceRef {
	ref := store.SourceRef{Store: string(name)}
	if s, f, ok := strings.Cut(string(name), targetSep); ok {
		ref.Store, ref.Folder = s, f
	}
	a.mu.Lock()
	var b store.Backend
	if a.st != nil {
		b = a.st.byName[name]
		if f := strings.Trim(a.st.folders[name], "/"); f != "" && f != rowfs.FolderRoot {
			ref.Folder = f
		}
	}
	a.mu.Unlock()
	if b == nil {
		return ref
	}
	c := concrete(b)
	if m, ok := c.(markerOf); ok {
		ref.StoreID = m.Marker().ID
	}
	if fb, ok := c.(folderIDOf); ok {
		if id, err := fb.FolderProjectID(context.Background()); err == nil {
			ref.FolderID = id
		}
	}
	return ref
}
