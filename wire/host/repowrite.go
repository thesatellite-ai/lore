package host

// repowrite.go — the git wiring's guard before it writes a file of the
// work tree (.gitattributes, a hook in a hooks folder inside the work
// tree, the clone's own attributes file): D-502, never through a link or
// a linked folder that leads out of the repository.

import (
	"path/filepath"

	"github.com/thesatellite-ai/lore/wire"
)

// repoWriteRefused is wire.WriteThroughLink's refusal for a write to the
// file at path of the work tree at top (E_REFUSED naming the file, where
// it leads and where lore's context is instead), or nil when the write
// stays in the work tree or path is not in it at all (a hooks folder
// shared by every repo, a linked worktree's git folder). The callers skip
// that one file with the refusal as their warning line and go on: the
// rest of the wiring is still theirs to write.
func repoWriteRefused(top, path string) error {
	if top == "" {
		return nil
	}
	_, err := wire.WriteThroughLink(top, path)
	return err
}

// hookRefused is repoWriteRefused for the hook file hook in the hooks
// folder dir.
func hookRefused(top, dir, hook string) error {
	return repoWriteRefused(top, filepath.Join(dir, hook))
}
