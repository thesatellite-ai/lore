// perms.go — the file modes the old CLI creates files and folders with.
// They match the root module's row layout (adapters/backend/rowfs: files
// 0600, folders 0750): lore's files belong to the user who runs it, and a
// folder stays listable by the group. The mode only applies when a file is
// created; os.WriteFile keeps the mode of a file that already exists.
package main

import "io/fs"

const (
	// cliFileMode is the mode of every file the CLI creates (rendered
	// context files, backups, bench results, support bundles, skill files).
	cliFileMode fs.FileMode = 0o600
	// cliDirMode is the mode of every folder the CLI creates.
	cliDirMode fs.FileMode = 0o750
)
