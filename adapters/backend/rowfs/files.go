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
	"sync"
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
	if errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err != nil {
		return nil, unreadable(full, rel, err)
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
	if errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err != nil {
		return nil, unreadable(full, rel, err)
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
	writeGen.Add(1) // any remembered IndexView is out of date (indexview.go)
	if info, lerr := os.Lstat(full); lerr == nil && info.Mode()&fs.ModeSymlink != 0 {
		return "", refuseSymlink(filepath.Base(full))
	}
	dir = filepath.Dir(full)
	if err = mkdirModes(dir, m); err != nil {
		return "", errs.FileIO("create the table folder", dir, err)
	}
	suffix := make([]byte, tmpSuffixBytes)
	if _, err = rand.Read(suffix); err != nil {
		return "", errs.Wrap(errs.Internal, "rowfs: tmp name", err)
	}
	tmp := filepath.Join(dir, tmpPrefix+hex.EncodeToString(suffix))
	inflight.Store(tmp, struct{}{})
	defer inflight.Delete(tmp) // after the rename or the removal below
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
	if err = groupWiden(tmp, dir, m.file, groupFileBits); err != nil {
		return "", errs.Wrap(errs.Internal, "rowfs: give a row file its group's access", err)
	}
	if err = osrename.Rename(tmp, full); err != nil {
		return "", errs.Wrap(errs.Internal, "rowfs: rename a row file into place", err)
	}
	return dir, nil
}

// removeIfExists removes path; a path already gone is not an error.
func removeIfExists(path string) error {
	writeGen.Add(1) // any remembered IndexView is out of date (indexview.go)
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// inflight holds the tmp paths this process is writing right now
// (writeRenamed): sweepTmp never removes one, so a sweep under a lock held
// reentrantly (Root.Hold) cannot pull a tmp file from under a writer of
// the same process.
var inflight sync.Map

// removeStaleTmp deletes a leftover tmp file older than staleTmpAge
// (crash debris) by its real modification time against the wall clock:
// never the injected clock (WithClock, LORE_NOW), under which a fresh
// file's age is negative and the file would stay forever. A scan holds no
// lock, so a younger file may belong to a running writer and is kept.
// Best effort by design: a failure leaves debris that the next scan or
// locked write retries, and never affects a row.
func removeStaleTmp(full string) {
	info, err := os.Lstat(full)
	if err != nil || time.Since(info.ModTime()) <= staleTmpAge {
		return
	}
	if rmErr := os.Remove(full); rmErr != nil {
		return // retried by the next scan
	}
}

// sweepTmp removes every tmp file in dir whatever its age, except the ones
// this process is writing (inflight). Only a writer holding the store lock
// calls it: no other process can be mid-write then, so every other tmp
// file is debris of a writer that died (an agent session killed mid-write)
// and would otherwise sit in git status until a scan found it a minute
// old. Best effort: a failure leaves the file for the next sweep.
func sweepTmp(dir string) {
	list, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range list {
		name := e.Name()
		if !strings.HasPrefix(name, tmpPrefix) {
			continue
		}
		full := filepath.Join(dir, name)
		if _, busy := inflight.Load(full); busy {
			continue
		}
		_ = removeIfExists(full) // best effort, see above
	}
}

// WriteFile writes b atomically at rel (slash-separated, relative to the
// store root), refusing a symlink anywhere on the way and any path that
// could leave the root. The git backend writes merged row files with it,
// which may live in any folder of the store. The caller holds the store
// lock (every caller merges inside a locked sync), so the folder's tmp
// debris is swept after the write (sweepTmp).
func (r *Root) WriteFile(rel string, b []byte) error {
	for seg := range strings.SplitSeq(rel, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return errs.New(errs.BadPath, "rowfs: "+quote(rel)+" is not a plain relative path")
		}
	}
	if err := checkChain(r.dir, rel); err != nil {
		return err
	}
	full := filepath.Join(r.dir, filepath.FromSlash(rel))
	if err := writeAtomic(full, b, r.modes); err != nil {
		return err
	}
	sweepTmp(filepath.Dir(full))
	return nil
}

// mkdirModes is os.MkdirAll(dir, m.dir) that also gives each folder it
// creates its group's access in a setgid parent (groupWiden): a new table
// folder of a group's shared checkout is 2770/2775, never 2700 (I-11).
func mkdirModes(dir string, m fileModes) error {
	var made []string
	for cur := dir; ; cur = filepath.Dir(cur) {
		if _, err := os.Lstat(cur); err == nil || !errors.Is(err, fs.ErrNotExist) {
			break
		}
		made = append(made, cur)
		if filepath.Dir(cur) == cur {
			break
		}
	}
	if err := os.MkdirAll(dir, m.dir); err != nil {
		return err
	}
	for i := len(made) - 1; i >= 0; i-- { // parents first: a child checks its parent's bit
		if err := groupWiden(made[i], filepath.Dir(made[i]), m.dir, groupDirBits); err != nil {
			return err
		}
	}
	return nil
}

// groupWiden adds bits (group read/write) to path when the write meant
// group access (want holds group read: a shared work-tree mode, never an
// owner-only one) and parent is setgid, the mark of a folder a group
// shares (git's core.sharedRepository=group sets it; so does `chmod g+s`).
// Elsewhere the umask alone decides, as for git's own checkout.
func groupWiden(path, parent string, want, bits fs.FileMode) error {
	if want&0o040 == 0 {
		return nil
	}
	pi, err := os.Stat(parent)
	if err != nil || pi.Mode()&fs.ModeSetgid == 0 {
		return nil //nolint:nilerr // no setgid parent (or none to read): the umask decides
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	have := info.Mode()
	if have.Perm()&bits == bits {
		return nil
	}
	keep := have.Perm() | bits
	if have&fs.ModeSetgid != 0 {
		keep |= fs.ModeSetgid
	}
	return os.Chmod(path, keep)
}
