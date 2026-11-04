package rowfs

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
)

// ProjectID is the folder's permanent prj_ id: the project_id of its
// _meta.json, or, before the folder's first write created that file, the
// id that first write will record (the shared copy's, else the natural
// id; see newMeta), so the value never changes once rows exist. The root
// folder has no folder identity (lore-store.yaml is the store's), so it
// answers the zero id.
//
// FLAG1: `--json` rows carry this as `_source.folder_id`, because a folder
// name is a label while the prj_ id is permanent. Nothing is written here.
func (f *Folder) ProjectID(ctx context.Context) (ids.ID[ids.Project], error) {
	if f.name == FolderRoot {
		return ids.ID[ids.Project]{}, nil
	}
	rel := f.rel + "/" + MetaFileName
	if err := checkChain(f.root.dir, rel); err != nil {
		return ids.ID[ids.Project]{}, err
	}
	doc, err := os.ReadFile(filepath.Join(f.dataDir, MetaFileName))
	if errors.Is(err, fs.ErrNotExist) {
		doc, err = f.newMeta(ctx, rel)
	}
	if err != nil {
		return ids.ID[ids.Project]{}, errs.Wrap(errs.Internal, "rowfs: read "+rel, err)
	}
	var m folderMeta
	if jerr := json.Unmarshal(doc, &m); jerr != nil {
		return ids.ID[ids.Project]{}, errs.Wrap(errs.InvalidInput, "rowfs: "+rel+" is not JSON", jerr)
	}
	if m.ProjectID == "" {
		return ids.ID[ids.Project]{}, nil
	}
	id, err := ids.Parse[ids.Project](m.ProjectID)
	if err != nil {
		return ids.ID[ids.Project]{}, errs.Wrap(errs.InvalidInput, "rowfs: "+rel+" has an invalid project_id", err)
	}
	return id, nil
}
