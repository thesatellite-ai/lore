package rowfs

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
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
	rel := f.storePath(MetaFileName)
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

// SeedProjectID gives a folder with no _meta.json yet the project id pid,
// writing the file its first write would otherwise create with a natural
// id. Why: a project made before the folder existed (a v0.1.17
// shared-database project converted by lore fix) keeps its prj_ id as the
// folder's, so every row that already names that project lands in the
// folder whose project it is, and the repo that names the id finds it.
// A folder whose _meta.json names pid already is left as it is (a resumed
// run). One naming another id is E_ALREADY_INITIALIZED: it is another
// project's folder, which is never re-labelled. The root folder has no
// folder identity and is refused (E_BAD_PATH).
func (f *Folder) SeedProjectID(ctx context.Context, pid ids.ID[ids.Project]) error {
	if f.name == FolderRoot {
		return errs.New(errs.BadPath, "rowfs: the store's root folder has no project id of its own")
	}
	have, err := f.ProjectIDIfSet()
	if err != nil {
		return err
	}
	switch {
	case have == pid:
		return nil
	case !have.IsZero():
		return errs.New(errs.AlreadyInitialized, "rowfs: folder "+quote(f.name)+" is project "+have.String()+", not "+pid.String())
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(folderMeta{ProjectID: pid.String(), Version: row.FormatVersion})
	if err != nil {
		return errs.Wrap(errs.Internal, "rowfs: encode "+MetaFileName, err)
	}
	doc, err := canon.Document(raw)
	if err != nil {
		return errs.Wrap(errs.Internal, "rowfs: encode "+MetaFileName, err)
	}
	return writeAtomic(filepath.Join(f.dataDir, MetaFileName), doc, f.root.modes)
}

// ProjectIDIfSet is the project id the folder's _meta.json records, the
// zero id when the file is not there yet (unlike ProjectID it never
// computes the id a first write would record).
func (f *Folder) ProjectIDIfSet() (ids.ID[ids.Project], error) {
	doc, err := os.ReadFile(filepath.Join(f.dataDir, MetaFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return ids.ID[ids.Project]{}, nil
	}
	if err != nil {
		return ids.ID[ids.Project]{}, errs.Wrap(errs.Internal, "rowfs: read "+f.storePath(MetaFileName), err)
	}
	var m folderMeta
	if jerr := json.Unmarshal(doc, &m); jerr != nil {
		return ids.ID[ids.Project]{}, errs.Wrap(errs.InvalidInput, "rowfs: "+f.storePath(MetaFileName)+" is not JSON", jerr)
	}
	if m.ProjectID == "" {
		return ids.ID[ids.Project]{}, nil
	}
	return ids.Parse[ids.Project](m.ProjectID)
}
