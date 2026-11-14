package store

import (
	"context"

	"github.com/thesatellite-ai/lore/model/row"
)

// A row mirror keeps the rows a backend last decoded, keyed by the path of
// their file and the exact identity of its content (a git blob id), so a
// warm list of a big table needs no row file opened (perf3, D-110): the
// backend asks its version control which files are unchanged and with
// which blob id, and takes those rows from the mirror. Unlike the cache's
// `_rows` (newest HLC wins), an entry is used only for the very same bytes,
// so a checkout of an older branch still lists exactly what is on disk.

// MirrorEntry is one row file's decoded row under its blob id. Path is
// relative to the data folder, slash-separated.
type MirrorEntry struct {
	Path string
	Blob string
	Row  Row
}

// RowMirror is one source's mirror. Both calls are best effort for the
// caller: a failed Lookup reads the files, a failed Keep keeps nothing.
type RowMirror interface {
	// Lookup returns the held rows of table whose path and blob id are in
	// want (path → blob id), keyed by path.
	Lookup(ctx context.Context, table row.Table, want map[string]string) (map[string]Row, error)
	// Keep stores entries, replacing what was held for their paths.
	Keep(ctx context.Context, entries []MirrorEntry) error
}

// MirrorCache is a Cache that can hold row mirrors (the SQLite cache).
type MirrorCache interface {
	Mirror(source Source) RowMirror
}

// MirroredReader is an optional Backend capability: a full read of one
// table served from m for every file the backend can prove unchanged.
// ok is false when it cannot prove anything (no version control view), and
// the caller reads the table as before.
//
// Callers never call it past a wrapper (a wrapper such as the live-ref
// layer changes what a read returns): they hand the mirror down the
// context (WithRowMirror) and call the outer Backend's Read, and the
// MirroredReader uses it when the read reaches it.
type MirroredReader interface {
	ReadTableMirrored(ctx context.Context, table row.Table, m RowMirror) (rows []Row, ok bool, err error)
	// ReadAllMirrored is Read(ctx, "") through m: every row and the cursor
	// for the next Read.
	ReadAllMirrored(ctx context.Context, m RowMirror) (rows []Row, next Cursor, ok bool, err error)
}

// rowMirrorKey is the context key of WithRowMirror.
type rowMirrorKey struct{}

// boundMirror is a mirror for one MirroredReader.
type boundMirror struct {
	reader MirroredReader
	m      RowMirror
}

// WithRowMirror returns ctx carrying m for reads that reach r (perf3).
func WithRowMirror(ctx context.Context, r MirroredReader, m RowMirror) context.Context {
	return context.WithValue(ctx, rowMirrorKey{}, boundMirror{reader: r, m: m})
}

// RowMirrorFor is the mirror ctx carries for r, nil when none: a read that
// reaches another backend never uses it.
func RowMirrorFor(ctx context.Context, r MirroredReader) RowMirror {
	b, ok := ctx.Value(rowMirrorKey{}).(boundMirror)
	if !ok || b.reader != r {
		return nil
	}
	return b.m
}
