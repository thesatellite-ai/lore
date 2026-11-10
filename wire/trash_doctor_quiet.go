package wire

// trash_doctor_quiet.go — the trash doctor row reads this machine's safety
// copies without the "this machine only: …" heading. That line heads a
// `lore trash list --local` answer (App.LocalTrash prints it through
// Options.Notice); `lore doctor` reads the same copies only to count the
// ones of purged rows, so printing the heading there was a stray line
// above the report.

import (
	"context"
	"path/filepath"

	opstrash "github.com/thesatellite-ai/lore/ops/trash"
)

// quietTrashEnv is the App as opstrash.Env with a LocalTrash that prints
// nothing; every other method is the App's own.
type quietTrashEnv struct{ *App }

// LocalTrash implements opstrash.LocalCopies without the listing heading.
func (q quietTrashEnv) LocalTrash(ctx context.Context) ([]opstrash.LocalCopy, error) {
	top, err := q.repoTop(ctx)
	if err != nil {
		return nil, err
	}
	return readLocalTrash(ctx, filepath.Join(top, filepath.FromSlash(v017LoreDB)))
}
