// Package fsreason words why a file could not be read or written the same
// way on every operating system. A refusal shows the reason to the person
// (surfaces print the message, never the cause); Linux and macOS say "no
// such file or directory", "not a directory", "is a directory" or
// "permission denied", while Windows says "The system cannot find the
// file specified." or, for a folder read as a file, only "Incorrect
// function." Of returns the POSIX words for those four cases on every OS
// and the operating system's own words for anything else.
package fsreason

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/thesatellite-ai/lore/logic/gitfault"
)

// The reasons Of words alike on every operating system.
const (
	NotExist   = "no such file or directory"
	NotDir     = "not a directory"
	IsDir      = "is a directory"
	Permission = "permission denied"
)

// Of is the reason err (from an operation on path) failed. path may be ""
// when err is an *fs.PathError, whose own path is used then.
func Of(path string, err error) string {
	reason := err.Error()
	var pe *fs.PathError
	if errors.As(err, &pe) {
		reason = pe.Err.Error()
		if path == "" {
			path = pe.Path
		}
	}
	switch {
	case path != "" && isDir(path):
		return IsDir
	case errors.Is(err, fs.ErrNotExist):
		if path != "" && throughFile(path) {
			return NotDir
		}
		return NotExist
	case errors.Is(err, fs.ErrPermission):
		return Permission
	}
	return reason
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// throughFile reports whether a folder of p is a file (x.md/y.md), which
// Windows reports as a path not found.
func throughFile(p string) bool {
	for d := filepath.Dir(p); d != filepath.Dir(d); d = filepath.Dir(d) {
		st, err := os.Stat(d)
		if err == nil {
			return !st.IsDir()
		}
	}
	return false
}

// GitPathState is gitfault's PathCheck: whether a local path a git remote
// names is absent, there but unreadable for this user (a folder that
// cannot be listed, a file that cannot be opened), or readable. git says
// "does not appear to be a git repository" for both of the first two;
// only the second keeps a correct address (B3-22).
func GitPathState(path string) gitfault.PathState {
	p := filepath.Clean(path)
	if _, err := os.Stat(p); err != nil {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return gitfault.PathAbsent
		case errors.Is(err, fs.ErrPermission):
			return gitfault.PathUnreadable
		}
		return gitfault.PathUnknown
	}
	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return gitfault.PathUnreadable
		}
		return gitfault.PathUnknown
	}
	_ = f.Close() // opened only to learn whether it can be read
	return gitfault.PathReadable
}
