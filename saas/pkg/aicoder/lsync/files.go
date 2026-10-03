package lsync

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"saas/pkg/aicoder/ids"
)

// On-disk names inside .lore/data/.
// ds:def id=sync-datanames-nhh4krj2 owner=@khanakia stability=stable desc="names inside .lore/data"
const (
	// DataDirName is the folder under .lore/ that holds committed row files.
	DataDirName = "data"
	// MetaFileName holds the project identity + format version (§12.3).
	MetaFileName = "_meta.json"
	// PurgedFileName lists ids deliberately purged on main, so ADOPT never
	// resurrects them from an old local DB (E47).
	PurgedFileName = "_purged.json"
	// rowFileExt is the extension of every row file.
	rowFileExt = ".json"
	// tmpPrefix marks in-flight atomic writes; leftovers are crash debris.
	tmpPrefix = ".tmp-"
)

// File modes for the data folder: the defaults git uses for a work tree, so
// files lore writes look like any other checked-out file.
const (
	dataDirMode  = 0o755
	dataFileMode = 0o644
)

// MaxFileSize caps a row file. Real rows are a few KB; a multi-MB file is
// either a mistake (a pasted log) or hostile, and committing it would bloat
// every clone forever (E32, E34).
// ds:def id=sync-maxfilesize-zk9m2syn owner=@khanakia stability=stable desc="row file size cap"
const MaxFileSize = 4 << 20

// staleTmpAge is how old a leftover tmp file must be before it is treated
// as crash debris and removed (younger ones may belong to a concurrent
// writer).
const staleTmpAge = time.Minute

// racyWindow implements git's "racily clean" rule: a file modified within
// this window of when we recorded its stat could have changed again inside
// the same timestamp tick, so it is always re-hashed.
// ds:def id=sync-racywindow-zrv7fwan owner=@khanakia stability=stable desc="stat-cache racy window"
const racyWindow = 2 * time.Second

// tmpSuffixBytes is the random suffix length of tmp file names.
const tmpSuffixBytes = 8

// tableNamePattern is the shape of every lore table name (snake_case). It
// rejects "..", nested paths and anything else that could escape the data
// dir when a path read from bookkeeping is joined back onto it.
var tableNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// relPath returns the slash-separated path of a row file relative to the
// data dir. Slashes (not filepath separators) so the bookkeeping is the
// same on every OS.
func relPath(table, id string) string { return table + "/" + id + rowFileExt }

// parseRelPath splits "table/id.json"; ok is false for anything else.
func parseRelPath(rel string) (table, id string, ok bool) {
	dir, file := path.Split(rel)
	table = strings.TrimSuffix(dir, "/")
	if !tableNamePattern.MatchString(table) || !strings.HasSuffix(file, rowFileExt) {
		return "", "", false
	}
	id = strings.TrimSuffix(file, rowFileExt)
	if ids.ValidateAny(id) != nil {
		return "", "", false
	}
	return table, id, true
}

// fileStat is the cached stat of one row file.
type fileStat struct {
	size    int64
	mtimeNs int64
}

// scanResult is the inventory of the data dir.
type scanResult struct {
	files map[string]fileStat
	// skipped lists entries that are not valid row files (unknown table
	// directory written by a newer lore, stray files, symlinks), with why.
	skipped map[string]string
}

// scanDataDir lists every row file. It never follows symlinks (E34): a
// symlinked file or directory is reported in skipped and ignored.
func scanDataDir(dataDir string, reg *Registry, now time.Time) (scanResult, error) {
	res := scanResult{files: map[string]fileStat{}, skipped: map[string]string{}}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return res, fmt.Errorf("lsync: read data dir: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() {
			if name == MetaFileName || name == PurgedFileName {
				continue
			}
			removeStaleTmp(filepath.Join(dataDir, name), name, now)
			if !strings.HasPrefix(name, tmpPrefix) {
				res.skipped[name] = "not a table directory"
			}
			continue
		}
		if e.Type()&fs.ModeSymlink != 0 {
			res.skipped[name] = "symlink ignored"
			continue
		}
		if _, ok := reg.Table(name); !ok {
			res.skipped[name] = "unknown table (written by a newer lore?)"
			continue
		}
		sub := filepath.Join(dataDir, name)
		files, err := os.ReadDir(sub)
		if err != nil {
			return res, fmt.Errorf("lsync: read %s: %w", name, err)
		}
		for _, f := range files {
			fn := f.Name()
			rel := name + "/" + fn
			if strings.HasPrefix(fn, tmpPrefix) {
				removeStaleTmp(filepath.Join(sub, fn), fn, now)
				continue
			}
			if f.Type()&fs.ModeSymlink != 0 || f.IsDir() {
				res.skipped[rel] = "not a regular file"
				continue
			}
			if _, _, ok := parseRelPath(rel); !ok {
				res.skipped[rel] = "file name is not <valid id>.json"
				continue
			}
			info, err := f.Info()
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue // removed between ReadDir and Info
				}
				return res, fmt.Errorf("lsync: stat %s: %w", rel, err)
			}
			res.files[rel] = fileStat{size: info.Size(), mtimeNs: info.ModTime().UnixNano()}
		}
	}
	return res, nil
}

