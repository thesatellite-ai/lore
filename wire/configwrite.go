package wire

// configwrite.go — where a write of ~/.lore/config.yaml lands, and when it
// is refused.
//
//	writeMachineConfigFile(path)
//	  ├─ path a link (a dotfiles repo owns the file) ─► write at the end of
//	  │    the chain (LinkEnd); every link stays a link
//	  ├─ the file is read-only (no owner write bit) ─► E_BAD_PATH naming it,
//	  │    hint: chmod u+w; nothing written
//	  └─ otherwise ─► writeFileAtomic (temp file + rename)
//
// Every write of config.yaml also retires the old top-level identity: into
// the default solo setup when that needs no choice (SOURCE_OF_TRUTH row 1,
// identity_source.go): a write lore makes anyway, never a write of its own.
//
// The atomic rename alone replaced a linked config.yaml with a regular file
// (the dotfiles copy silently stopped being the one lore reads), and
// replaced a file the person made read-only, because a rename needs only
// the folder to be writable.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/thesatellite-ai/lore/adapters/fsreason"
	"github.com/thesatellite-ai/lore/model/errs"
)

// ownerWriteBit is the permission bit whose absence marks a file the
// person made read-only.
const ownerWriteBit fs.FileMode = 0o200

// writeMachineConfigFile writes config.yaml at path: through its link
// (to the end of the chain) and never over a read-only file.
func writeMachineConfigFile(path string, b []byte, mode os.FileMode) error {
	end, _, err := LinkEnd(path)
	if err != nil {
		return err
	}
	if err := refuseReadOnly(end, path); err != nil {
		return err
	}
	if filepath.Base(path) == ConfigFile {
		b = migrateTopIdentityOnWrite(b)
	}
	return writeFileAtomic(end, b, mode)
}

// refuseReadOnly is E_BAD_PATH when the file at end (shown to the person
// as shown, the path they know) exists without its owner write bit: lore
// keeps a file the person made read-only and says how to allow the write.
// A missing file is no refusal.
func refuseReadOnly(end, shown string) error {
	info, err := os.Stat(end)
	if err != nil || info.Mode().Perm()&ownerWriteBit != 0 {
		return nil
	}
	return ReadOnlyFileError(shown, info.Mode().Perm())
}

// ReadOnlyFileError is the refusal to write a file the person made
// read-only: it names the file and its mode, and the chmod that allows it.
func ReadOnlyFileError(path string, perm fs.FileMode) *errs.Error {
	return errs.New(errs.BadPath, path+" is read-only (mode "+modeString(perm)+"); lore does not replace a file you made read-only").
		WithHint("to let lore write it: chmod u+w " + path + ", then run the command again")
}

// modeString is perm as four octal digits (0444).
func modeString(perm fs.FileMode) string {
	const digits = "01234567"
	p := uint32(perm.Perm())
	return string([]byte{'0', digits[p>>6&7], digits[p>>3&7], digits[p&7]})
}

// WriteError is a file lore could not create or write (a pointer file, the
// rendered LORE.md, a hook settings file, config.yaml): E_BAD_PATH naming
// the file and the operating system's reason. When the reason is a
// permission the person can change, the hint is the chmod of the file or
// folder that refused (a read-only CLAUDE.md, a read-only repo folder);
// otherwise it is fallbackHint. An opaque E_INTERNAL "write <file>" left
// the person guessing what to change.
func WriteError(what, path string, err error, fallbackHint string) *errs.Error {
	e := errs.Wrap(errs.BadPath, what+" "+path+": "+fsreason.Of(path, err), err)
	if !errors.Is(err, fs.ErrPermission) {
		return e.WithHint(fallbackHint)
	}
	return e.WithHint(permissionHint(runtime.GOOS, refusingPath(path)))
}

// permissionHint is how to give lore write access to p on goos. Windows
// has no chmod: a file lore may not write is read-only (its attribute) or
// closed to the account by its permissions.
func permissionHint(goos, p string) string {
	if goos == "windows" {
		return "to let lore write it: clear its read-only attribute (attrib -r \"" + p + "\") or give your account write access to it, then run the command again"
	}
	return "to let lore write it: chmod u+w " + p + ", then run the command again"
}

// refusingPath is the file or folder whose missing owner write bit blocks
// a write of path: path itself when it exists read-only, else its nearest
// existing folder.
func refusingPath(path string) string {
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&ownerWriteBit == 0 {
		return path
	}
	for d := filepath.Dir(path); ; d = filepath.Dir(d) {
		if _, err := os.Stat(d); err == nil || d == filepath.Dir(d) {
			return d
		}
	}
}
