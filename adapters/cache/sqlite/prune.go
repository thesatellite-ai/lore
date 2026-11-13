package sqlite

// prune.go — the cache layout helpers `lore cache prune` needs (FLAG3).
//
// FLAG3: cache prune only removes caches no repo on this machine attaches
// any more, because caches always rebuild from row files and old client
// data must not linger. lore kept no list of the repos that use a cache, so
// every core open records one: <cache root>/uses/<sha256 of repo top>.json
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
	// per repo (FLAG3). Why not "repos": it sat beside the repo caches'
	// "repo" folder, two near-identical names a person could not tell
	// apart (B2-6); moveOldUsesDir moves an old one on first use.
	UsesDir = "uses"
	// oldUsesDir is where the use records lived before 0.1.18's B2-6 fix.
	oldUsesDir = "repos"
	// StoresDir is the folder under the cache root holding the per-store
	// caches (<store id>/<branch>/<folder>.db, spec 7.4). It matches
	// wire's cacheStoresDir.
	StoresDir = "stores"
	// CombinedDir is the folder under the cache root holding the CFG3
	// combined caches. It matches wire's combinedDir.
	CombinedDir = "combined"
	// RepoDir is the folder under the cache root holding one repo-store
	// cache per checkout (RepoCacheRel). Its name comes from the checkout's
	// path, so a checkout that moves gets a new file and the old one is
	// left behind for `lore cache prune`.
	RepoDir = "repo"
	// repoCacheHashLen is how many hex digits of the path's sha256 a repo
	// cache name keeps: two checkouts of one repo name get two files.
	repoCacheHashLen = 12
	// repoCacheFallback names a repo cache whose folder name has no usable
	// character.
	repoCacheFallback = "repo"
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

// moveOldUsesDir moves the use records of an older lore from
// <cacheRoot>/repos to <cacheRoot>/uses (B2-6): the folder is renamed when
// uses/ does not exist yet, else each record uses/ lacks is moved in and
// the old folder removed once empty. Records are rebuilt by the next open
// of each repo, so a record that cannot be moved is simply left; nothing
// fails because of it.
func moveOldUsesDir(cacheRoot string) {
	old := filepath.Join(cacheRoot, oldUsesDir)
	fi, err := os.Lstat(old)
	if err != nil || !fi.IsDir() {
		return
	}
	dst := filepath.Join(cacheRoot, UsesDir)
	if _, err := os.Lstat(dst); errors.Is(err, fs.ErrNotExist) {
		if osrename.Rename(old, dst) == nil {
			return
		}
	}
	entries, err := os.ReadDir(old)
	if err != nil {
		return
	}
	if os.MkdirAll(dst, useDirMode) != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), useRecordExt) {
			continue
		}
		to := filepath.Join(dst, e.Name())
		if _, serr := os.Lstat(to); serr == nil {
			_ = os.Remove(filepath.Join(old, e.Name())) // uses/ has the newer record
			continue
		}
		_ = osrename.Rename(filepath.Join(old, e.Name()), to)
	}
	_ = os.Remove(old) // only when empty: anything else in it stays
}

// UseRecordPath is where repoTop's record lives under cacheRoot.
func UseRecordPath(cacheRoot, repoTop string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(repoTop)))
	return filepath.Join(cacheRoot, UsesDir, hex.EncodeToString(sum[:])+useRecordExt)
}

// RepoCacheRel is the repo store's cache of the checkout at repoTop,
// relative to the cache root and slash-separated:
// repo/<folder name>-<12 hex of sha256(path)>.db. The folder name makes the
// file readable (`lore where` lists it); the hash of the symlink-resolved
// path gives two checkouts of one name two files. wire opens the cache at
// this name and `lore cache prune` keeps exactly the names of live repos,
// so both must compute it here. Nothing is created.
func RepoCacheRel(repoTop string) string {
	top := filepath.Clean(repoTop)
	if real, err := filepath.EvalSymlinks(top); err == nil {
		top = real
	}
	sum := sha256.Sum256([]byte(top))
	return RepoDir + "/" + repoCacheName(filepath.Base(top)) + "-" + hex.EncodeToString(sum[:])[:repoCacheHashLen] + cacheFileExt
}

// repoCacheName keeps a folder name to letters, digits, '.', '_' and '-'
// (anything else becomes '_'), so the file name is plain on every system.
func repoCacheName(base string) string {
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if n := strings.Trim(b.String(), "."); n != "" {
		return n
	}
	return repoCacheFallback
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
	moveOldUsesDir(cacheRoot)
	path := UseRecordPath(cacheRoot, repoTop)
	if cur, rerr := os.ReadFile(filepath.Clean(path)); rerr == nil && string(cur) == string(body) {
		return nil
	}
	if mkErr := os.MkdirAll(filepath.Dir(path), useDirMode); mkErr != nil {
		return errs.FileIO("create the cache use folder", filepath.Dir(path), mkErr)
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
	moveOldUsesDir(cacheRoot)
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

// RepoCaches lists every repo-store cache file (*.db) directly in
// cacheRoot's repo/ folder, sorted by Rel (moved-aside damaged copies are
// not listed, as in StoreCaches). A missing folder is no files.
func RepoCaches(cacheRoot string) ([]File, error) {
	dir := filepath.Join(cacheRoot, RepoDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "sqlite: list the repo caches", err)
	}
	var out []File
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), cacheFileExt) {
			continue
		}
		out = append(out, File{Rel: RepoDir + "/" + e.Name(), Size: sizeWithSidecars(filepath.Join(dir, e.Name()))})
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
// empty up to (not including) stores/, combined/ or repo/. rel must name a
// .db file under one of those folders: anything else is refused, so a
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
// sit directly in cacheRoot's uses/ folder.
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
// inside stores/, combined/ or repo/; it returns the absolute path and that top
// folder.
func cachePathIn(cacheRoot, rel string) (path, top string, err error) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || !strings.HasSuffix(clean, cacheFileExt) {
		return "", "", errs.New(errs.Internal, "sqlite: refusing to remove "+rel+": not a cache file")
	}
	first, _, _ := strings.Cut(filepath.ToSlash(clean), "/")
	if first != StoresDir && first != CombinedDir && first != RepoDir {
		return "", "", errs.New(errs.Internal, "sqlite: refusing to remove "+rel+": outside the store caches")
	}
	return filepath.Join(cacheRoot, clean), filepath.Join(cacheRoot, first), nil
}
