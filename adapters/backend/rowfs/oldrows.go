package rowfs

// oldrows.go — counting a folder's rows written in an older row format
// without decoding the rest (devco:D-1016, bug hunt H-278).
//
// lore doctor (and lore migrate status) counts the v1 rows of every store
// folder the repo does not attach. It read each such folder whole, which
// decoded every row file of it: ~3 s of a doctor on a 100k-commit store
// whose rows are all current, and every row held at once. A file names its
// format in its top-level "_v", so only a file that declares an older
// format (or none that reads as a number) is decoded; the counts are the
// same.

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"

	"github.com/thesatellite-ai/lore/model/row"
)

// oldRowsDecodeHook, when set (tests only), is called with each file
// OldRows decodes in full.
var oldRowsDecodeHook func(rel string)

// OldRows counts, per table, the rows of f whose files decode to a row
// format below below (row.Row.SourceVersion), reading the files on disk
// as a full Read does; a file that does not decode is not counted.
func (f *Folder) OldRows(ctx context.Context, below int) (map[row.Table]int, error) {
	files, _, err := f.scan()
	if err != nil {
		return nil, err
	}
	rels := sortedKeys(files)
	old := make([]row.Table, len(rels)) // the table of an old row, "" otherwise
	if err := parallel(ctx, len(rels), func(i int) {
		b, err := readCapped(filepath.Join(f.dataDir, filepath.FromSlash(rels[i])), rels[i])
		if err != nil {
			return
		}
		if v, ok := declaredVersion(b); ok && v >= below {
			return // current: nothing to count, nothing to decode
		}
		if oldRowsDecodeHook != nil {
			oldRowsDecodeHook(rels[i])
		}
		sr, err := decodeImportable(rels[i], b)
		if err != nil {
			return
		}
		if r, err := sr.Decode(); err == nil && r.SourceVersion < below {
			old[i] = sr.Table
		}
	}); err != nil {
		return nil, err
	}
	out := map[row.Table]int{}
	for _, t := range old {
		if t != "" {
			out[t]++
		}
	}
	return out, nil
}

// declaredVersion is the integer a row document's top-level "_v" holds,
// read without decoding the rest; false when the document has none that
// reads as one (it is then decoded, which says why).
func declaredVersion(doc []byte) (int, bool) {
	dec := json.NewDecoder(bytes.NewReader(row.TrimBOM(doc)))
	dec.UseNumber()
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return 0, false
	}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return 0, false
		}
		if k != row.KeyVersion {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return 0, false
			}
			continue
		}
		v, err := dec.Token()
		if err != nil {
			return 0, false
		}
		n, ok := v.(json.Number)
		if !ok {
			return 0, false
		}
		i, err := strconv.Atoi(n.String())
		return i, err == nil
	}
	return 0, false
}
