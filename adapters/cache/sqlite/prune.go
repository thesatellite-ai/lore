package sqlite

// prune.go — the cache layout helpers `lore cache prune` needs (FLAG3).
//
// FLAG3: cache prune only removes caches no repo on this machine attaches
// any more, because caches always rebuild from row files and old client
// data must not linger. lore kept no list of the repos that use a cache, so
// every core open records one: <cache root>/repos/<sha256 of repo top>.json
// lists the cache files that repo's open used. A cache file no live record
// lists is unattached. The decision of which record is live (the repo still
// exists, its stores.yaml still names the store) belongs to the caller;
// these helpers only read, write and list, and never touch a path outside
// the cache root.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/osrename"
	"github.com/thesatellite-ai/lore/model/errs"
)

const (
	// UsesDir is the folder under the cache root holding one use record
	// per repo (FLAG3).
	UsesDir = "repos"
	// StoresDir is the folder under the cache root holding the per-store
	// caches (<store id>/<branch>/<folder>.db, spec 7.4). It matches
	// wire's cacheStoresDir.
	StoresDir = "stores"
	// CombinedDir is the folder under the cache root holding the CFG3
	// combined caches. It matches wire's combinedDir.
	CombinedDir = "combined"
	// cacheFileExt ends every cache file name (wire's cacheExt).
	cacheFileExt = ".db"
	// useRecordExt ends a use record's file name.
	useRecordExt = ".json"
	// useFileMode / useDirMode: the records name repo paths and store
	// names, so they are the owner's only (as the caches themselves).
	useFileMode = 0o600
	useDirMode  = 0o700
)

// cacheFileSidecars are the files SQLite keeps beside a cache in WAL mode;
// they are part of the cache and go with it.
var cacheFileSidecars = []string{"-wal", "-shm"}

// Use is one cache file a repo's core open used.
type Use struct {
	// Path is the cache file relative to the cache root, slash-separated
	// (stores/<id>/<branch>/<folder>.db or combined/<hash>.db).
	Path string `json:"path"`
	// Store is the attached source name the cache serves (for a combined
	// cache, one Use per source with the same Path).
	Store string `json:"store"`
	// Committed is true when the attachment came from the repo's
	// .lore/stores.yaml, so a caller can see a `lore store remove` at once
	// by re-reading that file; other attachments (org hook, personal `me`,
	// --to targets) are refreshed by the repo's next core open.
	Committed bool `json:"committed,omitempty"`
}

// UseRecord is one repo's record: the repo top and the caches it used at
// its last core open.
type UseRecord struct {
	// Repo is the absolute repo top the record belongs to.
	Repo string `json:"repo"`
	// Caches are the cache files that open used, in open order.
	Caches []Use `json:"caches"`
}

// UseOf is the Use of the cache file at path (absolute, under cacheRoot)
// serving source store; committed says the attachment came from the
// repo's .lore/stores.yaml. A path outside cacheRoot keeps its absolute
// slash form, which StoreCaches never lists, so it protects nothing.
func UseOf(cacheRoot, path, store string, committed bool) Use {
	rel, err := filepath.Rel(cacheRoot, path)
	if err != nil {
		rel = path
	}
	return Use{Path: filepath.ToSlash(rel), Store: store, Committed: committed}
}

// UseRecordPath is where repoTop's record lives under cacheRoot.
func UseRecordPath(cacheRoot, repoTop string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(repoTop)))
	return filepath.Join(cacheRoot, UsesDir, hex.EncodeToString(sum[:])+useRecordExt)
}

// RecordUse writes repoTop's record (atomic: temp file then rename). It
// writes nothing when the record already holds exactly these uses, so an
// ordinary command does not rewrite the file every time.
func RecordUse(cacheRoot, repoTop string, uses []Use) error {
	rec := UseRecord{Repo: filepath.Clean(repoTop), Caches: uses}
	if rec.Caches == nil {
		rec.Caches = []Use{}
	}
	body, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return errs.Wrap(errs.Internal, "sqlite: encode the cache use record", err)
	}
	body = append(body, '\n')
	path := UseRecordPath(cacheRoot, repoTop)
	if cur, rerr := os.ReadFile(filepath.Clean(path)); rerr == nil && string(cur) == string(body) {
		return nil
	}
	if mkErr := os.MkdirAll(filepath.Dir(path), useDirMode); mkErr != nil {
		return errs.Wrap(errs.Internal, "sqlite: create the cache use folder", mkErr)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".use-*")
	if err != nil {
		return errs.Wrap(errs.Internal, "sqlite: write the cache use record", err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(body)
	cerr := tmp.Close()
	if werr == nil && cerr == nil {
		werr = os.Chmod(name, useFileMode)
	}
	if werr == nil && cerr == nil {
		werr = osrename.Rename(name, path)
	}
	if joined := errors.Join(werr, cerr); joined != nil {
		_ = os.Remove(name) // the write already failed; that error is the one to report
		return errs.Wrap(errs.Internal, "sqlite: write the cache use record", joined)
	}
	return nil
}

// RecordFile is one use record as read from disk.
type RecordFile struct {
	// Path is the record file's absolute path.
	Path string
	// Record is its content; Repo is "" when the file did not decode (a
	// damaged record: the caller treats it as dead, it lists nothing).
	Record UseRecord
}

// ReadUses lists every use record under cacheRoot, sorted by file path. A
// missing folder is no records.
func ReadUses(cacheRoot string) ([]RecordFile, error) {
	dir := filepath.Join(cacheRoot, UsesDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "sqlite: list the cache use records", err)
	}
	var out []RecordFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), useRecordExt) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		rf := RecordFile{Path: p}
		if b, rerr := os.ReadFile(filepath.Clean(p)); rerr == nil {
			if json.Unmarshal(b, &rf.Record) != nil {
				rf.Record = UseRecord{}
			}
		}
		out = append(out, rf)
	}
	return out, nil
}

