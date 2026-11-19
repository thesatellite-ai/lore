package wire

// linkwrite.go — where a write to a tool file (a pointer file, the
// generated .lore/LORE.md, a path rule, a hook settings file) lands when
// that file is a symbolic link.
//
//	WriteThroughLink(root, path)
//	  ├─ path not a link (or missing) ─► path (written in place)
//	  ├─ LinkEnd: follow every hop (a chain of links, a dangling last hop)
//	  ├─ path in root, its folder a link out of root ─► E_REFUSED (D-502)
//	  ├─ link inside root, end outside ─► E_REFUSED naming link and end
//	  └─ otherwise ─► the end (written there; the links stay links)
//
// One hop was not enough: a chain a -> b -> c written through its first
// hop replaced the middle link b with a regular file on the atomic rename.
//
// Decision (D-502, owner 2026-11-17): a file lore writes in a repository
// (a tool file, a render target, a pointer file, a hook, .gitattributes)
// is never written through a link, or a linked folder, that leaves the
// repository's work tree: a CLAUDE.md linked in from another repository
// had lore's pointer block written into that other repository's file. A
// link that stays inside the work tree is written through to its end (a
// regular file there, or a new one); the link stays a link.

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/fspath"
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
	return filepath.Join(fspath.Canonical(filepath.Dir(cur)), filepath.Base(cur)), true, nil
}

// WriteThroughLink is where a write to path (a file lore writes in the
// repository at root) lands: path itself when it is not a link, else the
// end of its chain of links (LinkEnd), so a person's link layout stays
// (R27 #19) and no link of the chain is replaced by a regular file. A path
// inside the repository that leads out of it, through its own link or a
// linked folder above it, is refused (E_REFUSED, naming the path and where
// it leads): the file it reaches belongs to another repository or to the
// person (a cloned repository could commit CLAUDE.md as a link to
// ~/.bashrc). A path outside the repository (a --out of the person's own)
// is theirs and is followed.
func WriteThroughLink(root, path string) (string, error) {
	end, isLink, leaves, err := LeavesRepo(root, path)
	if err != nil {
		return "", err
	}
	if leaves {
		return "", errs.New(errs.Refused, "refusing to write "+path+": "+OutsideRepoReason(end, isLink)).
			WithHint(OutsideRepoHint(path))
	}
	if !isLink {
		return path, nil
	}
	return end, nil
}

// LeavesRepo is the one check every repository write goes through
// (WriteThroughLink, PointerLinkEnd, the git wiring's .gitattributes and
// hook writes): end is where a write to path lands (LinkEnd's end, or the
// path with its folder's links resolved), isLink whether path itself is a
// link, and leaves is true when path sits inside the work tree at root
// but end resolves outside it. A path outside root never leaves (it was
// never in). err is LinkEnd's (a loop, an unreadable hop).
func LeavesRepo(root, path string) (end string, isLink, leaves bool, err error) {
	end, isLink, err = LinkEnd(path)
	if err != nil {
		return "", isLink, false, err
	}
	at, err := filepath.Abs(path)
	if err != nil {
		return "", isLink, false, errs.Wrap(errs.BadPath, "resolve "+path, err)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", isLink, false, errs.Wrap(errs.BadPath, "resolve "+root, err)
	}
	if !isLink {
		end = filepath.Join(fspath.Canonical(filepath.Dir(at)), filepath.Base(at))
	}
	rootReal := fspath.Canonical(rootAbs)
	// Inside as typed (root/CLAUDE.md, root/.githooks/pre-push), before any
	// folder link is resolved: resolving first would call a file reached
	// through a linked folder "outside" and let it be written.
	if !insideOf(filepath.Clean(rootAbs), at) && !insideOf(rootReal, at) {
		return end, isLink, false, nil
	}
	return end, isLink, !insideOf(rootReal, end), nil
}

// OutsideRepoReason says why a file in the repository is left alone: a
// write to it lands on end, outside the repository, through the file's
// own link (isLink) or a linked folder above it.
func OutsideRepoReason(end string, isLink bool) string {
	if isLink {
		return "it is a symbolic link to " + end + ", outside the repository; lore does not write there"
	}
	return "it is in a folder that is a symbolic link out of the repository (to " + end + "); lore does not write there"
}

// OutsideRepoHint is the way on for the file at path lore left alone
// because it leads out of the repository: where lore's context is instead,
// and how to have lore write the file here.
func OutsideRepoHint(path string) string {
	return "lore's context is still there: run `lore context`, or read the private render `lore render --report` names; to have lore write " +
		filepath.Base(path) + " here, replace the link with a plain file or point it at a file inside the repository"
}

// insideOf reports whether p is base or inside it (both absolute, resolved).
func insideOf(base, p string) bool {
	rel, err := filepath.Rel(base, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
