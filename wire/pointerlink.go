package wire

// pointerlink.go — where a render stitches lore's pointer block when the
// tool file (CLAUDE.md, AGENTS.md, a --target) is a symbolic link.
//
//	PointerLinkEnd(root, path)
//	  ├─ path not a link ─────────────────────────► path (unchanged behaviour)
//	  ├─ chain unreadable / a loop ────────────────► skip: <why>
//	  ├─ end missing, inside the repo ─────────────► end (created there)
//	  ├─ end missing, outside the repo (dangling) ─► skip
//	  ├─ end a folder / not a regular file ────────► skip
//	  ├─ link inside the repo, end outside it:
//	  │    ├─ end in a system folder ──────────────► skip
//	  │    ├─ end cannot be read ──────────────────► skip
//	  │    ├─ end neither the same file name nor
//	  │    │  already holding lore's pointer block ─► skip
//	  │    └─ otherwise ───────────────────────────► end (written through)
//	  └─ otherwise (end inside, or a link of the person's outside) ► end
//
// Why the old refusal existed: WriteThroughLink refuses a link inside the
// repository that ends outside it, because a cloned repository could
// commit CLAUDE.md as a link to ~/.bashrc and a render would overwrite
// it. A person who links their tool files in from a private notes repo
// (a symlink manager) hit that refusal and the whole repo's render failed.
// Decision: a pointer file is written through such a link only when the
// end is the same kind of file (the same file name, CLAUDE.md -> …/
// CLAUDE.md, or one already holding lore's pointer block), a regular file,
// readable, and not in a system folder; anything else is skipped with the
// reason, never a failure. The pointer block is a short import line that
// is replaced in place, so the person's other text is kept. Other writes
// (the out file, path rules, folder files) keep WriteThroughLink's refusal.

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/thesatellite-ai/lore/logic/toolprofile"
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
	end, isLink, err := LinkEnd(path)
	if err != nil {
		return "", true, err.Error()
	}
	if !isLink {
		return path, false, ""
	}
	rootReal := resolvedPath(root)
	inside := insideOf(rootReal, end)
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
	at, aerr := filepath.Abs(path)
	if aerr != nil {
		return "", true, "cannot resolve " + path + ": " + aerr.Error()
	}
	linkAt := filepath.Join(resolvedPath(filepath.Dir(at)), filepath.Base(at))
	if inside || !insideOf(rootReal, linkAt) {
		return end, true, ""
	}
	if inSystemDir(end) {
		return "", true, "it is a symbolic link to " + end + ", in a system folder; lore does not write there"
	}
	data, rerr := os.ReadFile(filepath.Clean(end))
	if rerr != nil {
		return "", true, "it is a symbolic link to " + end + ", which cannot be read (" + rerr.Error() + ")"
	}
	if !strings.EqualFold(filepath.Base(end), filepath.Base(path)) && !toolprofile.HasPointer(string(data)) {
		return "", true, "it is a symbolic link to " + end + ", outside the repository, which is neither a " + filepath.Base(path) +
			" nor a file holding lore's pointer block; lore does not write through it"
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
