package folder

import (
	"context"

	"github.com/thesatellite-ai/lore/model/ids"
)

// FolderProjectID is the data folder's permanent prj_ id
// (rowfs.Folder.ProjectID; zero for the store root). FLAG1: it fills
// `_source.folder_id` in --json rows.
func (b *Backend) FolderProjectID(ctx context.Context) (ids.ID[ids.Project], error) {
	return b.folder.ProjectID(ctx)
}
