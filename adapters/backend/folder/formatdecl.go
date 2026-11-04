package folder

import (
	"context"

	"github.com/thesatellite-ai/lore/logic/formatgate"
)

// Format is the data folder's format declaration (rowfs.Folder.Format;
// engine.FormatReader): the features and min_lore_version of its
// _meta.json, an E_OLD_LORE error when that file cannot be read.
func (b *Backend) Format(ctx context.Context) (formatgate.Declared, error) {
	return b.folder.Format(ctx)
}
