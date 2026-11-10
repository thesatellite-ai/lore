package wire

// linkwrite.go — where a write to a tool file (a pointer file, the
// generated .lore/LORE.md, a path rule, a hook settings file) lands when
// that file is a symbolic link.
//
//	WriteThroughLink(root, path)
//	  ├─ path not a link (or missing) ─► path (written in place)
//	  ├─ LinkEnd: follow every hop (a chain of links, a dangling last hop)
//	  ├─ link inside root, end outside ─► E_REFUSED naming link and end
//	  └─ otherwise ─► the end (written there; the links stay links)
//
// One hop was not enough: a chain a -> b -> c written through its first
// hop replaced the middle link b with a regular file on the atomic rename.

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// maxLinkHops is how many links in a row LinkEnd follows before it calls
// the chain a loop (Linux's SYMLOOP_MAX).
const maxLinkHops = 40

// LinkEnd follows the chain of symbolic links at path to the file a write
// through it lands on. isLink is false when path is not a link (or does
// not exist); end is then path unchanged. For a link, end is the last hop
// with its folder's links resolved (absolute, comparable with a resolved
// root), whether or not that last hop exists (a dangling link). A chain
// longer than maxLinkHops is E_SYMLINK_LOOP; a hop that cannot be read is
// E_BAD_PATH.
func LinkEnd(path string) (end string, isLink bool, err error) {
	cur, err := filepath.Abs(path)
	if err != nil {
		return "", false, errs.Wrap(errs.BadPath, "resolve "+path, err)
	}
	for hops := 0; ; hops++ {
		fi, lerr := os.Lstat(cur)
		if lerr != nil {
			// Missing, a name too long, a file where a folder should be, no
			// permission: nothing to follow. The write itself then fails with
			// its own exact error (E_INVALID_INPUT for a name too long, "not a
			// directory"), as it did before links were followed.
			break
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			break
		}
		if hops == maxLinkHops {
			return "", true, errs.New(errs.SymlinkLoop, path+" is a chain of more than "+strconv.Itoa(maxLinkHops)+" symbolic links (a loop?)").
				WithHint("point " + path + " at a real file, or remove the link")
		}
		next, rerr := os.Readlink(cur)
		if rerr != nil {
			return "", true, errs.Wrap(errs.BadPath, "read the link "+cur, rerr).
				WithHint("check that the folders of " + path + " can be read")
		}
		if !filepath.IsAbs(next) {
			next = filepath.Join(filepath.Dir(cur), next)
		}
		cur, isLink = filepath.Clean(next), true
	}
	if !isLink {
		return path, false, nil
	}
	return filepath.Join(resolvedPath(filepath.Dir(cur)), filepath.Base(cur)), true, nil
}

// WriteThroughLink is where a write to path (a tool file of the
// repository at root) lands: path itself when it is not a link, else the
// end of its chain of links (LinkEnd), so a person's link layout stays
// (R27 #19) and no link of the chain is replaced by a regular file. A
// link that sits inside the repository and ends outside it is refused
// (E_REFUSED, naming the link and its end): a cloned repository could
// commit CLAUDE.md as a link to any file of the person's (~/.bashrc) and
// a render would overwrite it. A link the person keeps outside the
// repository (a --out of their own) is theirs and is followed.
func WriteThroughLink(root, path string) (string, error) {
	end, isLink, err := LinkEnd(path)
	if err != nil || !isLink {
		return path, err
	}
	at, err := filepath.Abs(path)
	if err != nil {
		return "", errs.Wrap(errs.BadPath, "resolve "+path, err)
	}
	rootReal := resolvedPath(root)
	linkAt := filepath.Join(resolvedPath(filepath.Dir(at)), filepath.Base(at))
	if insideOf(rootReal, linkAt) && !insideOf(rootReal, end) {
		return "", errs.New(errs.Refused, "refusing to write "+path+": it is a symbolic link to "+end+", outside the repository").
			WithHint("remove the link (lore writes a plain " + filepath.Base(path) + "), or point it at a file inside the repository")
	}
	return end, nil
}

// insideOf reports whether p is base or inside it (both absolute, resolved).
func insideOf(base, p string) bool {
	rel, err := filepath.Rel(base, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
