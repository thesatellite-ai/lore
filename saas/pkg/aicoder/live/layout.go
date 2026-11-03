package live

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Ref names and tree layout (§1 "Refs", "Files and folders", §2).
const (
	// RefLive is a code repo's one live ref.
	RefLive = "refs/lore/live"
	// RefBackupPrefix holds local-only copies of the live ref kept before a
	// compaction, a restore or a remote rewrite (never pushed; the last
	// BackupsKept are kept).
	RefBackupPrefix = "refs/lore-backup/live/"
	// RootFolder is the folder of a project at the repo root.
	RootFolder = "_root"
	// reservedPrefix marks folder names lore keeps (fixed rule 3).
	reservedPrefix = "_"
	// metaFolder holds the live ref's own files.
	metaFolder = "_live"
	// EpochPath is the compaction epoch (F8).
	EpochPath = metaFolder + "/epoch"
	// MetaPath holds min_lore_version and features.
	MetaPath = metaFolder + "/meta.json"
	// PurgedPath lists ids removed with `lore live purge` (§6.6): they never
	// come back from a stale clone.
	PurgedPath = metaFolder + "/purged.json"
	// rowExt is the row file extension, as in .lore/data.
	rowExt = ".json"
)

// StoreRef is a store branch's live ref (fixed rule 3: a store has one per
// store branch and never a plain refs/lore/live).
func StoreRef(branch string) string { return RefLive + "/" + branch }

// FirstEpoch is the epoch of a live ref that was never compacted.
const FirstEpoch = 1

// FolderFor maps a project's path relative to the repo top level ("" or
// "." for the root) to its folder in the live tree.
func FolderFor(projectRel string) string {
	p := strings.Trim(path.Clean("/"+strings.ReplaceAll(projectRel, "\\", "/")), "/")
	if p == "" || p == "." {
		return RootFolder
	}
	return p
}

// ValidateFolder refuses a folder a project may not use: empty, or a
// reserved name (fixed rule 3).
func ValidateFolder(folder string) error {
	if folder == "" {
		return fmt.Errorf("live: empty project folder")
	}
	first, _, _ := strings.Cut(folder, "/")
	if isReservedFolder(first) {
		return fmt.Errorf("live: folder %q is reserved (names starting with %q)", folder, reservedPrefix)
	}
	return nil
}

// RowPath is a row's path inside the live tree.
func RowPath(folder, table, id string) string {
	return folder + "/" + table + "/" + id + rowExt
}

// RowKey names one row inside one project folder.
type RowKey struct {
	Table string
	ID    string
}

// String is "table/id".
func (k RowKey) String() string { return k.Table + "/" + k.ID }

// ParseRowPath splits a tree path into folder, table and id. Paths under
// the ref's own _live/ folder and anything that is not <folder>/<table>/<id>.json
// return ok=false.
func ParseRowPath(p string) (folder string, k RowKey, ok bool) {
	if strings.HasPrefix(p, metaFolder+"/") || !strings.HasSuffix(p, rowExt) {
		return "", RowKey{}, false
	}
	// At least <folder>/<table>/<id>.json; the folder may itself hold
	// slashes, so only the last two parts are fixed.
	const minRowPathParts = 3
	parts := strings.Split(p, "/")
	if len(parts) < minRowPathParts {
		return "", RowKey{}, false
	}
	n := len(parts)
	id := strings.TrimSuffix(parts[n-1], rowExt)
	if id == "" || parts[n-2] == "" {
		return "", RowKey{}, false
	}
	return strings.Join(parts[:n-2], "/"), RowKey{Table: parts[n-2], ID: id}, true
}

// Tree is a live ref's tree: root-relative path → file bytes.
type Tree map[string][]byte

// Rows decodes one project folder's rows. Files that fail to decode are
// returned in bad (path → error) and never silently dropped.
func (t Tree) Rows(folder string) (rows map[RowKey]File, bad map[string]error) {
	rows, bad = map[RowKey]File{}, map[string]error{}
	prefix := folder + "/"
	for p, b := range t {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		f, k, ok := ParseRowPath(p)
		if !ok || f != folder {
			continue
		}
		file, err := DecodeFile(b)
		if err != nil {
			bad[p] = err
			continue
		}
		rows[k] = file
	}
	return rows, bad
}

// Folders lists the project folders present in the tree, sorted.
func (t Tree) Folders() []string {
	set := map[string]bool{}
	for p := range t {
		if f, _, ok := ParseRowPath(p); ok {
			set[f] = true
		}
	}
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// Epoch reads _live/epoch (FirstEpoch when absent or unreadable: a ref
// written before epochs existed counts as never compacted).
func (t Tree) Epoch() int {
	b, ok := t[EpochPath]
	if !ok {
		return FirstEpoch
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n < FirstEpoch {
		return FirstEpoch
	}
	return n
}

// encodeEpoch renders an epoch file.
func encodeEpoch(n int) []byte { return []byte(strconv.Itoa(n) + "\n") }
