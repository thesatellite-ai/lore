// perms.go — the file modes the old CLI creates files and folders with.
// They match the root module's row layout (adapters/backend/rowfs: files
// 0600, folders 0750): lore's files belong to the user who runs it, and a
// folder stays listable by the group. The mode only applies when a file is
// created; os.WriteFile keeps the mode of a file that already exists.
package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	// cliFileMode is the mode of every file the CLI creates (rendered
	// context files, backups, bench results, support bundles, skill files).
	cliFileMode fs.FileMode = 0o600
	// cliDirMode is the mode of every folder the CLI creates.
	cliDirMode fs.FileMode = 0o750
)

// writeFileInDir writes data to path through an os.Root opened on path's
// parent folder (P2-LINT-5), so the write lands in that folder and nowhere
// else: a name that is a symlink leading outside the folder is refused
// rather than followed. mode applies only when the file is created.
func writeFileInDir(path string, data []byte, mode fs.FileMode) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(root.WriteFile(filepath.Base(path), data, mode), root.Close())
}
