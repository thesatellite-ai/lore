package wire

// pointerlink.go — where a render stitches lore's pointer block when the
// tool file (CLAUDE.md, AGENTS.md, a --target) is a symbolic link.
//
//	PointerLinkEnd(root, path)
//	  ├─ chain unreadable / a loop ────────────────► skip: <why>
//	  ├─ in the repo, leading out of it (LeavesRepo: its own link or a
//	  │  linked folder) ───────────────────────────► skip, naming the end
//	  ├─ path not a link ─────────────────────────► path (unchanged behaviour)
//	  ├─ end missing, inside the repo ─────────────► end (created there)
//	  ├─ end missing, outside the repo (dangling) ─► skip
//	  ├─ end a folder / not a regular file ────────► skip
//	  ├─ a link of the person's outside the repo,
//	  │  end in a system folder ───────────────────► skip
//	  └─ otherwise (end a regular file) ───────────► end (written through)
//
// Decision (D-502, owner 2026-11-17): a pointer file inside the repository
// that leads out of it is never written through, whatever its end holds.
// It used to be when the end had the same file name (a symlink manager
// linking CLAUDE.md in from a notes repository), and a CLAUDE.md linked to
// another repository's CLAUDE.md got lore's pointer block in that other
// repository's tracked file. Such a file is skipped with one line naming
// it, where it leads and where lore's context is instead (OutsideRepoHint);
// the render goes on, never a failure. A link that stays inside the
// repository is written through to its end when that is a regular file
// (or does not exist yet), so CLAUDE.md -> AGENTS.md keeps one block.

import (
	"io/fs"
	"os"
	"runtime"

	"github.com/thesatellite-ai/lore/model/fspath"
)

// PointerSkippedMark is in render's note for a pointer file it left as it
// is (PointerLinkEnd's skip): "<file>: pointer skipped: <why>". lore fix
// lifts those lines into its render step's report.
const PointerSkippedMark = ": pointer skipped: "

// systemDirs are folders a pointer link is never written through, even to
// a file of the same name: the operating system's own places.
var systemDirs = []string{"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/dev", "/proc", "/sys", "/System", "/Library", "/private/etc"}

// PointerLinkEnd is where render stitches the pointer block of the tool
// file at path (of the repository at root): the file to read and write,
// or a non-empty skip saying why the file is left as it is (named in a
// note; the render goes on). isLink reports whether path is a link, so
// the caller can treat a failed write through it as a skip too.
func PointerLinkEnd(root, path string) (end string, isLink bool, skip string) {
	end, isLink, leaves, err := LeavesRepo(root, path)
	switch {
	case err != nil:
		return "", true, err.Error()
	case leaves:
		return "", isLink, OutsideRepoReason(end, isLink) + " (" + OutsideRepoHint(path) + ")"
	case !isLink:
		return path, false, ""
	}
	inside := insideOf(fspath.Canonical(root), end)
	fi, err := os.Stat(end)
	switch {
	case err != nil && os.IsNotExist(err):
		if inside {
			return end, true, "" // created at the end, as before
		}
		return "", true, "it is a symbolic link to " + end + ", which does not exist"
	case err != nil:
		return "", true, "it is a symbolic link to " + end + ", which cannot be read (" + err.Error() + ")"
	case fi.IsDir():
		return "", true, "it is a symbolic link to " + end + ", which is a folder"
	case !fi.Mode().IsRegular():
		return "", true, "it is a symbolic link to " + end + ", which is not a regular file"
	}
	if !inside && inSystemDir(end) {
		return "", true, "it is a symbolic link to " + end + ", in a system folder; lore does not write there"
	}
	return end, true, ""
}

// inSystemDir reports whether p (absolute, resolved) is in one of
// systemDirs. Windows paths have no such list: the same-kind rule guards
// them.
func inSystemDir(p string) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	for _, d := range systemDirs {
		if insideOf(d, p) {
			return true
		}
	}
	return false
}

// PointerFileMode is the mode to keep when lore rewrites the file at path
// (a pointer file's link end): its own, or def when it cannot be read.
func PointerFileMode(path string, def fs.FileMode) fs.FileMode {
	if fi, err := os.Stat(path); err == nil {
		return fi.Mode().Perm()
	}
	return def
}
