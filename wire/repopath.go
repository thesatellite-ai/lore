package wire

import (
	"path/filepath"
	"strings"
)

// InRepo reports whether p (relative to root, or absolute) lands inside the
// repository at root once its folder's symlinks are resolved (the file
// itself may not exist yet; resolvedPath, knownrepos.go). It is the one rule for every --target a
// command writes into: `lore directive install` here, `lore render` and
// `lore init` in wire/renderhost, so the commands never disagree about
// what "inside the repository" means.
func InRepo(root, p string) bool {
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, filepath.FromSlash(p))
	}
	abs = filepath.Clean(abs)
	at := filepath.Join(resolvedPath(filepath.Dir(abs)), filepath.Base(abs))
	rel, err := filepath.Rel(resolvedPath(root), at)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