func removeStaleTmp(full, name string, now time.Time) {
	if !strings.HasPrefix(name, tmpPrefix) {
		return
	}
	info, err := os.Lstat(full)
	if err != nil {
		return
	}
	if now.Sub(info.ModTime()) > staleTmpAge {
		_ = os.Remove(full) // best-effort crash-debris cleanup (E29)
	}
}

// readRowFile reads a row file with the size cap and symlink refusal.
func readRowFile(full string) ([]byte, error) {
	info, err := os.Lstat(full)
	if err != nil {
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("lsync: %s is a symlink", full)
	}
	if info.Size() > MaxFileSize {
		return nil, fmt.Errorf("lsync: %s is %d bytes (max %d)", full, info.Size(), MaxFileSize)
	}
	return os.ReadFile(full)
}

// writeFileAtomic writes b to full via a same-directory temp file + rename,
// so readers (and git) never see a half-written row file and a process
// crash leaves either the old or the new content, plus at most a tmp file
// that scanDataDir later removes.
//
// No fsync, deliberately: it costs ~7ms per file on macOS, which made a
// 10k-row bootstrap take over a minute. Row files are a projection of
// lore.db (whose WAL is durable); the one artefact a power loss can leave —
// a zero-length file — is detected and re-exported from the DB by the next
// pass (see engine.repairEmptyFile).
func writeFileAtomic(full string, b []byte) (err error) {
	if info, lerr := os.Lstat(full); lerr == nil && info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("lsync: refusing to write through symlink %s", full)
	}
	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, dataDirMode); err != nil {
		return fmt.Errorf("lsync: mkdir %s: %w", dir, err)
	}
	suffix := make([]byte, tmpSuffixBytes)
	if _, err := rand.Read(suffix); err != nil {
		return fmt.Errorf("lsync: tmp name: %w", err)
	}
	tmp := filepath.Join(dir, tmpPrefix+hex.EncodeToString(suffix))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, dataFileMode)
	if err != nil {
		return fmt.Errorf("lsync: create tmp: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp) // cleanup on failure; the original is untouched
		}
	}()
	if _, err = f.Write(b); err != nil {
		_ = f.Close()
		return fmt.Errorf("lsync: write tmp: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("lsync: close tmp: %w", err)
	}
	if err = os.Rename(tmp, full); err != nil {
		return fmt.Errorf("lsync: rename into place: %w", err)
	}
	return nil
}

// removeRowFile deletes a row file and its table directory when empty, so
// git never keeps an empty-dir artefact and scans stay cheap.
func removeRowFile(full string) error {
	if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("lsync: remove %s: %w", full, err)
	}
	dir := filepath.Dir(full)
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		_ = os.Remove(dir) // best-effort: a concurrent writer may refill it
	}
	return nil
}

// SamplePaths returns one path, relative to the data dir and slash
// separated, for every kind of file sync writes: the two control files and a
// row file in each synced table's folder. Callers ask git whether its ignore
// rules would exclude any of them; a repo-wide `_*`, `*.json` or
// `snapshots/` rule silently drops lore files from `git add` otherwise.
func SamplePaths(reg *Registry) []string {
	out := []string{MetaFileName, PurgedFileName}
	for _, name := range reg.Names() {
		out = append(out, name+"/"+samplePathID+rowFileExt)
	}
	return out
}

// samplePathID is the file name stem SamplePaths uses inside table folders.
// It is not a valid row id, so a stray file by that name is skipped rather
// than imported.
const samplePathID = "sample"
