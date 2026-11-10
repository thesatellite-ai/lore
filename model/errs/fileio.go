package errs

// fileio.go — FileIO, the error of a file lore must read or write under
// the home folder (~/.lore/config.yaml, ~/.lore/state, …) when the
// filesystem says no. Before it these were E_INTERNAL "wire: read
// config.yaml" with neither the path nor a way out, when ~/.lore was a
// file or a folder the person could not write.

import (
	"errors"
	"io/fs"
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
		return Wrap(ReadOnlyFS, msg+": permission denied", cause).
			WithHint("lore needs to write " + dir + ": make it yours and writable (chmod u+rwx " + dir + ")")
	case errors.Is(cause, syscall.ENOTDIR) || errors.Is(cause, fs.ErrExist):
		return Wrap(BadPath, msg+": a file is in the way of a folder", cause).
			WithHint("a file stands where lore keeps a folder on the way to " + path + "; move it aside (mv it to a .bak name) and run the command again")
	}
	return Wrap(Internal, msg, cause).WithHint("check " + path + " (ls -ld " + dir + ")")
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
