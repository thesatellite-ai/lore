package rowfs

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/merge3"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// markerStart is the cheap pre-check: no document without it can hold a
// conflict block written with a raw '<', so the fields are only walked
// when the bytes have it, or when the document escapes '<' (escapedLess).
var markerStart = []byte("<<<<<<<")

// escapedLess is '<' as a JSON escape (\u003c, any hex case). lore's own
// writers emit a raw '<', but other JSON encoders (Go's json.Marshal by
// default) write \u003c, so "<<<<<<< " never appears in the bytes while the
// decoded text still holds the markers: such a document is walked too.
var escapedLess = [][]byte{[]byte(`\u003c`), []byte(`\u003C`)}

// mayHoldMarkers is the pre-check on the raw bytes: a raw marker start, or
// any escaped '<' (case-insensitive hex).
func mayHoldMarkers(doc []byte) bool {
	if bytes.Contains(doc, markerStart) {
		return true
	}
	for _, e := range escapedLess {
		if bytes.Contains(doc, e) {
			return true
		}
	}
	return false
}

// fieldMarkersError is decodeImportable's refusal of a row that decodes
// but holds a conflict block in a text field or meta entry: lists leave it
// out, while Get still reads it (so it can be shown and settled). Problem
// carries the difference as FieldMarkers; the text and code are the
// wrapped *errs.Error's.
type fieldMarkersError struct{ err *errs.Error }

// Unwrap exposes the *errs.Error (errs.CodeOf, errors.As).
func (e fieldMarkersError) Unwrap() error { return e.err }

// Error is the wrapped error's text.
func (e fieldMarkersError) Error() string { return e.err.Error() }

// isFieldMarkers reports a fieldMarkersError in err's chain.
func isFieldMarkers(err error) bool {
	var m fieldMarkersError
	return errors.As(err, &m)
}

// decodeImportable is DecodeRowFile for the import path (Read, Problems):
// a document whose text field, or any string inside a meta entry, still
// holds the conflict markers of an unresolved merge is refused
// (STORE_SERVER_SPEC merge rule 2: "refused on import until resolved";
// METADATA 3.12: every conflict scanner recurses into meta), so marker
// text never reaches the cache as knowledge. A structured meta conflict
// entry ({"conflict": {base, ours, theirs}}) is NOT refused here: 3.12
// imports the row with its other values, and the commit and landing
// checks hold it back (merge.MetaConflicts). Get keeps reading such a
// file as it is, so a person or `lore sync` can show and resolve it.
func decodeImportable(rel string, doc []byte) (store.Row, error) {
	r, err := DecodeRowFile(rel, doc)
	if err != nil || !mayHoldMarkers(doc) {
		return r, err
	}
	full, err := row.Decode(doc)
	if err != nil {
		return store.Row{}, err
	}
	for name, v := range full.Fields {
		if hasMarkers(v) {
			return store.Row{}, fieldMarkersError{errs.New(errs.InvalidInput, "rowfs: "+quote(rel)+": field "+name+" holds unresolved conflict markers; resolve them in the file and commit")}
		}
	}
	keys := slices.SortedFunc(maps.Keys(full.Meta), func(a, b ids.ID[ids.MetaKey]) int { return strings.Compare(a.String(), b.String()) })
	for _, k := range keys { // by id: the same first entry named on every machine
		if rawHasMarkers(full.Meta[k].Raw()) {
			return store.Row{}, fieldMarkersError{errs.New(errs.InvalidInput, "rowfs: "+quote(rel)+": meta entry "+k.String()+" holds unresolved conflict markers; resolve them in the file and commit")}
		}
	}
	return r, nil
}

// rawHasMarkers reports a JSON document holding a conflict block in any
// string at any depth (object keys included: a hand edit can put one
// anywhere). A document that does not decode is not this check's business
// (row.Decode already accepted it).
func rawHasMarkers(raw []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	for {
		t, err := dec.Token()
		if errors.Is(err, io.EOF) || err != nil {
			return false
		}
		if s, ok := t.(string); ok && merge3.ContainsConflictMarkers(s) {
			return true
		}
	}
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
