package rowfs

import (
	"bytes"

	"github.com/thesatellite-ai/lore/logic/merge3"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// markerStart is the cheap pre-check: no document without it can hold a
// conflict block, so the fields are only walked when the bytes have it.
var markerStart = []byte("<<<<<<< ")

// decodeImportable is DecodeRowFile for the import path (Read, Problems):
// a document whose text field still holds the conflict markers of an
// unresolved merge is refused (STORE_SERVER_SPEC merge rule 2: "refused
// on import until resolved"), so marker text never reaches the cache as
// knowledge. Get keeps reading such a file as it is, so a person or
// `lore sync` can show and resolve it.
func decodeImportable(rel string, doc []byte) (store.Row, error) {
	r, err := DecodeRowFile(rel, doc)
	if err != nil || !bytes.Contains(doc, markerStart) {
		return r, err
	}
	full, err := row.Decode(doc)
	if err != nil {
		return store.Row{}, err
	}
	for name, v := range full.Fields {
		if hasMarkers(v) {
			return store.Row{}, errs.New(errs.InvalidInput, "rowfs: "+quote(rel)+": field "+name+" holds unresolved conflict markers; resolve them in the file and commit")
		}
	}
	return r, nil
}

// hasMarkers reports a text value, or a list item, holding a conflict
// block (merge3.ContainsConflictMarkers).
func hasMarkers(v value.Value) bool {
	switch t := v.(type) {
	case value.Text:
		return merge3.ContainsConflictMarkers(t.String())
	case value.List:
		for _, it := range t.Items() {
			if hasMarkers(it) {
				return true
			}
		}
	}
	return false
}
