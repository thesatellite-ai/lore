package wire

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// createAsked reports whether the input carries --create (engine.CreateFlag)
// set. The check is generic (PL-CREATE-1): any operation whose input embeds
// CreateFlag gets it, with no per-noun code here.
func createAsked(in op.Input) bool {
	v, ok := in.Bool(engine.InputCreate)
	return ok && v
}

// missingFolder is the refusal of `--to <base>/<folder>` when the store has
// no such folder (Q-stores-2 typo guard): it names the folders the store
// does have and the two ways to create it on purpose (--create, or --yes as
// the answer to the question). Nothing has been written at this point.
func (a *App) missingFolder(ctx context.Context, src attach.Store, base, folderName string) error {
	msg := "store " + base + " has no folder " + folderName + " yet; nothing written"
	if dir, err := a.storeDir(ctx, src); err == nil {
		if have := dataFolders(dir); len(have) > 0 {
			msg += " (existing: " + strings.Join(have, ", ") + ")"
		}
	}
	return errs.New(errs.NeedsConfirm, msg).
		WithQuestion("create the folder " + folderName + " in store " + base + "?").
		WithHint("check the spelling; to create it on purpose rerun with --create (or --yes)")
}

// dataFolders lists the top-level folders of a store's local folder dir that
// hold a data folder (<f>/.lore/data), sorted. An unreadable dir lists
// nothing: the list only decorates a refusal.
func dataFolders(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		f, ferr := rowfs.CleanFolder(e.Name())
		if ferr != nil || f == rowfs.FolderRoot {
			continue // .lore, .git and other tool folders
		}
		if fi, serr := os.Stat(filepath.Join(dir, filepath.FromSlash(rowfs.FolderDataPath(f)))); serr == nil && fi.IsDir() {
			out = append(out, f)
		}
	}
	slices.Sort(out)
	return out
}