// File is one cache file on disk.
type File struct {
	// Rel is the path relative to the cache root, slash-separated (the
	// form Use.Path holds).
	Rel string
	// Size is the file's size plus its WAL sidecars'.
	Size int64
}

// StoreCaches lists every cache file (*.db) under cacheRoot's stores/ and
// combined/ folders, sorted by Rel. Moved-aside damaged copies
// (<name>.db.corrupt-<time>) are not cache files and are never listed:
// they are kept on purpose for a person to inspect.
func StoreCaches(cacheRoot string) ([]File, error) {
	var out []File
	for _, top := range []string{StoresDir, CombinedDir} {
		root := filepath.Join(cacheRoot, top)
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				if errors.Is(werr, fs.ErrNotExist) && p == root {
					return fs.SkipDir
				}
				return werr
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), cacheFileExt) {
				return nil
			}
			rel, err := filepath.Rel(cacheRoot, p)
			if err != nil {
				return err
			}
			out = append(out, File{Rel: filepath.ToSlash(rel), Size: sizeWithSidecars(p)})
			return nil
		})
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "sqlite: list the store caches", err)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, nil
}

// sizeWithSidecars is p's size plus its -wal and -shm files' (0 for any
// that is missing).
func sizeWithSidecars(p string) int64 {
	var n int64
	for _, s := range slices.Concat([]string{""}, cacheFileSidecars) {
		if fi, err := os.Stat(p + s); err == nil {
			n += fi.Size()
		}
	}
	return n
}

// RemoveCache deletes the cache file rel (relative to cacheRoot, as
// StoreCaches lists it) with its WAL sidecars, then the folders it leaves
// empty up to (not including) stores/ or combined/. rel must name a .db
// file under one of those two folders: anything else is refused, so a
// damaged record can never point the delete outside the cache.
func RemoveCache(cacheRoot, rel string) error {
	p, top, err := cachePathIn(cacheRoot, rel)
	if err != nil {
		return err
	}
	for _, s := range slices.Concat([]string{""}, cacheFileSidecars) {
		if rerr := os.Remove(p + s); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return errs.Wrap(errs.Internal, "sqlite: remove the cache "+rel, rerr)
		}
	}
	for dir := filepath.Dir(p); dir != top && strings.HasPrefix(dir, top+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil { // not empty (or gone): stop climbing
			break
		}
	}
	return nil
}

// RemoveUseRecord deletes one record file returned by ReadUses; path must
// sit directly in cacheRoot's repos/ folder.
func RemoveUseRecord(cacheRoot, path string) error {
	dir := filepath.Join(cacheRoot, UsesDir)
	if filepath.Dir(filepath.Clean(path)) != dir || !strings.HasSuffix(path, useRecordExt) {
		return errs.New(errs.Internal, "sqlite: refusing to remove "+path+": not a cache use record")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return errs.Wrap(errs.Internal, "sqlite: remove the cache use record", err)
	}
	return nil
}

// cachePathIn resolves rel under cacheRoot and checks it is a .db file
// inside stores/ or combined/; it returns the absolute path and that top
// folder.
func cachePathIn(cacheRoot, rel string) (path, top string, err error) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || !strings.HasSuffix(clean, cacheFileExt) {
		return "", "", errs.New(errs.Internal, "sqlite: refusing to remove "+rel+": not a cache file")
	}
	first, _, _ := strings.Cut(filepath.ToSlash(clean), "/")
	if first != StoresDir && first != CombinedDir {
		return "", "", errs.New(errs.Internal, "sqlite: refusing to remove "+rel+": outside the store caches")
	}
	return filepath.Join(cacheRoot, clean), filepath.Join(cacheRoot, first), nil
}
