package errs

// fileio.go — FileIO, the error of a file lore must read or write under
// the home folder (~/.lore/config.yaml, ~/.lore/state, …) when the
// filesystem says no. Before it these were E_INTERNAL "wire: read
// config.yaml" with neither the path nor a way out, when ~/.lore was a
// file or a folder the person could not write.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// FileIO is "cannot <verb> <path>" with a code and a fix that follow the
// cause:
//
//	permission denied            ─▶ E_READ_ONLY_FS, hint: chmod u+rwx the folder
//	a file where a folder must be ─▶ E_BAD_PATH, hint: move the file aside
//	anything else                ─▶ E_INTERNAL, hint: check the path
func FileIO(verb, path string, cause error) *Error {
	dir := filepath.Dir(path)
	msg := "cannot " + verb + " " + path
	switch {
	case errors.Is(cause, fs.ErrPermission):
		return Wrap(ReadOnlyFS, msg+": permission denied", cause).WithHint(PermissionHint(path))
	case errors.Is(cause, syscall.ENOTDIR) || errors.Is(cause, fs.ErrExist):
		return Wrap(BadPath, msg+": a file is in the way of a folder", cause).
			WithHint("a file stands where lore keeps a folder on the way to " + path + "; move it aside (mv it to a .bak name) and run the command again")
	case diskFull(cause):
		return Wrap(DiskFull, msg+": no space left on the device", cause).
			WithHint("free some space on the disk that holds " + dir + ", then run the command again")
	case readOnlyFS(cause):
		return Wrap(ReadOnlyFS, msg+": the file system is read-only", cause).
			WithHint("use a folder on a writable disk")
	case badName(cause):
		// B3-28: a path this OS cannot have (a Windows drive path on Linux,
		// a name too long, a character the file system refuses)
		return Wrap(BadPath, msg+": "+osReason(cause), cause).
			WithHint("check the path " + path + ": it must be a folder this machine can create")
	}
	return Wrap(Internal, msg+": "+osReason(cause), cause).WithHint("check " + path + " (ls -ld " + dir + ")")
}

// PermissionHint is the fix of a permission-denied path: it names what
// actually blocks (devco:U-4), so following it fixes the error:
//
//	a folder on the way that cannot be entered ─▶ chmod u+rwx that folder
//	path itself, when it exists and is not writable ─▶ chmod u+rw the file (u+rwx a folder)
//	else the nearest folder that exists          ─▶ chmod u+rwx that folder
//
// Before, the hint always chmodded filepath.Dir(path): for a read-only lock
// file (or a clone folder locked at 000) the same error came back after it.
func PermissionHint(path string) string {
	what, isDir := blockingPath(path)
	if isDir {
		return "lore needs to write " + what + ": make it yours and writable (chmod u+rwx " + what + ")"
	}
	return "lore needs to write " + what + ": make it yours and writable (chmod u+rw " + what + ")"
}

// blockingPath is the path that denies access to path (see PermissionHint)
// and whether it is a folder.
func blockingPath(path string) (string, bool) {
	clean := filepath.Clean(path)
	var chain []string // the ancestors of clean, from the root down
	for p := filepath.Dir(clean); ; p = filepath.Dir(p) {
		chain = append([]string{p}, chain...)
		if filepath.Dir(p) == p {
			break
		}
	}
	nearest := filepath.Dir(clean)
	for _, p := range chain {
		fi, err := os.Stat(p)
		if err != nil {
			break // the rest of the way does not exist yet
		}
		if fi.IsDir() && !searchable(p) {
			return p, true // cannot even pass through it
		}
		nearest = p
	}
	if fi, err := os.Stat(clean); err == nil {
		if !writable(clean) {
			return clean, fi.IsDir()
		}
	}
	return nearest, true
}

// osReason is the operating system's own words for cause ("invalid
// argument", "file name too long"): the reason without the path, which
// the message names already.
func osReason(cause error) string {
	var pe *fs.PathError
	if errors.As(cause, &pe) {
		return pe.Err.Error()
	}
	return cause.Error()
}

// FileIOOr is FileIO for an err that may already be a lore error: a coded
// error (anything but E_INTERNAL) keeps its code, message and hint behind
// "<verb> <path>"; anything else is classified by FileIO.
func FileIOOr(verb, path string, err error) *Error {
	if c := CodeOf(err); c != Internal && c != "" {
		return Prefix(verb+" "+path, err)
	}
	return FileIO(verb, path, err)
}
