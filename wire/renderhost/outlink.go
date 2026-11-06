package renderhost

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// refuseOutsideLink refuses to write the generated out file (default
// .lore/LORE.md) through a symbolic link that sits inside the repository
// at root and leads outside it.
//
// Why: render writes through a symlinked out file to keep a person's link
// layout (R27 #19), and writes LORE.full.md beside the link's target. A
// cloned repository can commit .lore/LORE.md as a link to any file of the
// person's (~/.bashrc, another repo's notes): following it would overwrite
// that file and drop LORE.full.md in its folder. A link whose target stays
// in the repository is still followed, as the pointer files are (those are
// written through an os.Root on their folder, so their links never leave
// it); an out path the person names outside the repository with --out is
// theirs and is not checked.
func refuseOutsideLink(root, outPath string) error {
	fi, err := os.Lstat(outPath)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return nil // missing or a regular file: written in place
	}
	rootReal := realPath(root)
	at, err := filepath.Abs(outPath)
	if err != nil {
		return errs.Wrap(errs.BadPath, "resolve "+outPath, err)
	}
	// Where the link itself sits (its folder resolved, the name kept).
	linkAt := filepath.Join(realPath(filepath.Dir(at)), filepath.Base(at))
	if !within(rootReal, linkAt) {
		return nil
	}
	target, err := filepath.EvalSymlinks(at)
	if err != nil {
		// A dangling link: its first hop is where the write would go.
		hop, rerr := os.Readlink(at)
		if rerr != nil {
			return errs.Wrap(errs.BadPath, "read the link "+outPath, rerr)
		}
		if !filepath.IsAbs(hop) {
			hop = filepath.Join(filepath.Dir(linkAt), hop)
		}
		target = filepath.Join(realPath(filepath.Dir(hop)), filepath.Base(hop))
	}
	if within(rootReal, target) {
		return nil
	}
	return errs.New(errs.Refused, "refusing to write "+outPath+": it is a symbolic link to "+target+", outside the repository").
		WithHint("remove the link (lore writes a plain " + filepath.Base(outPath) + "), or point it inside the repository")
}

// realPath is p with every symlink resolved, or p made absolute when it
// cannot be resolved (it does not exist yet).
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// within reports whether p is base or inside it (both absolute, resolved).
func within(base, p string) bool {
	rel, err := filepath.Rel(base, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
