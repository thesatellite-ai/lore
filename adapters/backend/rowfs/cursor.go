package rowfs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store"
)

// statCursorVersion is the format of StatCursor's JSON; a cursor with any
// other version reads as "from the beginning".
const statCursorVersion = 1

// StatCursor is the folder backend's cursor (spec 5.2): the stat of every
// row file seen by the last Read plus when that Read started. It is opaque
// to the core and travels as JSON in a store.Cursor.
//
// Invariant: Files is sorted by Path and names only files that decoded as
// rows; a file that failed to decode is left out so the next Read tries it
// again.
type StatCursor struct {
	Version int        `json:"v"`
	At      int64      `json:"at"` // unix nanoseconds when the Read started
	Files   []FileStat `json:"files"`
}

// FileStat is one row file in a StatCursor. Path is relative to the
// folder's data dir, slash-separated.
type FileStat struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	MTime  int64  `json:"mtime"` // unix nanoseconds
	SHA256 string `json:"sha256"`
	// Blob is the file's git blob id when git reported it unchanged in the
	// work tree at that Read (IndexView); a later Read under the same blob
	// id knows the file without touching it. Empty otherwise.
	Blob string `json:"blob,omitempty"`
}

// ParseStatCursor reads c; ok is false for the empty cursor and for
// anything unreadable, which callers treat as "from the beginning".
func ParseStatCursor(c store.Cursor) (StatCursor, bool) {
	if c == "" {
		return StatCursor{}, false
	}
	var sc StatCursor
	if err := json.Unmarshal([]byte(c), &sc); err != nil || sc.Version != statCursorVersion {
		return StatCursor{}, false
	}
	return sc, true
}

// Encode renders the cursor. Files are sorted first so equal states give
// equal cursors.
func (c StatCursor) Encode() (store.Cursor, error) {
	c.Version = statCursorVersion
	slices.SortFunc(c.Files, func(a, b FileStat) int {
		switch {
		case a.Path < b.Path:
			return -1
		case a.Path > b.Path:
			return 1
		}
		return 0
	})
	b, err := json.Marshal(c)
	if err != nil {
		return "", errs.Wrap(errs.Internal, "rowfs: encode the cursor", err)
	}
	return store.Cursor(b), nil
}

// byPath indexes the cursor's files.
func (c StatCursor) byPath() map[string]FileStat {
	m := make(map[string]FileStat, len(c.Files))
	for _, f := range c.Files {
		m[f.Path] = f
	}
	return m
}

// racy reports whether a file with mtime may have changed again after a
// cursor taken at `at` recorded it (git's racily-clean rule).
func racy(mtime, at int64) bool { return mtime >= at-int64(RacyWindow) }

// sum is the hex sha256 of b.
func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// nanos is t in unix nanoseconds.
func nanos(t time.Time) int64 { return t.UnixNano() }
