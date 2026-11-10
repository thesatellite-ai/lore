package rowfs

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
)

// On-disk names. Changing any of them moves every store's files, so each is
// a format decision (spec 6.1 layout).
// ds:def id=sync-datanames-dgnn2vn8 owner=@khanakia stability=stable desc="on-disk names: .lore, data, _meta.json, _purged.json, lock, .json row files"
const (
	// LoreDir is the lore folder inside the store root and every folder.
	LoreDir = ".lore"
	// DataDir is the folder under LoreDir that holds the row files.
	DataDir = "data"
	// MetaFileName holds a folder's identity and format version; it lives
	// in the data folder but is never a row.
	MetaFileName = "_meta.json"
	// PurgedFileName lists ids purged on purpose; never a row.
	PurgedFileName = "_purged.json"
	// LockFileName is the store lock file inside the root's LoreDir.
	LockFileName = "lock"
	// RowFileExt is the extension of every row file.
	RowFileExt = ".json"
	// tmpPrefix marks in-flight atomic writes; leftovers are crash debris.
	tmpPrefix = ".tmp-"
)

// FolderRoot names the store's root folder (<root>/.lore/data). The empty
// string means the same.
const FolderRoot = "."

// FolderOrg is the conventional company-wide folder (`org_folder: org`,
// CFG1). It is an ordinary folder name; the constant exists so callers do
// not spell it.
const FolderOrg = "org"

// MaxFileSize caps a row file (lsync/files.go:47). Real rows are a few KB;
// a multi-MB file is a mistake or hostile, and committing it would bloat
// every clone forever.
// ds:def id=sync-maxfilesize-8up39v74 owner=@khanakia stability=stable desc="row file size cap (4 MiB)"
const MaxFileSize = 4 << 20

// RacyWindow is git's "racily clean" rule (lsync/files.go:56): a file whose
// mtime is within this window of when its stat was recorded may have
// changed again inside the same timestamp tick, so it is always re-hashed.
const RacyWindow = 2 * time.Second

// staleTmpAge is how old a leftover tmp file must be before a scan removes
// it as crash debris (a younger one may belong to a running writer).
const staleTmpAge = time.Minute

// File modes. Owner-only by default: a folder store of an internal store
// is internal, and git records only the executable bit, so nothing is lost
// for git stores. A repo checkout's .lore/ (WithSharedModes, and the
// repo-only WriteRepoMeta / DeclareRepo) is committed and read by everyone
// who clones it, so its files get git's usual work-tree modes.
const (
	dirMode  = 0o750
	fileMode = 0o600
	// sharedDirMode and sharedFileMode: a committed work tree's modes.
	sharedDirMode  = 0o755
	sharedFileMode = 0o644
)

// fileModes is the mode pair one write uses for the file and any folder it
// creates.
type fileModes struct{ file, dir fs.FileMode }

var (
	// privateModes: owner-only (folder stores, the journal, markers).
	privateModes = fileModes{file: fileMode, dir: dirMode}
	// sharedModes: a repo checkout's committed .lore/ files.
	sharedModes = fileModes{file: sharedFileMode, dir: sharedDirMode}
)

// folderSegment is one segment of a folder name: it cannot be empty, "."
// or "..", and cannot start with a dot (which keeps .lore, .git and other
// tool folders out of reach).
var folderSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// OSJunkPatterns are the files operating systems drop into browsed
// folders, as gitignore / path.Match globs on a base name: Finder's
// .DS_Store and AppleDouble ._name files, Windows Explorer's Thumbs.db and
// desktop.ini. Never lore data: the folder reader skips them, doctor and
// the pre-commit hook ignore them (adapters/dataignore), and the git
// wiring writes them, in this order, as ignore lines. The one list.
var OSJunkPatterns = []string{".DS_Store", "Desktop.ini", "Thumbs.db", "desktop.ini", "._*"}

// IsOSJunk reports whether a file's base name (a slash path is reduced to
// its last element) is operating-system clutter.
func IsOSJunk(name string) bool {
	base := path.Base(strings.TrimSuffix(name, "/"))
	for _, p := range OSJunkPatterns {
		if ok, _ := path.Match(p, base); ok {
			return true
		}
	}
	return false
}

// CleanFolder validates a folder name and returns its canonical form:
// FolderRoot for the root, else slash-separated segments ("acme-hub",
// "org", "team/web"). Anything that could escape the root (absolute,
// "..", a dot segment, a backslash) is E_BAD_PATH.
func CleanFolder(name string) (string, error) {
	if name == "" || name == FolderRoot {
		return FolderRoot, nil
	}
	for seg := range strings.SplitSeq(name, "/") {
		if !folderSegment.MatchString(seg) {
			return "", errs.New(errs.BadPath, "rowfs: folder name "+quote(name)+" must be slash-separated plain segments")
		}
	}
	return name, nil
}

// RowPath is the path of a row file relative to its folder's data dir,
// slash-separated so bookkeeping is the same on every OS.
func RowPath(table row.Table, id ids.AnyID) string {
	return table.String() + "/" + id.String() + RowFileExt
}

// FolderDataPath is the data dir of folder, relative to the store root and
// slash-separated (".lore/data" or "org/.lore/data"). folder must be clean.
func FolderDataPath(folder string) string {
	if folder == FolderRoot {
		return LoreDir + "/" + DataDir
	}
	return folder + "/" + LoreDir + "/" + DataDir
}

// ParseRowPath splits "table/id.json"; ok is false for anything that is not
// a row file name (a control file, a stray file, a bad id).
func ParseRowPath(rel string) (row.Table, ids.AnyID, bool) {
	dir, file := path.Split(rel)
	table, err := row.ParseTable(strings.TrimSuffix(dir, "/"))
	if err != nil || !strings.HasSuffix(file, RowFileExt) {
		return "", ids.AnyID{}, false
	}
	id, err := ids.ParseAny(strings.TrimSuffix(file, RowFileExt))
	if err != nil {
		return "", ids.AnyID{}, false
	}
	return table, id, true
}

// maxQuoted bounds how much of a bad value an error repeats.
const maxQuoted = 64

// quote renders a value for an error message, truncated.
func quote(s string) string {
	if len(s) > maxQuoted {
		s = s[:maxQuoted] + "…"
	}
	return `"` + s + `"`
}
