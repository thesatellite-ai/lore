package rowfs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/adapters/osrename"
	"github.com/thesatellite-ai/lore/model/errs"
)

// tmpSuffixBytes is the random suffix length of tmp file names.
const tmpSuffixBytes = 8

// errSymlink is the cause inside every symlink refusal, so tests and
// doctor can tell it from other path errors with errors.Is.
var errSymlink = errors.New("rowfs: symlink refused")

// ErrSymlink reports whether err is a symlink refusal.
func ErrSymlink(err error) bool { return errors.Is(err, errSymlink) }

// refuseSymlink is the error for a symlink found at rel.
func refuseSymlink(rel string) error {
	return errs.Wrap(errs.BadPath, "rowfs: "+quote(rel)+" is a symlink; lore never follows links inside a store", errSymlink)
}

// checkChain Lstats every existing component of rel (slash-separated,
// relative to root) and refuses a symlink anywhere on the way (spec 12,
// lsync/files.go:233): a link could point a write outside the store or a
// read at a file nobody committed. Components that do not exist yet are
// fine: the writer creates them as real folders.
func checkChain(root, rel string) error {
	cur := root
	for seg := range strings.SplitSeq(rel, "/") {
		cur = filepath.Join(cur, seg)
		info, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return errs.Wrap(errs.Internal, "rowfs: stat "+quote(rel), err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return refuseSymlink(rel)
		}
	}
	return nil
}

// readCapped reads a regular file of at most MaxFileSize bytes. A missing
// file is returned as fs.ErrNotExist (wrapped) so callers branch on it.
func readCapped(full, rel string) ([]byte, error) {
	info, err := os.Lstat(full)
	if err != nil {
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, refuseSymlink(rel)
	}
	if !info.Mode().IsRegular() {
		return nil, errs.New(errs.BadPath, "rowfs: "+quote(rel)+" is not a regular file")
	}
	if info.Size() > MaxFileSize {
		return nil, tooLarge(rel, info.Size())
	}
	b, err := os.ReadFile(filepath.Clean(full))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxFileSize { // grew between Lstat and read
		return nil, tooLarge(rel, int64(len(b)))
	}
	return b, nil
}

// tooLarge is the E_BODY_TOO_LARGE error for a row file over the cap.
func tooLarge(rel string, size int64) error {
	return errs.New(errs.BodyTooLarge, "rowfs: "+quote(rel)+" is "+strconv.FormatInt(size, 10)+" bytes (max "+strconv.Itoa(MaxFileSize)+")")
}

// writeAtomic writes b to full through a same-folder temp file, fsync and
// rename, then fsyncs the folder: a reader (and git) sees the old or the
// new file, never half of one, and a crash leaves at most a tmp file that a
// later scan removes. Unlike v0.1.17 (lsync/files.go writeFileAtomic) it
// fsyncs: row files are the truth now, not a projection of a database.
func writeAtomic(full string, b []byte, m fileModes) error {
	dir, err := writeRenamed(full, b, m)
	if err != nil {
		return err
	}
	if err := syncDir(dir); err != nil {
		return errs.Wrap(errs.Internal, "rowfs: fsync a table folder", err)
	}
	return nil
}

// writeRenamed is writeAtomic without the folder fsync: the file's bytes
// are on disk (syncFile) before the rename, so a crash never leaves a
// renamed empty file; the caller fsyncs the returned folder, once per
// folder for a batch (Apply), which is what makes the rename itself
// durable. It returns the folder of full.
func writeRenamed(full string, b []byte, m fileModes) (dir string, err error) {
	if info, lerr := os.Lstat(full); lerr == nil && info.Mode()&fs.ModeSymlink != 0 {
		return "", refuseSymlink(filepath.Base(full))
	}
	dir = filepath.Dir(full)
	if err = os.MkdirAll(dir, m.dir); err != nil {
		return "", errs.Wrap(errs.Internal, "rowfs: create a table folder", err)
	}
	suffix := make([]byte, tmpSuffixBytes)
	if _, err = rand.Read(suffix); err != nil {
		return "", errs.Wrap(errs.Internal, "rowfs: tmp name", err)
	}
	tmp := filepath.Join(dir, tmpPrefix+hex.EncodeToString(suffix))
	f, err := os.OpenFile(filepath.Clean(tmp), os.O_WRONLY|os.O_CREATE|os.O_EXCL, m.file)
	if err != nil {
		return "", errs.Wrap(errs.Internal, "rowfs: create a tmp file", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, removeIfExists(tmp)) // the original is untouched
		}
	}()
	if _, err = f.Write(b); err != nil {
		return "", errs.Wrap(errs.Internal, "rowfs: write a tmp file", errors.Join(err, f.Close()))
	}
	if err = syncFile(f); err != nil {
		return "", errs.Wrap(errs.Internal, "rowfs: fsync a tmp file", errors.Join(err, f.Close()))
	}
	if err = f.Close(); err != nil {
		return "", errs.Wrap(errs.Internal, "rowfs: close a tmp file", err)
	}
	if err = osrename.Rename(tmp, full); err != nil {
		return "", errs.Wrap(errs.Internal, "rowfs: rename a row file into place", err)
	}
	return dir, nil
}

// removeIfExists removes path; a path already gone is not an error.
func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// removeStaleTmp deletes a leftover tmp file older than staleTmpAge
// (crash debris). Best effort by design: a failure leaves debris that the
// next scan retries, and never affects a row.
func removeStaleTmp(full string, now time.Time) {
	info, err := os.Lstat(full)
	if err != nil || now.Sub(info.ModTime()) <= staleTmpAge {
		return
	}
	if rmErr := os.Remove(full); rmErr != nil {
		return // retried by the next scan
	}
}

// WriteFile writes b atomically at rel (slash-separated, relative to the
// store root), refusing a symlink anywhere on the way and any path that
// could leave the root. The git backend writes merged row files with it,
// which may live in any folder of the store.
func (r *Root) WriteFile(rel string, b []byte) error {
	for seg := range strings.SplitSeq(rel, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return errs.New(errs.BadPath, "rowfs: "+quote(rel)+" is not a plain relative path")
		}
	}
	if err := checkChain(r.dir, rel); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(r.dir, filepath.FromSlash(rel)), b, r.modes)
}
